package ionterm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func feedLines(t *testing.T, c *fakeConn, from, to int) {
	t.Helper()
	for i := from; i <= to; i++ {
		c.feed <- []byte(fmt.Sprintf("L%03d\r\n", i))
	}
}

// A session whose output has outrun a six-row screen, which is the only state
// where scrolling back shows anything.
func scrolledSession(t *testing.T, lines int) (*Session, *fakeConn) {
	t.Helper()
	d := &fakeDialer{conn: newFakeConn(nil)}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "1", "s0001", 40, 6, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	feedLines(t, d.conn, 1, lines)
	eventually(t, "the last line to reach the screen", func() bool {
		return strings.Contains(plain(s.Render()), fmt.Sprintf("L%03d", lines))
	})
	eventually(t, "lines to scroll off the screen", func() bool { return s.Scrollback() > 0 })
	return s, d.conn
}

func TestScrolledViewReachesLinesThatLeftTheScreen(t *testing.T) {
	s, _ := scrolledSession(t, 30)

	live := s.ScrolledView(40, 6, 0)
	if live.Offset != 0 {
		t.Errorf("Offset = %d at the live bottom, want 0", live.Offset)
	}
	if live.Total <= 6 {
		t.Errorf("Total = %d, want the scrollback counted with the screen", live.Total)
	}
	if !strings.Contains(plain(live.Content), "L030") {
		t.Errorf("the live pane does not show the newest line:\n%s", plain(live.Content))
	}
	if strings.Contains(plain(live.Content), "L001") {
		t.Fatalf("the first line is still on screen, so nothing scrolled off:\n%s", plain(live.Content))
	}

	// Far enough back to reach the first line ever written.
	back := s.ScrolledView(40, 6, live.Total)
	if want := live.Total - 6; back.Offset != want {
		t.Errorf("Offset = %d, want it clamped to %d", back.Offset, want)
	}
	if !strings.Contains(plain(back.Content), "L001") {
		t.Errorf("scrolled to the top and the first line is not there:\n%s", plain(back.Content))
	}
	if strings.Contains(plain(back.Content), "L030") {
		t.Errorf("the live bottom is still showing at the top of the scrollback:\n%s", plain(back.Content))
	}
}

func TestScrolledViewKeepsTheFrameHeight(t *testing.T) {
	s, _ := scrolledSession(t, 30)

	for _, off := range []int{0, 1, 7, 500} {
		v := s.ScrolledView(40, 6, off)
		if got := len(strings.Split(v.Content, "\n")); got != 6 {
			t.Errorf("off %d rendered %d rows, want 6 — a shorter pane moves the frame", off, got)
		}
	}
}

func TestScrolledViewMovesOneLinePerOffset(t *testing.T) {
	s, _ := scrolledSession(t, 30)

	first := func(v View) string {
		return strings.TrimSpace(plain(strings.SplitN(v.Content, "\n", 2)[0]))
	}
	a := first(s.ScrolledView(40, 6, 4))
	b := first(s.ScrolledView(40, 6, 5))

	var na, nb int
	if _, err := fmt.Sscanf(a, "L%03d", &na); err != nil {
		t.Fatalf("top line %q is not one of the fed lines: %v", a, err)
	}
	if _, err := fmt.Sscanf(b, "L%03d", &nb); err != nil {
		t.Fatalf("top line %q is not one of the fed lines: %v", b, err)
	}
	if na-nb != 1 {
		t.Errorf("one more line of offset moved the window from %q to %q, want one line", a, b)
	}
}

func TestScrolledViewDoesNotRaceDeviceOutput(t *testing.T) {
	s, conn := scrolledSession(t, 10)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 11; i <= 400; i++ {
			conn.feed <- []byte(fmt.Sprintf("L%03d\r\n", i))
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out feeding the session")
		}
		if v := s.ScrolledView(40, 6, 3); v.Total == 0 {
			t.Fatal("Total = 0 while the device was writing")
		}
	}
}

// Buffer is what a copy takes, so it has to reach past the screen: the whole
// point of copying a session is the output that has already scrolled away.
func TestBufferHoldsTheScrollbackAndTheScreen(t *testing.T) {
	s, _ := scrolledSession(t, 30)

	got := s.Buffer()
	for _, want := range []string{"L001", "L015", "L030"} {
		if !strings.Contains(got, want) {
			t.Errorf("Buffer() is missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("Buffer() carries escape codes, which a clipboard should not:\n%q", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) < 30 {
		t.Errorf("Buffer() has %d lines, want every line written", len(lines))
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("Buffer() ends with the blank prompt rows the screen pads with")
	}
}
