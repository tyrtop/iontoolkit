package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

type router struct {
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func newRouter(speaks bool) *router {
	r := &router{out: make(chan []byte, 4), done: make(chan struct{})}
	if speaks {
		r.speak()
	}
	return r
}

func (r *router) speak() { r.out <- []byte("login: ") }

func (r *router) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case b := <-r.out:
		return websocket.MessageText, b, nil
	case <-r.done:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (r *router) Write(context.Context, websocket.MessageType, []byte) error { return nil }

func (r *router) Ping(context.Context) error { return nil }

func (r *router) Close(websocket.StatusCode, string) error {
	r.once.Do(func() { close(r.done) })
	return nil
}

type routers struct {
	mu     sync.Mutex
	speaks []bool
	dialed []*router
}

func (d *routers) DialToolkit(context.Context, string, int, int) (ionterm.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := newRouter(d.speaks[min(len(d.dialed), len(d.speaks)-1)])
	d.dialed = append(d.dialed, r)
	return r, nil
}

func (d *routers) dials() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.dialed)
}

func (d *routers) nth(i int) *router {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialed[i]
}

func dialRuntime(t *testing.T, d ionterm.Dialer) (*Runtime, string) {
	t.Helper()
	rt := deviceRuntime(t)
	rt.mu.Lock()
	rt.dev.Dialer = func(*api.Client) ionterm.Dialer { return d }
	rt.mu.Unlock()
	t.Cleanup(rt.Sessions().CloseAll)
	for _, el := range rt.Elements() {
		if el.Name == "acme-rtr-01" {
			return rt, el.ID
		}
	}
	t.Fatal("the fixture has no acme-rtr-01")
	return nil, ""
}

func TestAnOpenListsTheSessionOnceTheRouterSpeaksAndRedialsOneThatStaysSilent(t *testing.T) {
	t.Cleanup(ionterm.SetHandshakeForTest(50 * time.Millisecond))
	for _, tt := range []struct {
		name    string
		speaks  []bool
		dials   int
		wantErr string
	}{
		{name: "a router that prints its login", speaks: []bool{true}, dials: 1},
		{name: "silent on the first dial, then it prints", speaks: []bool{false, true}, dials: 2},
		{name: "silent on every dial", speaks: []bool{false}, dials: 2, wantErr: "no response from device after 50ms (2 tries)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &routers{speaks: tt.speaks}
			rt, eid := dialRuntime(t, d)
			err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open")
			if got := d.dials(); got != tt.dials {
				t.Errorf("dialed %d times, want %d", got, tt.dials)
			}
			s := rt.Sessions().Find(eid)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("session.open = %v, want it to fail with the session's own error %q", err, tt.wantErr)
				}
				if s != nil {
					t.Errorf("a session that never printed anything is listed: %q", s.Name)
				}
				return
			}
			if err != nil || s == nil || !s.Answered() {
				t.Errorf("session.open = %v, listed %v, want the session that printed its login", err, s != nil)
			}
			if n := rt.Sessions().Count(); n != 1 {
				t.Errorf("%d sessions listed, want the silent dial left out", n)
			}
		})
	}
}

func TestTheDialSaysItIsWaitingWhileTheRouterIsSilent(t *testing.T) {
	d := &routers{speaks: []bool{false}}
	rt, eid := dialRuntime(t, d)
	reports := rt.DialReports()
	done := make(chan error, 1)
	go func() {
		done <- rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open")
	}()
	for waiting := false; !waiting; {
		select {
		case r := <-reports:
			waiting = r.EID == eid && r.Waiting
		case <-time.After(5 * time.Second):
			t.Fatal("the dial never said it was waiting for the router")
		}
	}
	if n := rt.Sessions().Count(); n != 0 {
		t.Errorf("%d sessions listed while the router had said nothing", n)
	}
	d.nth(0).speak()
	if err := <-done; err != nil {
		t.Fatalf("session.open = %v once the router spoke", err)
	}
	if dl, ok := rt.Dialing(eid); ok {
		t.Errorf("the dial is still recorded after the open: %+v", dl)
	}
}

func TestARetrySwapsInANewSessionOnlyOnceItsRouterSpeaks(t *testing.T) {
	t.Cleanup(ionterm.SetHandshakeForTest(50 * time.Millisecond))
	for _, tt := range []struct {
		name    string
		kill    bool
		speaks  bool
		dials   int
		wantErr string
	}{
		{name: "a dead session and a router that answers the redial", kill: true, speaks: true, dials: 2},
		{name: "a dead session and a router that stays silent", kill: true, dials: 3, wantErr: "no response from device"},
		{name: "a session that is still answering", speaks: true, dials: 1, wantErr: "acme-rtr-01 is still answering"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &routers{speaks: []bool{true, tt.speaks}}
			rt, eid := dialRuntime(t, d)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rt.Exec(&Call{Ctx: ctx, Args: map[string]string{"elements": eid}}, "session.open"); err != nil {
				t.Fatal(err)
			}
			first := rt.Sessions().Find(eid)
			if tt.kill {
				d.nth(0).Close(websocket.StatusNormalClosure, "")
				if err := awaitState(ctx, first, first.Ended); err != nil {
					t.Fatal(err)
				}
			}

			err := rt.Exec(&Call{Ctx: ctx, Args: map[string]string{"element": eid}}, "session.retry")
			if got := d.dials(); got != tt.dials {
				t.Errorf("dialed %d times, want %d", got, tt.dials)
			}
			if n := rt.Sessions().Count(); n != 1 {
				t.Errorf("%d sessions listed after the retry, want 1", n)
			}
			now := rt.Sessions().Find(eid)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("session.retry = %v, want %q", err, tt.wantErr)
				}
				if now != first {
					t.Error("the old session went although nothing took its place")
				}
				return
			}
			if err != nil || now == first || !now.Answered() {
				t.Errorf("session.retry = %v, want the dead session replaced by one whose router spoke", err)
			}
			if !first.Closed() {
				t.Error("the dead session was left open after the swap")
			}
		})
	}
}

func waitForDetached(t *testing.T, rt *Runtime, want int) {
	t.Helper()
	dirty := rt.Sessions().Dirty()
	deadline := time.After(5 * time.Second)
	for rt.Sessions().DetachedCount() != want {
		select {
		case <-dirty:
		case <-deadline:
			t.Fatalf("%d detached sessions, want %d", rt.Sessions().DetachedCount(), want)
		}
	}
}

func TestADetachedShellIsClosedOnceItEndsOrItsTimeIsUp(t *testing.T) {
	reattach := func(t *testing.T, rt *Runtime, _ *router) {
		if err := rt.Exec(&Call{}, "session.reattach"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, tt := range []struct {
		name   string
		keep   string
		then   func(t *testing.T, rt *Runtime, r *router)
		closed bool
	}{
		{name: "its device hangs up", keep: "30m", closed: true,
			then: func(_ *testing.T, _ *Runtime, r *router) { r.Close(websocket.StatusNormalClosure, "") }},
		{name: "it is kept past its time", keep: "20ms", closed: true},
		{name: "its time is cut short while it is detached", keep: "30m", closed: true,
			then: func(t *testing.T, rt *Runtime, _ *router) {
				if err := rt.Set("detached_for", "20ms"); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "it is reattached before its time", keep: "20ms", then: reattach},
		{name: "off keeps it until it ends", keep: "off",
			then: func(*testing.T, *Runtime, *router) { time.Sleep(100 * time.Millisecond) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &routers{speaks: []bool{true}}
			rt, eid := dialRuntime(t, d)
			if err := rt.Set("detached_for", tt.keep); err != nil {
				t.Fatal(err)
			}
			if err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open"); err != nil {
				t.Fatal(err)
			}
			s := rt.Sessions().Find(eid)
			if err := rt.Exec(&Call{}, "session.detach"); err != nil || rt.Sessions().DetachedCount() != 1 {
				t.Fatalf("session.detach = %v, %d detached", err, rt.Sessions().DetachedCount())
			}
			if tt.then != nil {
				tt.then(t, rt, d.nth(0))
			}
			if tt.closed {
				waitForDetached(t, rt, 0)
				if !s.Closed() {
					t.Error("the shell left the detached list but is still open")
				}
				return
			}
			if s.Closed() {
				t.Error("the shell was closed although it was still in use")
			}
		})
	}
}

func TestAShellDetachedOrClosedWhileItOpens(t *testing.T) {
	for _, tt := range []struct {
		cmd      string
		detached int
		stopped  bool
	}{
		{cmd: "session.detach", detached: 1},
		{cmd: "session.close", stopped: true},
	} {
		t.Run(tt.cmd, func(t *testing.T) {
			d := &routers{speaks: []bool{false}}
			rt, eid := dialRuntime(t, d)
			reports := rt.DialReports()
			done := make(chan error, 1)
			go func() {
				done <- rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open")
			}()
			for waiting := false; !waiting; {
				select {
				case r := <-reports:
					waiting = r.EID == eid && r.Waiting
				case <-time.After(5 * time.Second):
					t.Fatal("the dial never got to waiting for the router")
				}
			}

			if err := rt.Exec(&Call{Args: map[string]string{"element": eid}}, tt.cmd); err != nil {
				t.Fatalf("%s while it opens = %v", tt.cmd, err)
			}
			if opening := rt.Opening(); len(opening) != 0 {
				t.Errorf("still reported as opening after %s: %v", tt.cmd, opening)
			}
			if !tt.stopped {
				d.nth(0).speak()
			}
			if err := <-done; errors.Is(err, ErrStopped) != tt.stopped || (err != nil && !tt.stopped) {
				t.Errorf("session.open = %v, want stopped %v", err, tt.stopped)
			}
			if n := rt.Sessions().Count(); n != 0 {
				t.Errorf("%d shells in a tab after %s, want none", n, tt.cmd)
			}
			if n := rt.DetachedCount(); n != tt.detached {
				t.Errorf("%d detached after %s, want %d", n, tt.cmd, tt.detached)
			}
			if dl, ok := rt.Dialing(eid); ok {
				t.Errorf("the dial is still recorded: %+v", dl)
			}
		})
	}
}

type sizedRouters struct {
	routers
	cols, rows int
}

func (d *sizedRouters) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	d.mu.Lock()
	d.cols, d.rows = cols, rows
	d.mu.Unlock()
	return d.routers.DialToolkit(ctx, eid, cols, rows)
}

func TestAShellIsDialedAtTheTerminalsSizeWithoutSavingIt(t *testing.T) {
	d := &sizedRouters{routers: routers{speaks: []bool{true}}}
	rt, eid := dialRuntime(t, d)
	cols, rows := rt.Geometry()
	rt.SetTerminal(132, 43)
	if err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open"); err != nil {
		t.Fatal(err)
	}
	if d.cols != 132 || d.rows != 43 {
		t.Errorf("dialed at %dx%d, want the 132x43 terminal the shell will be attached to", d.cols, d.rows)
	}
	if c, r := rt.Geometry(); c != cols || r != rows {
		t.Errorf("session_cols/rows became %dx%d, want the saved %dx%d left alone", c, r, cols, rows)
	}
}
