package ionterm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/coder/websocket"
)

type Conn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
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

	emuMu sync.Mutex

	keys   chan uv.KeyEvent
	text   chan string
	auto   *autologin
	notify func(string)
	ctx    context.Context
	cancel context.CancelFunc
	taps   []chan []byte
	quiet  bool

	mu       sync.Mutex
	err      error
	closed   bool
	ended    bool
	hung     bool
	gotData  bool
	line     []rune
	lastSeen time.Time

	pingEvery time.Duration
	pongWait  time.Duration
	handshake time.Duration
}

var (
	pingEvery         = 15 * time.Second
	pongWait          = 10 * time.Second
	handshakeDeadline = 20 * time.Second
)

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

	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))

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
		ctx:       ctx,
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
	defer s.stopTaps()
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
		taps := s.taps
		s.mu.Unlock()
		for _, t := range taps {
			select {
			case t <- append([]byte(nil), data...):
			case <-ctx.Done():
				return
			}
		}

		s.mu.Lock()
		send, ok := s.auto.feed(data)
		s.mu.Unlock()
		if ok {
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

func (s *Session) liveness(ctx context.Context) {
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

func (s *Session) markHung() {
	s.mu.Lock()
	if s.err == nil && !s.closed && !s.ended {
		s.hung = true
	}
	s.mu.Unlock()
	s.stopTaps()
	s.markDirty()
}

func (s *Session) stopTaps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.taps {
		close(t)
	}
	s.taps, s.quiet = nil, true
}

// SASE accepts a websocket for a missing element, then stays mute
func (s *Session) Answered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotData
}

func (s *Session) LoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotData && s.auto.done()
}

func (s *Session) LoggingIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.auto.done()
}

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

func cleanHangUp(err error) bool {
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	}
	return errors.Is(err, io.EOF)
}

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
	s.stopTaps()
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

// the device echoes recalled or completed commands we never typed
func (s *Session) Line() string {
	if line := s.screenLine(); line != "" {
		return line
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.line))
}

func (s *Session) screenLine() string {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()

	pos := s.emu.CursorPosition()
	if pos.Y < 0 || pos.X <= 0 {
		return ""
	}
	rows := strings.Split(strings.TrimRight(s.emu.Render(), "\n"), "\n")
	if pos.Y >= len(rows) {
		return ""
	}
	return strings.TrimSpace(afterPrompt(ansi.Strip(ansi.Truncate(rows[pos.Y], pos.X, "")), s.Name))
}

func afterPrompt(row, name string) string {
	if name != "" {
		if _, rest, ok := strings.Cut(row, name+"#"); ok {
			return rest
		}
	}
	if i := strings.LastIndexAny(row, "#>$"); i >= 0 {
		return row[i+1:]
	}
	return row
}

func (s *Session) Insert(text string) {
	s.note(0, text)
	s.SendText(text)
}

func (s *Session) Tap() <-chan []byte {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.quiet {
		close(ch)
		return ch
	}
	s.taps = append(s.taps, ch)
	return ch
}

func (s *Session) WriteRaw(p []byte) error {
	return s.conn.Write(s.ctx, websocket.MessageText, p)
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

	// Emulator.Close's closed flag races its own Read in x/vt v0.1.0
	if pw, ok := s.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
}

func (s *Session) Cursor() (int, int) {
	p := s.emu.CursorPosition()
	return p.X, p.Y
}

func (s *Session) Size() (int, int) { return s.Cols, s.Rows }
