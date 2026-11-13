package ionterm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/cancelreader"
)

type tap struct {
	ch   chan []byte
	gone chan struct{}
	once sync.Once
	mu   sync.Mutex
	shut bool
}

func newTap() *tap {
	return &tap{ch: make(chan []byte, 64), gone: make(chan struct{})}
}

func (t *tap) send(ctx context.Context, data []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.shut {
		return true
	}
	select {
	case t.ch <- append([]byte(nil), data...):
	case <-t.gone:
	case <-ctx.Done():
		return false
	}
	return true
}

func (t *tap) stop() {
	t.once.Do(func() {
		close(t.gone)
		t.mu.Lock()
		t.shut = true
		close(t.ch)
		t.mu.Unlock()
	})
}

type AttachOptions struct {
	Catch func(uv.Key) bool
	Enter func()
}

func (s *Session) Attach(in io.Reader, out io.Writer, opt AttachOptions) (uv.Key, bool, error) {
	cr, err := cancelreader.NewReader(in)
	if err != nil {
		return uv.Key{}, false, fmt.Errorf("attach %s: %w", s.Name, err)
	}
	defer cr.Close()

	s.mu.Lock()
	s.line = s.line[:0]
	s.mu.Unlock()

	t, history := s.attachTap()
	if _, err := io.WriteString(out, history); err != nil {
		s.untap(t)
		return uv.Key{}, false, err
	}

	shown := make(chan struct{})
	go func() {
		defer close(shown)
		for data := range t.ch {
			if _, err := out.Write(data); err != nil {
				break
			}
		}
		cr.Cancel()
	}()
	defer func() {
		s.untap(t)
		cr.Cancel()
		<-shown
	}()

	var dec uv.EventDecoder
	buf := make([]byte, 4096)
	for {
		n, rerr := cr.Read(buf)
		if n > 0 {
			k, caught, err := s.typeRaw(&dec, buf[:n], opt)
			if err != nil || caught {
				return k, caught, err
			}
		}
		if rerr != nil {
			return uv.Key{}, false, s.Err()
		}
	}
}

func (s *Session) typeRaw(dec *uv.EventDecoder, b []byte, opt AttachOptions) (uv.Key, bool, error) {
	from := 0
	for i := 0; i < len(b); {
		n, ev := dec.Decode(b[i:])
		n = max(n, 1)
		if k, ok := ev.(uv.KeyPressEvent); ok {
			key := uv.Key(k)
			if opt.Catch != nil && opt.Catch(key) {
				return key, true, s.writeRaw(b[from:i])
			}
			if key.Code == uv.KeyEnter && opt.Enter != nil {
				if err := s.writeRaw(b[from:i]); err != nil {
					return uv.Key{}, false, err
				}
				from = i
				opt.Enter()
			}
		}
		i += n
	}
	return uv.Key{}, false, s.writeRaw(b[from:])
}

func (s *Session) writeRaw(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	return s.WriteRaw(p)
}

func (s *Session) attachTap() (*tap, string) {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()

	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J\x1b[3J")
	sb := s.emu.Scrollback()
	pos := s.emu.CursorPosition()
	lines := screenLines(s.emu.Render(), pos.Y)
	all := make([]string, 0, sb.Len()+len(lines))
	for i := 0; i < sb.Len(); i++ {
		all = append(all, sb.Line(i).Render())
	}
	all = append(all, lines...)
	for i, ln := range all {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(strings.TrimRight(ln, " "))
		b.WriteString("\x1b[m")
	}
	if up := len(all) - 1 - (sb.Len() + pos.Y); up > 0 && sb.Len()+pos.Y >= 0 {
		fmt.Fprintf(&b, "\x1b[%dA", up)
	}
	b.WriteString("\r")
	if pos.X > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", pos.X)
	}

	s.mu.Lock()
	t := s.tapLocked()
	s.mu.Unlock()
	return t, b.String()
}
