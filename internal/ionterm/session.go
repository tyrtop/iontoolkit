package ionterm

import (
	"context"
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

	keys   chan uv.KeyEvent
	text   chan string
	auto   *autologin
	notify func(string)
	cancel context.CancelFunc

	mu      sync.Mutex
	err     error
	closed  bool
	gotData bool
	line    []rune

	handshake time.Duration
}

var handshakeDeadline = 20 * time.Second

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
		handshake: handshakeDeadline,
		cancel:    cancel,
	}

	go s.pumpOutput(ctx)
	go s.pumpInput(ctx)
	go s.pumpKeys(ctx)
	go s.watchdog(ctx)

	return s, nil
}

func (s *Session) pumpOutput(ctx context.Context) {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			s.fail(err)
			return
		}
		if len(data) == 0 {
			continue
		}
		if _, err := s.emu.Write(data); err != nil {
			s.fail(err)
			return
		}
		s.mu.Lock()
		s.gotData = true
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

func (s *Session) watchdog(ctx context.Context) {
	t := time.NewTimer(s.handshake)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}

	s.mu.Lock()
	stalled := !s.gotData && s.err == nil && !s.closed
	s.mu.Unlock()

	if stalled {
		s.fail(fmt.Errorf("no response from device after %s", s.handshake))
	}
}

func (s *Session) markDirty() {
	if s.notify != nil {
		s.notify(s.ElementID)
	}
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
