package fixture

import (
	"context"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

const pagingDemo = `
tenants:
  - name: Acme Co
    users:
      - name: admin
        password: ion
    elements:
      - name: rtr-01
        pages: true
        page_lines: 4
        responses:
          help: |
            line one
            line two
            line three
            line four
            line five
            line six
`

func TestASessionRunsTheRealLoginAndReturnsFixtureOutput(t *testing.T) {
	s := load(t, demo)
	el := s.Tenants[0].Elements[0]

	outs, err := s.SessionClient("set paging off", nil).
		ToolkitSession(t.Context(), el.ID, el.Name+"#", "admin", "ion", []string{"dump device status"})
	if err != nil {
		t.Fatalf("ToolkitSession() error = %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d command outputs, want 1", len(outs))
	}
	// Exactly the fixture's text: the echoed command and the prompt must not
	// leak into the output, which is what the clean-up code is for.
	want := "Serial     : FAKE0001\nUptime     : 4 days"
	if outs[0].Output != want {
		t.Errorf("output = %q, want %q", outs[0].Output, want)
	}
}

func TestTheWrongPasswordIsRejectedRatherThanAccepted(t *testing.T) {
	s := load(t, demo)
	el := s.Tenants[0].Elements[0]

	ctx, cancel := contextWithTimeout(t, 2*time.Second)
	defer cancel()

	// A fixture that let any password through would make autologin look right
	// when it was sending the wrong thing.
	_, err := s.SessionClient("", nil).
		ToolkitSession(ctx, el.ID, el.Name+"#", "admin", "wrong", []string{"dump device status"})
	if err == nil {
		t.Fatal("the wrong password logged in")
	}
	if !strings.Contains(err.Error(), "Login incorrect") {
		t.Errorf("error = %q, want the device's rejection in the tail", err)
	}
}

func TestPagedOutputMatchesWhateverStoppedThePager(t *testing.T) {
	s := load(t, pagingDemo)
	el := s.Tenants[0].Elements[0]
	want := "line one\nline two\nline three\nline four\nline five\nline six"

	cases := map[string]struct {
		pagerOff string
		pagers   []string
	}{
		"paging turned off":  {"set paging off", nil},
		"scraper only":       {"", nil},
		"both, the defaults": {"set paging off", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			outs, err := s.SessionClient(tc.pagerOff, tc.pagers).
				ToolkitSession(t.Context(), el.ID, el.Name+"#", "admin", "ion", []string{"help"})
			if err != nil {
				t.Fatalf("ToolkitSession() error = %v", err)
			}
			if outs[0].Output != want {
				t.Errorf("output = %q, want %q", outs[0].Output, want)
			}
		})
	}
}

func TestWithNothingAnsweringThePagerTheReadTimesOutCarryingTheTail(t *testing.T) {
	s := load(t, pagingDemo)
	el := s.Tenants[0].Elements[0]

	ctx, cancel := contextWithTimeout(t, 500*time.Millisecond)
	defer cancel()

	// The bug phase 0b fixed: without either defence a paging command uses up
	// the whole session timeout, and the tail is the only clue why.
	_, err := s.SessionClient("", []string{}).
		ToolkitSession(ctx, el.ID, el.Name+"#", "admin", "ion", []string{"help"})
	if err == nil {
		t.Fatal("the read returned although nothing answered the pager")
	}
	if !strings.Contains(err.Error(), "--More--") {
		t.Errorf("error = %q, want the pager prompt in the tail", err)
	}
}

func TestAnUnknownCommandAnswersPlausibly(t *testing.T) {
	s := load(t, demo)
	el := s.Tenants[0].Elements[0]

	outs, err := s.SessionClient("", nil).
		ToolkitSession(t.Context(), el.ID, el.Name+"#", "admin", "ion", []string{"dump nonsense"})
	if err != nil {
		t.Fatalf("ToolkitSession() error = %v", err)
	}
	// Empty output would teach a tutorial nothing; the failure case is one of
	// the things a fixture exists to show.
	if !strings.Contains(outs[0].Output, "unknown command") || !strings.Contains(outs[0].Output, "dump nonsense") {
		t.Errorf("output = %q, want it to name the command it rejected", outs[0].Output)
	}
}

func TestDialingAnElementTheFixtureDoesNotHaveLooksLikeARejectedHandshake(t *testing.T) {
	s := load(t, demo)
	_, err := s.Dialer().DialToolkit(t.Context(), "404", 80, 24)
	if err == nil {
		t.Fatal("dialling an unknown element succeeded")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want the shape a rejected handshake has", err)
	}
}

func TestATUISessionLogsInThroughTheRealAutologin(t *testing.T) {
	s := load(t, demo)
	el := s.Tenants[0].Elements[0]

	sess, err := ionterm.Open(t.Context(), s.Dialer(), el.ID, el.Name, 80, 24,
		ionterm.Credentials{User: "admin", Pass: "ion"}, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(sess.Close)

	// The pane is what the user sees, so the prompt arriving there is the proof
	// that autologin answered both prompts.
	waitFor(t, func() bool { return strings.Contains(pane(sess), el.Name+"#") })
	if got := pane(sess); strings.Contains(got, "Login incorrect") {
		t.Errorf("autologin was rejected:\n%s", got)
	}

	sess.SendText("dump device status\r")
	waitFor(t, func() bool { return strings.Contains(pane(sess), "FAKE0001") })

	sess.SendText("exit\r")
	waitFor(t, func() bool { return sess.Ended() })
	if err := sess.Err(); err != nil {
		t.Errorf("Err() = %v after exit, want a clean hang-up", err)
	}
}

func pane(s *ionterm.Session) string { return s.Render() }

// waitFor polls because the device answers on its own goroutine; a session has
// no "settled" to wait on.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the device")
}

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), d)
}

var _ ioncli.Conn = (*conn)(nil)
