package ionterm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/coder/websocket"
)

type Conn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	// Ping must be answered by the peer. An idle ION shell sends nothing for
	// hours, so silence alone cannot tell a hang from a quiet prompt.
	Ping(ctx context.Context) error
	Close(code websocket.StatusCode, reason string) error
}

type Dialer interface {
	DialToolkit(ctx context.Context, eid string, cols, rows int) (Conn, error)
}

type Session struct {
	ElementID string
	Name      string
	Cols      int
	Rows      int

	emu  *vt.SafeEmulator
	conn Conn

	// SafeEmulator locks each of its own methods, but Scrollback() hands back
	// the buffer itself and the lock is gone by the time its lines are read.
	// This covers a write against the reads that walk them, which is all that
	// is needed: ionterm owns every emulator call site.
	emuMu sync.Mutex

	keys   chan uv.KeyEvent
	text   chan string
	auto   *autologin
	notify func(string)
	cancel context.CancelFunc

	mu       sync.Mutex
	err      error
	closed   bool
	ended    bool
	hung     bool
	gotData  bool
	line     []rune
	lastSeen time.Time

	// Snapshotted at Open so a session's cadence cannot change under its own
	// liveness loop.
	pingEvery time.Duration
	pongWait  time.Duration
	handshake time.Duration
}

// A dial that answers nothing at all is still bounded, but once the socket is
// up liveness comes from pongs rather than the clock: a slow device that keeps
// answering is not hung, and one that stops is, whatever the elapsed time.
var (
	pingEvery = 15 * time.Second
	pongWait  = 10 * time.Second
	// A socket that pongs but never sends a prompt still has to be given up
	// on: autologin has nothing to answer, and there is no traffic to measure.
	handshakeDeadline = 20 * time.Second
)

// SetLivenessForTest shortens the ping cadence so another package's tests can
// reach a hang without waiting out the real one, and restores it afterwards.
// Sessions snapshot these at Open, so it must be called before one is opened.
func SetLivenessForTest(ping, pong time.Duration) (restore func()) {
	oldPing, oldPong := pingEvery, pongWait
	pingEvery, pongWait = ping, pong
	return func() { pingEvery, pongWait = oldPing, oldPong }
}

func Open(ctx context.Context, d Dialer, eid, name string, cols, rows int, creds Credentials, notify func(string)) (*Session, error) {
	conn, err := d.DialToolkit(ctx, eid, cols, rows)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	s := &Session{
		ElementID: eid,
		Name:      name,
		Cols:      cols,
		Rows:      rows,
		emu:       vt.NewSafeEmulator(cols, rows),
		conn:      conn,
		keys:      make(chan uv.KeyEvent, 64),
		text:      make(chan string, 8),
		auto:      newAutologin(creds),
		notify:    notify,
		lastSeen:  time.Now(),
		cancel:    cancel,
		pingEvery: pingEvery,
		pongWait:  pongWait,
		handshake: handshakeDeadline,
	}

	go s.pumpOutput(ctx)
	go s.pumpInput(ctx)
	go s.pumpKeys(ctx)
	go s.liveness(ctx)

	return s, nil
}

func (s *Session) pumpOutput(ctx context.Context) {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			if cleanHangUp(err) {
				s.end()
			} else {
				s.fail(err)
			}
			return
		}
		if len(data) == 0 {
			continue
		}
		s.emuMu.Lock()
		_, err = s.emu.Write(data)
		s.emuMu.Unlock()
		if err != nil {
			s.fail(err)
			return
		}
		s.mu.Lock()
		s.gotData = true
		s.lastSeen = time.Now()
		s.mu.Unlock()

		s.mu.Lock()
		auto := s.auto
		s.mu.Unlock()
		if send, ok := auto.feed(data); ok {
			s.SendText(send)
		}
		s.markDirty()
	}
}

func (s *Session) pumpInput(ctx context.Context) {
	buf := make([]byte, 4096)
	for {
		n, err := s.emu.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if err := s.conn.Write(ctx, websocket.MessageText, buf[:n]); err != nil {
			s.fail(err)
			return
		}
	}
}

func (s *Session) pumpKeys(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case k, ok := <-s.keys:
			if !ok {
				return
			}
			s.emu.SendKey(k)
		case t, ok := <-s.text:
			if !ok {
				return
			}
			s.emu.SendText(t)
		}
	}
}

// liveness replaces a flat deadline. Traffic is proof enough; only a quiet
// connection is pinged, and only an unanswered ping is a hang.
func (s *Session) liveness(ctx context.Context) {
	// The handshake is a one-shot deadline because there is no traffic yet to
	// measure; everything after it is paced by pongs.
	hs := time.NewTimer(s.handshake)
	defer hs.Stop()
	t := time.NewTicker(s.pingEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-hs.C:
			s.mu.Lock()
			mute := !s.gotData && s.err == nil && !s.closed && !s.ended
			s.mu.Unlock()
			if mute {
				s.fail(fmt.Errorf("no response from device after %s", s.handshake))
				return
			}

		case <-t.C:
			s.mu.Lock()
			quiet := time.Since(s.lastSeen)
			done := s.closed || s.ended || s.err != nil || s.hung
			s.mu.Unlock()
			if done {
				return
			}
			if quiet < s.pingEvery {
				continue
			}

			pingCtx, cancel := context.WithTimeout(ctx, s.pongWait)
			err := s.conn.Ping(pingCtx)
			cancel()
			if err == nil {
				s.mu.Lock()
				s.lastSeen = time.Now()
				s.mu.Unlock()
				continue
			}
			if ctx.Err() != nil {
				return
			}
			s.markHung()
			return
		}
	}
}

// markHung records a connection that stopped answering. It is kept apart from
// err because it is recoverable: the user can dial the element again, which
// err offers no route to.
func (s *Session) markHung() {
	s.mu.Lock()
	if s.err == nil && !s.closed && !s.ended {
		s.hung = true
	}
	s.mu.Unlock()
	s.markDirty()
}

// Answered reports whether the device has sent anything at all. The socket
// coming up proves nothing about the appliance: the SASE side accepts a
// toolkit websocket for an element that is not there, and such a session sits
// mute until the handshake deadline fails it. The first byte is the only
// evidence there is an ION on the other end.
func (s *Session) Answered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotData
}

// Hung is a session whose connection stopped answering pings.
func (s *Session) Hung() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hung
}

func (s *Session) markDirty() {
	if s.notify != nil {
		s.notify(s.ElementID)
	}
}

// cleanHangUp is what typing "exit" looks like from here: the device closes the
// socket. A torn connection is a failure the user needs to see.
func cleanHangUp(err error) bool {
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	}
	return errors.Is(err, io.EOF)
}

// end records no error, so the tab closes instead of reporting a failure the
// user caused on purpose.
func (s *Session) end() {
	s.mu.Lock()
	if s.err == nil && !s.closed {
		s.ended = true
	}
	s.mu.Unlock()
	s.markDirty()
}

func (s *Session) Ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	if s.err == nil && !s.closed {
		s.err = err
	}
	s.mu.Unlock()
	s.markDirty()
}

func (s *Session) SendKey(k uv.KeyEvent) {
	select {
	case s.keys <- k:
	default:
	}
}

func (s *Session) Type(k uv.KeyPressEvent) {
	s.note(k.Code, k.Text)
	s.SendKey(k)
}

func (s *Session) note(code rune, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case code == '\r' || code == '\n':
		s.line = s.line[:0]
	case code == 0x7f || code == 0x08:
		if len(s.line) > 0 {
			s.line = s.line[:len(s.line)-1]
		}
	case text != "":
		s.line = append(s.line, []rune(text)...)
	}
}

func (s *Session) Login(c Credentials) bool {
	if c.User == "" || c.Pass == "" {
		return false
	}
	s.mu.Lock()
	s.auto = &autologin{creds: c, step: 1}
	s.mu.Unlock()

	s.SendText(c.User + "\r")
	return true
}

func (s *Session) LineEmpty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.line) == 0
}

func (s *Session) Line() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.line)
}

func (s *Session) Insert(text string) {
	s.note(0, text)
	s.SendText(text)
}

func (s *Session) SendText(text string) {
	select {
	case s.text <- text:
	default:
	}
}

func (s *Session) Render() string { return s.emu.Render() }

func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	_ = s.conn.Close(websocket.StatusNormalClosure, "")

	// Unblocks pumpInput without calling Emulator.Close, whose unsynchronized
	// closed flag races its own Read in x/vt v0.1.0.
	if pw, ok := s.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
}

func (s *Session) Cursor() (int, int) {
	p := s.emu.CursorPosition()
	return p.X, p.Y
}

func (s *Session) Size() (int, int) { return s.Cols, s.Rows }
