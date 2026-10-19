package fixture

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"tyrtop.com/iontk/internal/ionterm"
)

// Dialer hands out playback devices. It satisfies ionterm.Dialer, so a TUI
// session in fixture mode goes through the real pane, autologin and emulator.
func (s *Set) Dialer() ionterm.Dialer { return dialer{s} }

type dialer struct{ set *Set }

func (d dialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.set.dial(eid)
}

func (s *Set) dial(eid string) (*conn, error) {
	el := s.element(eid)
	if el == nil {
		// The shape a rejected handshake takes, so the dial error paths are the
		// ones being exercised.
		return nil, fmt.Errorf("expected handshake response status 101 but got 404")
	}
	t := s.tenant(el.tsg)

	c := &conn{
		out:    make(chan []byte, 64),
		in:     make(chan []byte, 16),
		closed: make(chan struct{}),
	}
	go c.serve(&device{set: s, tenant: t, el: el})
	return c, nil
}

// What a read or write gets after the device hung up. The real client reports a
// websocket close error, and ionterm tells a clean hang-up from a failure by
// inspecting it.
var errClosed = websocket.CloseError{Code: websocket.StatusNormalClosure}

// conn is a websocket-shaped pipe to a playback device. It satisfies both
// ionterm.Conn and ioncli.Conn, which is what lets one device serve the TUI's
// sessions and a batch run's.
type conn struct {
	out    chan []byte
	in     chan []byte
	closed chan struct{}

	once sync.Once
}

func (c *conn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-c.closed:
		return 0, nil, errClosed
	case b, ok := <-c.out:
		if !ok {
			return 0, nil, errClosed
		}
		return websocket.MessageText, b, nil
	}
}

func (c *conn) Write(ctx context.Context, _ websocket.MessageType, p []byte) error {
	// Copied: the caller owns the buffer, and the device reads it later.
	b := append([]byte(nil), p...)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errClosed
	case c.in <- b:
		return nil
	}
}

// A playback device always answers, so there is nothing to time out. Returning
// an error once closed is what stops ionterm calling it a hang.
func (c *conn) Ping(ctx context.Context) error {
	select {
	case <-c.closed:
		return errClosed
	default:
		return nil
	}
}

func (c *conn) Close(websocket.StatusCode, string) error {
	c.hangUp()
	return nil
}

func (c *conn) hangUp() {
	c.once.Do(func() { close(c.closed) })
}

func (c *conn) send(s string) bool {
	select {
	case <-c.closed:
		return false
	case c.out <- []byte(s):
		return true
	}
}

// line collects typed bytes up to a carriage return, which is how the device
// sees a command: iontk writes keystrokes, not lines.
func (c *conn) line() (string, bool) {
	var b strings.Builder
	for {
		select {
		case <-c.closed:
			return "", false
		case in := <-c.in:
			for _, ch := range string(in) {
				switch ch {
				case '\r', '\n':
					return b.String(), true
				case 0x7f, '\b':
					// Backspace, so a typed-then-corrected command still works.
					if s := b.String(); s != "" {
						b.Reset()
						b.WriteString(s[:len(s)-1])
					}
				default:
					b.WriteRune(ch)
				}
			}
		}
	}
}

func (c *conn) serve(d *device) {
	defer c.hangUp()
	d.run(c)
}

// device is one appliance's CLI: the login exchange, then a command loop.
type device struct {
	set    *Set
	tenant *Tenant
	el     *Element
	// pagingOff is set by the command --pager-off sends, so the fixture honours
	// the thing iontk does to avoid the pager.
	pagingOff bool
}

func (d *device) prompt() string { return d.el.Name + "#" }

func (d *device) run(c *conn) {
	if !c.send("\r\nION Toolkit\r\n\r\n") {
		return
	}

	for {
		if !c.send("login: ") {
			return
		}
		user, ok := c.line()
		if !ok {
			return
		}
		// The device echoes what is typed; without this the pane stays blank
		// while autologin works, which is the bug a fixture should catch.
		if !c.send(user+"\r\n") || !c.send("Password: ") {
			return
		}
		pass, ok := c.line()
		if !ok {
			return
		}
		if !c.send("\r\n") {
			return
		}
		if d.tenant != nil && d.tenant.login(user, pass) {
			break
		}
		if !c.send("Login incorrect\r\n") {
			return
		}
	}

	if !c.send(d.prompt() + " ") {
		return
	}

	for {
		cmd, ok := c.line()
		if !ok {
			return
		}
		// The echo redraws the prompt and the command from column zero, which
		// is the frame shape the appliance produces: the pane overwrites the
		// prompt it already has, and ioncli finds the marker it cuts output at.
		if !c.send("\r" + d.prompt() + " " + cmd + "\r\n") {
			return
		}
		cmd = strings.TrimSpace(cmd)

		switch {
		case cmd == "":
		case cmd == "exit" || cmd == "quit" || cmd == "logout":
			c.send("\r\n")
			return
		case cmd == "set paging off":
			d.pagingOff = true
		default:
			if !d.write(c, d.response(cmd)) {
				return
			}
		}
		if !c.send(d.prompt() + " ") {
			return
		}
	}
}

func (d *device) response(cmd string) string {
	if out, ok := d.el.Responses[cmd]; ok {
		return out
	}
	return strings.ReplaceAll(d.set.UnknownResponse, "{command}", cmd) + "\n"
}

// write sends a command's output, pausing on the pager prompt when the element
// is set to page and paging has not been turned off.
func (d *device) write(c *conn, out string) bool {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return true
	}
	lines := strings.Split(out, "\n")

	if !d.el.Pages || d.pagingOff {
		return c.send(strings.Join(lines, "\r\n") + "\r\n")
	}

	for i := 0; i < len(lines); i += d.el.PageLines {
		end := min(i+d.el.PageLines, len(lines))
		if !c.send(strings.Join(lines[i:end], "\r\n") + "\r\n") {
			return false
		}
		if end == len(lines) {
			break
		}
		// No newline after the prompt: the device stops there and waits, which
		// is what makes the scraper's last-line matching necessary.
		if !c.send(pagerPrompt) {
			return false
		}
		if _, ok := d.awaitSpace(c); !ok {
			return false
		}
		// Erased in place, the way the appliance does it.
		if !c.send(strings.Repeat("\b", len(pagerPrompt))) {
			return false
		}
	}
	return true
}

// awaitSpace waits for the keypress the pager asked for. A q gives up on the
// rest, which is the other half of what the prompt offers.
func (d *device) awaitSpace(c *conn) (quit bool, ok bool) {
	for {
		select {
		case <-c.closed:
			return false, false
		case in := <-c.in:
			for _, ch := range string(in) {
				switch ch {
				case ' ':
					return false, true
				case 'q', 'Q':
					return true, true
				}
			}
		}
	}
}
