package ionterm

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/coder/websocket"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;:?]*[a-zA-Z]|\x1b[()][A-Z0-9]`)

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

func fixtureFrames(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open("testdata/ion-session.raw")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var frames [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s, err := strconv.Unquote(line)
		if err != nil {
			t.Fatalf("bad fixture line %q: %v", line, err)
		}
		frames = append(frames, []byte(s))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return frames
}

type fakeConn struct {
	frames  [][]byte
	i       int
	readErr error

	mu      sync.Mutex
	written []byte

	feed chan []byte
	done chan struct{}
	once sync.Once
}

func newFakeConn(frames [][]byte) *fakeConn {
	return &fakeConn{
		frames: frames,
		feed:   make(chan []byte, 8),
		done:   make(chan struct{}),
	}
}

func (c *fakeConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	if c.readErr != nil {
		return 0, nil, c.readErr
	}
	if c.i < len(c.frames) {
		f := c.frames[c.i]
		c.i++
		return websocket.MessageText, f, nil
	}
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-c.done:
		return 0, nil, io.EOF
	case f := <-c.feed:
		return websocket.MessageText, f, nil
	}
}

func (c *fakeConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written = append(c.written, p...)
	return nil
}

func (c *fakeConn) Close(code websocket.StatusCode, reason string) error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *fakeConn) sent() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.written)
}

type fakeDialer struct {
	conn       *fakeConn
	cols, rows int
	eid        string
	err        error
}

func (d *fakeDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (Conn, error) {
	d.eid, d.cols, d.rows = eid, cols, rows
	if d.err != nil {
		return nil, d.err
	}
	return d.conn, nil
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSessionRendersDeviceOutput(t *testing.T) {
	d := &fakeDialer{conn: newFakeConn(fixtureFrames(t))}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "S5032-RTR-01", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "device output to reach the screen", func() bool {
		return strings.Contains(plain(s.Render()), "DPDK Controlled")
	})

	screen := plain(s.Render())
	if n := strings.Count(screen, "S5032-RTR-01#"); n > 2 {
		t.Errorf("repaints did not collapse: %d prompts", n)
	}
	if strings.Contains(screen, "\x1b") {
		t.Error("escape byte survived into the rendered screen")
	}
	if s.Err() != nil {
		t.Errorf("Err() = %v", s.Err())
	}
}

func TestDialerReceivesRequestedGeometry(t *testing.T) {
	d := &fakeDialer{conn: newFakeConn(nil)}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	if _, err := m.Open(context.Background(), d, "456", "x", 112, 37, Credentials{}); err != nil {
		t.Fatal(err)
	}
	if d.eid != "456" || d.cols != 112 || d.rows != 37 {
		t.Errorf("dialed eid=%q cols=%d rows=%d", d.eid, d.cols, d.rows)
	}
}

func TestSessionForwardsKeysToTheWire(t *testing.T) {
	conn := newFakeConn(nil)
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range "dump" {
		s.SendKey(uv.KeyPressEvent{Code: r, Text: string(r)})
	}
	s.SendKey(uv.KeyPressEvent{Code: '\r'})

	eventually(t, "keystrokes to reach the connection", func() bool {
		return strings.Contains(conn.sent(), "dump")
	})
	if got := conn.sent(); !strings.Contains(got, "\r") {
		t.Errorf("sent %q, want a carriage return", got)
	}
}

func TestSessionSurfacesReadError(t *testing.T) {
	conn := newFakeConn(nil)
	conn.readErr = errors.New("websocket: close 1006")
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "read error to surface", func() bool { return s.Err() != nil })
	if got := s.Err().Error(); !strings.Contains(got, "1006") {
		t.Errorf("Err() = %q", got)
	}
}

func TestOpenPropagatesDialError(t *testing.T) {
	d := &fakeDialer{err: errors.New("status 403")}
	m := NewManager()

	if _, err := m.Open(context.Background(), d, "123", "x", 69, 24, Credentials{}); err == nil {
		t.Fatal("want a dial error")
	}
	if m.Count() != 0 {
		t.Errorf("failed dial left %d sessions", m.Count())
	}
}

func TestManagerDirtySignalCoalesces(t *testing.T) {
	d := &fakeDialer{conn: newFakeConn(fixtureFrames(t))}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	if _, err := m.Open(context.Background(), d, "123", "x", 69, 24, Credentials{}); err != nil {
		t.Fatal(err)
	}

	select {
	case eid := <-m.Dirty():
		if eid != "123" {
			t.Errorf("dirty eid = %q, want 123", eid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no dirty signal")
	}

	if n := len(m.Dirty()); n > 1 {
		t.Errorf("dirty channel buffered %d, want it to coalesce to at most 1", n)
	}
}

func TestManagerTabNavigation(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	for _, eid := range []string{"a", "b", "c"} {
		d := &fakeDialer{conn: newFakeConn(nil)}
		if _, err := m.Open(context.Background(), d, eid, eid, 69, 24, Credentials{}); err != nil {
			t.Fatal(err)
		}
	}

	if m.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", m.Count())
	}
	if got := m.Active().ElementID; got != "c" {
		t.Errorf("newest session should be active, got %q", got)
	}

	m.Next()
	if got := m.Active().ElementID; got != "a" {
		t.Errorf("Next from last should wrap to first, got %q", got)
	}
	m.Prev()
	if got := m.Active().ElementID; got != "c" {
		t.Errorf("Prev from first should wrap to last, got %q", got)
	}

	m.Select(1)
	if got := m.Active().ElementID; got != "b" {
		t.Errorf("Select(1) = %q, want b", got)
	}

	m.Select(99)
	if got := m.Active().ElementID; got != "b" {
		t.Errorf("out of range Select changed the active tab to %q", got)
	}

	m.CloseActive()
	if m.Count() != 2 {
		t.Fatalf("Count() = %d after close, want 2", m.Count())
	}
	for _, s := range m.All() {
		if s.ElementID == "b" {
			t.Error("closed session still listed")
		}
	}
	if m.Active() == nil {
		t.Error("Active() is nil after closing a middle tab")
	}
}

func TestOpenNeverReusesAVisibleSession(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	d1 := &fakeDialer{conn: newFakeConn(nil)}
	s1, err := m.Open(context.Background(), d1, "a", "a", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	d2 := &fakeDialer{conn: newFakeConn(nil)}
	if _, err := m.Open(context.Background(), d2, "b", "b", 69, 24, Credentials{}); err != nil {
		t.Fatal(err)
	}

	d3 := &fakeDialer{conn: newFakeConn(nil)}
	s3, err := m.Open(context.Background(), d3, "a", "a", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	if m.Count() != 3 {
		t.Errorf("Count() = %d, want 3 — each selection opens its own session", m.Count())
	}
	if s3 == s1 {
		t.Error("Open reused an existing session")
	}
	if d3.eid != "a" {
		t.Error("Open did not dial")
	}
	if got := m.Active().ElementID; got != "a" {
		t.Errorf("newest session should be active, got %q", got)
	}
}

func TestCloseAllIsIdempotent(t *testing.T) {
	m := NewManager()
	d := &fakeDialer{conn: newFakeConn(nil)}
	s, err := m.Open(context.Background(), d, "a", "a", 69, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	m.CloseAll()
	m.CloseAll()
	s.Close()

	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}
	if !s.Closed() {
		t.Error("session not marked closed")
	}
}

func contextTODO() context.Context { return context.Background() }

func plainWidth(s string) int { return len([]rune(plain(s))) }

func TestWatchdogFailsASilentSession(t *testing.T) {
	old := handshakeDeadline
	handshakeDeadline = 60 * time.Millisecond
	t.Cleanup(func() { handshakeDeadline = old })

	d := &fakeDialer{conn: newFakeConn(nil)}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "hung", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "watchdog to fail the silent session", func() bool { return s.Err() != nil })
	if got := s.Err().Error(); !strings.Contains(got, "no response") {
		t.Errorf("Err() = %q", got)
	}
}

func TestWatchdogStaysQuietOnceDataArrives(t *testing.T) {
	old := handshakeDeadline
	handshakeDeadline = 60 * time.Millisecond
	t.Cleanup(func() { handshakeDeadline = old })

	d := &fakeDialer{conn: newFakeConn([][]byte{[]byte("ion1# ")})}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "live", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "first data", func() bool {
		return strings.Contains(plain(s.Render()), "ion1#")
	})

	time.Sleep(150 * time.Millisecond)
	if err := s.Err(); err != nil {
		t.Errorf("watchdog fired on a live but idle session: %v", err)
	}
}

func TestSendTextReachesTheWire(t *testing.T) {
	conn := newFakeConn(nil)
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	s.SendText("dump interface status 4")

	eventually(t, "pasted text to reach the connection", func() bool {
		return strings.Contains(conn.sent(), "dump interface status 4")
	})
}

func TestSendTextDoesNotBlockWhenNobodyDrains(t *testing.T) {
	s := &Session{text: make(chan string, 1)}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			s.SendText("x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendText blocked with a full buffer and no reader")
	}
}

func TestLineBufferTracksTyping(t *testing.T) {
	conn := newFakeConn(nil)
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	if !s.LineEmpty() {
		t.Fatal("fresh session should have an empty line")
	}

	for _, r := range "dump" {
		s.Type(uv.KeyPressEvent{Code: r, Text: string(r)})
	}
	if s.LineEmpty() {
		t.Error("line should not be empty after typing")
	}
	if got := s.Line(); got != "dump" {
		t.Errorf("Line() = %q, want dump", got)
	}

	s.Type(uv.KeyPressEvent{Code: 0x7f})
	if got := s.Line(); got != "dum" {
		t.Errorf("after backspace Line() = %q, want dum", got)
	}

	s.Type(uv.KeyPressEvent{Code: '\r'})
	if !s.LineEmpty() {
		t.Errorf("enter should clear the line, got %q", s.Line())
	}
}

func TestLineBufferIgnoresNonPrintingKeys(t *testing.T) {
	s := &Session{keys: make(chan uv.KeyEvent, 8)}

	s.note('\x1b', "")
	s.note(0, "")
	if !s.LineEmpty() {
		t.Errorf("control keys entered the line buffer: %q", s.Line())
	}
}

func TestLastClosedRemembersTheTab(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	if _, _, ok := m.LastClosed(); ok {
		t.Error("nothing closed yet, want ok=false")
	}

	for _, eid := range []string{"a", "b"} {
		d := &fakeDialer{conn: newFakeConn(nil)}
		if _, err := m.Open(context.Background(), d, eid, "name-"+eid, 200, 50, Credentials{}); err != nil {
			t.Fatal(err)
		}
	}

	m.CloseActive()

	eid, name, ok := m.LastClosed()
	if !ok {
		t.Fatal("want a remembered tab")
	}
	if eid != "b" || name != "name-b" {
		t.Errorf("LastClosed() = %q/%q, want b/name-b", eid, name)
	}
	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1", m.Count())
	}
}

func TestNilManagerLastClosed(t *testing.T) {
	var m *Manager
	if _, _, ok := m.LastClosed(); ok {
		t.Error("nil manager should report nothing closed")
	}
}

func TestDetachKeepsTheSessionRunning(t *testing.T) {
	conn := newFakeConn(fixtureFrames(t))
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	detached, ok := m.Detach()
	if !ok || detached != s {
		t.Fatal("Detach did not return the active session")
	}
	if m.Count() != 0 || m.DetachedCount() != 1 {
		t.Fatalf("visible=%d detached=%d, want 0/1", m.Count(), m.DetachedCount())
	}
	if s.Closed() {
		t.Fatal("detach must not close the session")
	}

	eventually(t, "a detached session to keep consuming output", func() bool {
		return strings.Contains(plain(s.Render()), "DPDK Controlled")
	})

	back, ok := m.Reattach()
	if !ok {
		t.Fatal("Reattach found nothing")
	}
	if back != s {
		t.Error("Reattach returned a different session")
	}
	if m.Count() != 1 || m.DetachedCount() != 0 {
		t.Errorf("visible=%d detached=%d, want 1/0", m.Count(), m.DetachedCount())
	}
	if m.Active() != s {
		t.Error("reattached session is not active")
	}
}

func TestReattachIsLIFO(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	for _, eid := range []string{"a", "b", "c"} {
		d := &fakeDialer{conn: newFakeConn(nil)}
		if _, err := m.Open(context.Background(), d, eid, eid, 200, 50, Credentials{}); err != nil {
			t.Fatal(err)
		}
	}

	m.Select(0)
	m.Detach()
	m.Select(0)
	m.Detach()

	if m.Count() != 1 || m.DetachedCount() != 2 {
		t.Fatalf("visible=%d detached=%d", m.Count(), m.DetachedCount())
	}

	s, _ := m.Reattach()
	if s.ElementID != "b" {
		t.Errorf("reattached %q, want b (most recently detached)", s.ElementID)
	}
	s, _ = m.Reattach()
	if s.ElementID != "a" {
		t.Errorf("reattached %q, want a", s.ElementID)
	}
	if _, ok := m.Reattach(); ok {
		t.Error("Reattach succeeded with nothing detached")
	}
}

func TestOpenAlwaysDialsFreshEvenIfDetached(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	d1 := &fakeDialer{conn: newFakeConn(nil)}
	first, err := m.Open(context.Background(), d1, "a", "a", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	m.Detach()

	d2 := &fakeDialer{conn: newFakeConn(nil)}
	again, err := m.Open(context.Background(), d2, "a", "a", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	if again == first {
		t.Error("Open reused the detached session; it must dial a new one")
	}
	if d2.eid != "a" {
		t.Error("the dialer was not called")
	}
	if m.DetachedCount() != 1 {
		t.Errorf("DetachedCount() = %d — Open must leave detached sessions alone", m.DetachedCount())
	}
	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1 visible", m.Count())
	}
	if first.Closed() {
		t.Error("the detached session should still be alive")
	}
}

func TestOpenTwiceForOneElementGivesTwoTabs(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.CloseAll)

	for i := 0; i < 2; i++ {
		d := &fakeDialer{conn: newFakeConn(nil)}
		if _, err := m.Open(context.Background(), d, "a", "a", 200, 50, Credentials{}); err != nil {
			t.Fatal(err)
		}
	}
	if m.Count() != 2 {
		t.Errorf("Count() = %d, want 2 — the elements list always starts a new session", m.Count())
	}
}

func TestCloseAllClosesDetachedSessionsToo(t *testing.T) {
	m := NewManager()

	d := &fakeDialer{conn: newFakeConn(nil)}
	s, err := m.Open(context.Background(), d, "a", "a", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	m.Detach()

	m.CloseAll()
	if !s.Closed() {
		t.Error("CloseAll left a detached session running")
	}
	if m.DetachedCount() != 0 {
		t.Errorf("DetachedCount() = %d after CloseAll", m.DetachedCount())
	}
}

func TestLoginSendsStoredCredentialsOnDemand(t *testing.T) {
	conn := newFakeConn([][]byte{[]byte("1234 login: ")})
	d := &fakeDialer{conn: conn}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "the login prompt", func() bool {
		return strings.Contains(plain(s.Render()), "login:")
	})
	if conn.sent() != "" {
		t.Fatalf("something was sent without credentials: %q", conn.sent())
	}

	if !s.Login(Credentials{User: "cisops", Pass: "s3cret"}) {
		t.Fatal("Login returned false with both credentials")
	}
	eventually(t, "the username on the wire", func() bool {
		return strings.Contains(conn.sent(), "cisops\r")
	})

	conn.feed <- []byte("\r\nPassword: ")
	eventually(t, "the password after the prompt", func() bool {
		return strings.Contains(conn.sent(), "s3cret\r")
	})
}

func TestLoginRefusesWithoutCredentials(t *testing.T) {
	d := &fakeDialer{conn: newFakeConn(nil)}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Login(Credentials{User: "u"}) {
		t.Error("Login accepted a missing password")
	}
	if s.Login(Credentials{}) {
		t.Error("Login accepted empty credentials")
	}
}
