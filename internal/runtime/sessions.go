package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

const AreaSessions = "sessions"

type Dial struct {
	EID     string
	Attempt int
	Of      int
	LastErr string
	Failed  bool
}

type sessionsState struct {
	mgr     *ionterm.Manager
	dials   map[string]Dial
	reports chan Dial
}

func (rt *Runtime) Sessions() *ionterm.Manager {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sess.mgr == nil {
		rt.sess.mgr = ionterm.NewManager()
	}
	return rt.sess.mgr
}

func (rt *Runtime) DialReports() <-chan Dial { return rt.dialReports() }

func (rt *Runtime) dialReports() chan Dial {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sess.reports == nil {
		rt.sess.reports = make(chan Dial, 32)
	}
	return rt.sess.reports
}

func (rt *Runtime) editDials(edit func(d map[string]Dial)) {
	rt.mu.Lock()
	if rt.sess.dials == nil {
		rt.sess.dials = map[string]Dial{}
	}
	edit(rt.sess.dials)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaSessions})
}

func (rt *Runtime) Dials() map[string]Dial {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make(map[string]Dial, len(rt.sess.dials))
	for k, v := range rt.sess.dials {
		out[k] = v
	}
	return out
}

func (rt *Runtime) Dialing(eid string) (Dial, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	d, ok := rt.sess.dials[eid]
	return d, ok
}

func (rt *Runtime) NoteDial(d Dial) {
	rt.editDials(func(m map[string]Dial) { m[d.EID] = d })
}

func (rt *Runtime) MarkDialing(eid string) {
	rt.NoteDial(Dial{EID: eid, Attempt: 1, Of: 1})
}

func (rt *Runtime) DialDone(eid string) {
	rt.editDials(func(m map[string]Dial) { delete(m, eid) })
}

func (rt *Runtime) DialFailed(eid, reason string) {
	rt.editDials(func(m map[string]Dial) {
		d := m[eid]
		d.EID, d.Failed, d.LastErr = eid, true, reason
		m[eid] = d
	})
}

func (rt *Runtime) Connecting() bool {
	for _, d := range rt.Dials() {
		if !d.Failed {
			return true
		}
	}
	return false
}

func (rt *Runtime) reportDials(c *api.Client) {
	ch := rt.dialReports()
	c.SetDialReporter(func(a api.DialAttempt) {
		d := Dial{EID: a.ElementID, Attempt: a.Attempt, Of: a.Of}
		if a.Err != nil {
			d.LastErr = a.Err.Error()
		}
		rt.NoteDial(d)
		select {
		case ch <- d:
		default:
		}
	})
}

func (rt *Runtime) Geometry() (int, int) {
	c, _ := rt.Get("session_cols")
	r, _ := rt.Get("session_rows")
	cols, _ := strconv.Atoi(c)
	rows, _ := strconv.Atoi(r)
	return cols, rows
}

func (rt *Runtime) OpenSession(ctx context.Context, eid, name, tsg string) (*ionterm.Session, error) {
	c := rt.Clients().Get(tsg)
	if c == nil {
		return nil, fmt.Errorf("could not reach %s", name)
	}
	rt.mu.RLock()
	dialer := rt.dev.Dialer
	rt.mu.RUnlock()
	var d ionterm.Dialer
	if dialer != nil {
		d = dialer(c)
	}
	rt.MarkDialing(eid)
	if d == nil {
		rt.DialFailed(eid, "no way to dial a device")
		return nil, fmt.Errorf("%s: no way to dial a device", name)
	}
	cols, rows := rt.Geometry()
	user, pass := rt.Login(tsg)
	rt.reportDials(c)
	s, err := rt.Sessions().Open(ctx, d, eid, name, cols, rows, ionterm.Credentials{User: user, Pass: pass})
	if err != nil {
		rt.DialFailed(eid, err.Error())
		return nil, err
	}
	rt.DialDone(eid)
	return s, nil
}

var ErrLoggingIn = errors.New("the session is still logging in")

func (rt *Runtime) InsertLine(text string) error {
	if err := rt.CheckCommands([]string{text}); err != nil {
		return err
	}
	s := rt.Sessions().Active()
	if s == nil || s.Err() != nil {
		return nil
	}
	if s.LoggingIn() {
		return ErrLoggingIn
	}
	s.Insert(text + " ")
	return nil
}

func (rt *Runtime) waitLoggedIn(ctx context.Context) {
	s := rt.Sessions().Active()
	if s == nil {
		return
	}
	ready := time.Now().Add(10 * time.Second)
	for !s.LoggedIn() && time.Now().Before(ready) && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

func sessionCommands() []Command {
	return []Command{
		{
			Name: "session.open", Slow: true, Help: "open a device session on each element: elements=ID,ID (default: the selection)",
			Args: []Arg{{Name: "elements"}},
			Run: func(rt *Runtime, c *Call) error {
				rows := rt.Targets()
				if ids := c.Arg("elements"); ids != "" {
					rows = rt.knownRows(c, strings.Split(ids, ","))
				}
				if len(rows) == 0 {
					return fmt.Errorf("session.open: no elements")
				}
				var failed []string
				for _, r := range rows {
					if t, ok := rt.TenantOf(r.TSG); ok {
						if _, err := rt.Connect(c.Ctx, t); err != nil {
							failed = append(failed, r.Label()+": "+rt.explain(err))
							continue
						}
					}
					if _, err := rt.OpenSession(c.Ctx, r.ID, r.Label(), r.TSG); err != nil {
						failed = append(failed, r.Label()+": "+err.Error())
						continue
					}
					c.Say("%s open", r.Label())
				}
				if len(failed) > 0 {
					return fmt.Errorf("%s", strings.Join(failed, "; "))
				}
				return nil
			},
		},
		{
			Name: "session.list", Help: "the open sessions, the active one marked",
			Run: func(rt *Runtime, c *Call) error {
				mgr := rt.Sessions()
				for i, s := range mgr.All() {
					mark := " "
					if i == mgr.ActiveIndex() {
						mark = ">"
					}
					c.Say("%s %d %s", mark, i+1, s.Name)
				}
				return nil
			},
		},
		{
			Name: "session.next", Help: "the next session tab",
			Run: func(rt *Runtime, _ *Call) error { rt.Sessions().Next(); return nil },
		},
		{
			Name: "session.prev", Help: "the previous session tab",
			Run: func(rt *Runtime, _ *Call) error { rt.Sessions().Prev(); return nil },
		},
		{
			Name: "session.close", Help: "close the active session",
			Run: func(rt *Runtime, _ *Call) error { rt.Sessions().CloseActive(); return nil },
		},
		{
			Name: "session.detach", Help: "put the active session aside, still connected",
			Run: func(rt *Runtime, c *Call) error {
				if s, ok := rt.Sessions().Detach(); ok {
					c.Say("detached %s", s.Name)
				}
				return nil
			},
		},
		{
			Name: "session.reattach", Help: "bring back the last session put aside",
			Run: func(rt *Runtime, c *Call) error {
				if s, ok := rt.Sessions().Reattach(); ok {
					c.Say("reattached %s", s.Name)
				}
				return nil
			},
		},
		{
			Name: "session.insert", Slow: true, Help: "put text on the active session's line without sending it",
			Args: []Arg{{Name: "text", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				rt.waitLoggedIn(c.Ctx)
				return rt.InsertLine(c.Arg("text"))
			},
		},
		{
			Name: "session.send", Slow: true, Help: "type a line into the active session and wait for it to settle",
			Args: []Arg{{Name: "text", Required: true}, {Name: "wait", Default: "2s"}},
			Run: func(rt *Runtime, c *Call) error {
				s := rt.Sessions().Active()
				if s == nil {
					return fmt.Errorf("session.send: no session open")
				}
				wait, err := time.ParseDuration(c.Arg("wait"))
				if err != nil {
					return fmt.Errorf("session.send: wait %q is not a duration", c.Arg("wait"))
				}
				rt.waitLoggedIn(c.Ctx)
				s.SendText(c.Arg("text") + "\r")
				select {
				case <-time.After(wait):
				case <-c.Ctx.Done():
				}
				c.Say("%s", s.Render())
				return nil
			},
		},
	}
}

func (rt *Runtime) ResetSessions() {
	rt.mu.Lock()
	rt.sess.mgr = ionterm.NewManager()
	rt.mu.Unlock()
}
