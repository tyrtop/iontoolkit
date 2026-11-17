package ionterm

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/coder/websocket"
)

type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *screen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

type attached struct {
	key    uv.Key
	caught bool
	err    error
}

func attachTo(t *testing.T, s *Session, opt AttachOptions) (*os.File, *screen, <-chan attached) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	out := &screen{}
	done := make(chan attached, 1)
	go func() {
		k, caught, err := s.Attach(r, out, opt)
		done <- attached{k, caught, err}
	}()
	return w, out, done
}

func ended(t *testing.T, done <-chan attached) attached {
	t.Helper()
	select {
	case a := <-done:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("Attach never returned")
	}
	return attached{}
}

func openFake(t *testing.T, frames ...string) (*Session, *fakeConn) {
	t.Helper()
	var fs [][]byte
	for _, f := range frames {
		fs = append(fs, []byte(f))
	}
	conn := newFakeConn(fs)
	m := NewManager()
	t.Cleanup(m.CloseAll)
	s, err := m.Open(context.Background(), &fakeDialer{conn: conn}, "123", "S7020-RTR-01", 80, 24, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	return s, conn
}

func TestAttachShowsWhatTheDeviceSaidBeforeThenWhatItSaysNow(t *testing.T) {
	s, conn := openFake(t, "uptime\r\n 12 days\r\nS7020-RTR-01# ")
	eventually(t, "the prompt on the emulator", func() bool { return strings.Contains(s.Render(), "S7020-RTR-01#") })

	in, out, done := attachTo(t, s, AttachOptions{Catch: func(k uv.Key) bool { return k.String() == "ctrl+]" }})
	eventually(t, "the history on the terminal", func() bool { return strings.Contains(out.String(), "S7020-RTR-01#") })

	got := out.String()
	if !strings.HasPrefix(got, "\x1b[H\x1b[2J\x1b[3J") {
		t.Errorf("history starts %q, want the screen and its scrollback cleared first", got[:min(len(got), 16)])
	}
	if !strings.Contains(got, "12 days") {
		t.Errorf("history %q is missing the uptime the device printed before the attach", got)
	}

	conn.feed <- []byte("dump")
	eventually(t, "live output on the terminal", func() bool { return strings.HasSuffix(out.String(), "dump") })

	if _, err := in.Write([]byte{0x1d}); err != nil {
		t.Fatal(err)
	}
	a := ended(t, done)
	if !a.caught || a.key.String() != "ctrl+]" {
		t.Errorf("Attach returned %q caught=%v, want ctrl+] handed back", a.key.String(), a.caught)
	}
	if got := conn.sent(); strings.Contains(got, "\x1d") {
		t.Errorf("device got %q, want ctrl+] kept from it", got)
	}
}

func TestAttachSendsKeysAsTypedAndHandsBackTheOnesIontkKeeps(t *testing.T) {
	s, conn := openFake(t, "S7020-RTR-01# ")
	eventually(t, "the prompt on the emulator", func() bool { return strings.Contains(s.Render(), "#") })

	var atEnter []string
	in, out, done := attachTo(t, s, AttachOptions{
		Catch: func(k uv.Key) bool { return k.String() == "alt+w" },
		Enter: func() { atEnter = append(atEnter, conn.sent()) },
	})
	eventually(t, "the history on the terminal", func() bool { return strings.Contains(out.String(), "#") })

	if _, err := in.Write([]byte("dump\r\x1b[A\x1bwlost")); err != nil {
		t.Fatal(err)
	}
	a := ended(t, done)
	if !a.caught || a.key.String() != "alt+w" {
		t.Errorf("Attach returned %q caught=%v, want alt+w handed back", a.key.String(), a.caught)
	}
	if got := conn.sent(); got != "dump\r\x1b[A" {
		t.Errorf("device got %q, want the bytes before alt+w unchanged and nothing after it", got)
	}
	if len(atEnter) != 1 || atEnter[0] != "dump" {
		t.Errorf("Enter saw the device holding %q, want one call made before the CR went", atEnter)
	}
}

func TestAttachLetsGoWhenTheDeviceHangsUp(t *testing.T) {
	s, conn := openFake(t, "S7020-RTR-01# ")
	eventually(t, "the prompt on the emulator", func() bool { return strings.Contains(s.Render(), "#") })

	_, out, done := attachTo(t, s, AttachOptions{})
	eventually(t, "the history on the terminal", func() bool { return strings.Contains(out.String(), "#") })

	conn.Close(websocket.StatusNormalClosure, "")
	if a := ended(t, done); a.caught || a.err != nil {
		t.Errorf("Attach = %q caught=%v err=%v, want it to return on its own after a clean hang-up", a.key.String(), a.caught, a.err)
	}
}

func TestADetachedTerminalDoesNotHoldUpTheSession(t *testing.T) {
	s, conn := openFake(t, "S7020-RTR-01# ")
	eventually(t, "the prompt on the emulator", func() bool { return strings.Contains(s.Render(), "#") })

	in, out, done := attachTo(t, s, AttachOptions{Catch: func(k uv.Key) bool { return k.String() == "ctrl+]" }})
	eventually(t, "the history on the terminal", func() bool { return strings.Contains(out.String(), "#") })
	if _, err := in.Write([]byte{0x1d}); err != nil {
		t.Fatal(err)
	}
	ended(t, done)

	for i := range 200 {
		conn.feed <- []byte("line " + string(rune('a'+i%26)) + "\r\n")
	}
	conn.feed <- []byte("still here# ")
	eventually(t, "output after the detach on the emulator", func() bool { return strings.Contains(s.Render(), "still here#") })
}
