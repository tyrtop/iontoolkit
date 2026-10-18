package ionterm

import (
	"context"
	"io"
	"testing"

	"github.com/coder/websocket"
)

// endedSession is a session the device has already hung up on, which is what a
// shell left with "exit" becomes.
func endedSession(t *testing.T, m *Manager, eid, name string) *Session {
	t.Helper()
	conn := newFakeConn(nil)
	conn.readErr = io.EOF
	s, err := m.Open(context.Background(), &fakeDialer{conn: conn}, eid, name, 80, 24, Credentials{})
	if err != nil {
		t.Fatalf("Open(%q) error = %v", eid, err)
	}
	eventually(t, "session "+eid+" to end", s.Ended)
	return s
}

func liveSession(t *testing.T, m *Manager, eid, name string) *Session {
	t.Helper()
	s, err := m.Open(context.Background(), &fakeDialer{conn: newFakeConn(nil)}, eid, name, 80, 24, Credentials{})
	if err != nil {
		t.Fatalf("Open(%q) error = %v", eid, err)
	}
	return s
}

func TestReapDropsOnlyTheSessionsThatEnded(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	live := liveSession(t, m, "keep", "keeper")
	endedSession(t, m, "gone", "leaver")

	if got := m.Reap(); got != 1 {
		t.Errorf("Reap() = %d, want 1", got)
	}
	if got := m.Count(); got != 1 {
		t.Fatalf("Count() = %d after reaping, want 1", got)
	}
	if got := m.All()[0]; got != live {
		t.Errorf("the surviving session is %q, want %q", got.ElementID, live.ElementID)
	}
	// Reaping again must not keep finding work to do, or the TUI would churn.
	if got := m.Reap(); got != 0 {
		t.Errorf("second Reap() = %d, want 0", got)
	}
}

func TestReapLeavesTheElementReopenable(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	endedSession(t, m, "e1", "ion-a")
	m.Reap()

	// alt+t has to have something to act on: the shell is gone, so the element
	// it was on is what gets dialled again.
	eid, name, ok := m.LastClosed()
	if !ok {
		t.Fatal("LastClosed() ok = false after a session ended, want the element remembered")
	}
	if eid != "e1" || name != "ion-a" {
		t.Errorf("LastClosed() = (%q, %q), want (%q, %q)", eid, name, "e1", "ion-a")
	}
}

func TestReapKeepsTheActiveTabInRange(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	liveSession(t, m, "a", "a")
	endedSession(t, m, "b", "b")
	// The ended session is the active one, being the most recently opened.
	if got := m.ActiveIndex(); got != 1 {
		t.Fatalf("ActiveIndex() = %d, want the newest tab active", got)
	}

	m.Reap()
	if got := m.ActiveIndex(); got != 0 {
		t.Errorf("ActiveIndex() = %d after reaping the active tab, want 0", got)
	}
	if m.Active() == nil {
		t.Error("Active() = nil with a tab still open, want the remaining session")
	}
}

func TestReapAlsoClearsDetachedSessionsThatDied(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	conn := newFakeConn(nil)
	s, err := m.Open(context.Background(), &fakeDialer{conn: conn}, "e1", "ion", 80, 24, Credentials{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, ok := m.Detach(); !ok {
		t.Fatal("Detach() ok = false, want the session detached")
	}

	// It ends while nobody is looking at it.
	conn.Close(websocket.StatusNormalClosure, "")
	eventually(t, "detached session to end", s.Ended)

	if got := m.Reap(); got != 1 {
		t.Errorf("Reap() = %d, want the dead detached session counted", got)
	}
	if got := m.DetachedCount(); got != 0 {
		t.Errorf("DetachedCount() = %d, want 0", got)
	}
}

func TestReattachSkipsSessionsThatCannotBeResumed(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	conn := newFakeConn(nil)
	dead, err := m.Open(context.Background(), &fakeDialer{conn: conn}, "dead", "dead", 80, 24, Credentials{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, ok := m.Detach(); !ok {
		t.Fatal("Detach() ok = false")
	}
	dead.fail(io.ErrUnexpectedEOF)

	// Handing back a failed session would restore a tab showing nothing but how
	// it broke; alt+t has to fall through to opening a fresh one instead.
	if s, ok := m.Reattach(); ok {
		t.Errorf("Reattach() returned session %q, want no usable session", s.ElementID)
	}
	if got := m.DetachedCount(); got != 0 {
		t.Errorf("DetachedCount() = %d, want the unusable session dropped", got)
	}
}

func TestReattachStillReturnsALiveSession(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	want := liveSession(t, m, "e1", "ion")
	if _, ok := m.Detach(); !ok {
		t.Fatal("Detach() ok = false")
	}

	got, ok := m.Reattach()
	if !ok {
		t.Fatal("Reattach() ok = false, want the detached session back")
	}
	if got != want {
		t.Errorf("Reattach() returned %q, want %q", got.ElementID, want.ElementID)
	}
	if n := m.Count(); n != 1 {
		t.Errorf("Count() = %d, want 1", n)
	}
}
