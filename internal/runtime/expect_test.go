package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/script"
)

func openBoth(t *testing.T) (*Runtime, string) {
	t.Helper()
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	if err := rt.Exec(&Call{Ctx: context.Background()}, "session.open"); err != nil {
		t.Fatalf("session.open: %v", err)
	}
	t.Cleanup(rt.Sessions().CloseAll)
	for _, el := range rt.Elements() {
		if el.Name == "acme-rtr-01" {
			return rt, el.ID
		}
	}
	t.Fatal("the fleet has no acme-rtr-01")
	return nil, ""
}

func send(t *testing.T, rt *Runtime, args map[string]string) (SentLine, time.Duration) {
	t.Helper()
	c := &Call{Ctx: context.Background(), Args: args}
	start := time.Now()
	if err := rt.Exec(c, "session.send"); err != nil {
		t.Fatalf("session.send %v: %v", args, err)
	}
	got, _ := c.Result.(SentLine)
	return got, time.Since(start)
}

func TestASendReturnsWhenThePromptComesBack(t *testing.T) {
	rt, first := openBoth(t)
	got, _ := send(t, rt, map[string]string{"element": first, "text": "dump device status"})
	if out := got.Output; !strings.Contains(out, "FAKE0001") || strings.Contains(out, "acme-rtr-01#") {
		t.Errorf("sent to acme-rtr-01 while acme-rtr-02 was active: %v, want acme-rtr-01's own answer without its prompt", got)
	}
	if w := rt.Waits(); len(w) != 0 {
		t.Errorf("the finished send is still listed as waiting: %+v", w)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := rt.Exec(&Call{Ctx: ctx, Args: map[string]string{"element": first, "text": "dump device status", "expect": "never comes back"}}, "session.send")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), `"never comes back" never showed up on acme-rtr-01`) {
		t.Errorf("expecting text that never comes = %v, want the send to wait until the run ended and say what it waited for", err)
	}

	err = rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"element": first, "text": "uptime", "wait": "2s"}}, "session.send")
	if err == nil || !strings.Contains(err.Error(), "wait is not an argument of session.send") {
		t.Errorf("a send given a wait = %v, want it refused, since a send waits for the answer", err)
	}
}

func TestASessionIsClosedByItsElement(t *testing.T) {
	rt, first := openBoth(t)
	if err := rt.Exec(&Call{Args: map[string]string{"element": first}}, "session.close"); err != nil {
		t.Fatal(err)
	}
	if all := rt.Sessions().All(); len(all) != 1 || all[0].Name != "acme-rtr-02" {
		t.Errorf("after closing acme-rtr-01 the sessions are %d, want acme-rtr-02 alone", len(all))
	}
	err := rt.Exec(&Call{Args: map[string]string{"element": first}}, "session.close")
	if err == nil || !strings.Contains(err.Error(), "no session is open or opening on element "+first) {
		t.Errorf("closing it twice = %v", err)
	}
}

func TestAWaitSaysWhatAndHowLong(t *testing.T) {
	start := time.Now()
	for _, tt := range []struct {
		wait Wait
		at   time.Duration
		want string
	}{
		{Wait{What: "S7030-RTR-01: debug bw-test", Since: start}, 65 * time.Second,
			"S7030-RTR-01: debug bw-test · 1m5s"},
		{Wait{What: "dump vpn summary on 2 element(s)", Since: start}, 2 * time.Second,
			"dump vpn summary on 2 element(s) · 2s"},
	} {
		if got := tt.wait.Line(start.Add(tt.at)); got != tt.want {
			t.Errorf("Line = %q, want %q", got, tt.want)
		}
	}
}

func TestALineThatIsntReadOnlyIsOnlyListedWithoutDoit(t *testing.T) {
	rt, first := openBoth(t)
	var said []string
	c := &Call{Ctx: context.Background(), Args: map[string]string{"element": first, "text": "config interface 3 shutdown"},
		Out: func(l string) { said = append(said, l) }}
	if err := rt.Exec(c, "session.send"); err != nil {
		t.Fatal(err)
	}
	planned, _ := c.Result.(SentLine)
	want := []script.Write{{Method: "CLI", Path: first, Body: "config interface 3 shutdown"}}
	if !planned.Planned || !reflect.DeepEqual(planned.PlannedWrites(), want) || !strings.Contains(strings.Join(said, "\n"), "plan acme-rtr-01: config interface 3 shutdown") {
		t.Errorf("a write without --doit = %+v, said %q; want it listed as a CLI write and planned", planned, said)
	}
	if s := rt.Sessions().Find(first); strings.Contains(s.Buffer(), "config interface") {
		t.Errorf("the write was typed without --doit:\n%s", s.Buffer())
	}

	c = &Call{Ctx: context.Background(), DoIt: true, Args: map[string]string{"element": first, "text": "config interface 3 shutdown"}}
	if err := rt.Exec(c, "session.send"); err != nil {
		t.Fatal(err)
	}
	sent, _ := c.Result.(SentLine)
	if sent.Planned || !reflect.DeepEqual(sent.PlannedWrites(), want) || !strings.Contains(rt.Sessions().Find(first).Buffer(), "acme-rtr-01# config interface 3 shutdown") {
		t.Errorf("a write with --doit = %+v; want it typed and listed", sent)
	}

	read, _ := send(t, rt, map[string]string{"element": first, "text": "dump device status"})
	if read.Planned || len(read.PlannedWrites()) != 0 || !strings.Contains(read.Output, "FAKE0001") {
		t.Errorf("a read-only line without --doit = %+v; want it typed and no write listed", read)
	}
	if _, ok := any(read).(script.Planned); !ok {
		t.Error("a send's result doesn't list its writes to the workflow that ran it")
	}
	var second string
	for _, el := range rt.Elements() {
		if el.Name == "acme-rtr-02" {
			second = el.ID
		}
	}
	rt.Exec(&Call{Args: map[string]string{"element": second}}, "session.close")
	said = nil
	c = &Call{Ctx: context.Background(), Args: map[string]string{"element": second, "text": "config interface 3 shutdown"},
		Out: func(l string) { said = append(said, l) }}
	if err := rt.Exec(c, "session.send"); err != nil || !strings.Contains(strings.Join(said, "\n"), "plan acme-rtr-02: config interface 3 shutdown") {
		t.Errorf("a write without --doit on an element with no session = %v, said %q; want it listed by the element's name", err, said)
	}
	for line, want := range map[string]bool{"\x03": false, "dump device status": false, "inspect system arp": false, "debug bw-test src-interface 3": true, "config interface 3 shutdown": true} {
		if got := isWrite(line); got != want {
			t.Errorf("isWrite(%q) = %v, want %v", line, got, want)
		}
	}
}
