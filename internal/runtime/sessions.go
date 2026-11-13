package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/script"
)

const AreaSessions = "sessions"

type Dial struct {
	EID     string
	Name    string
	Attempt int
	Of      int
	LastErr string
	Failed  bool
	Waiting bool
	Aside   bool
	seq     int
}

type sessionsState struct {
	mgr     *ionterm.Manager
	dials   map[string]Dial
	reports chan Dial
	keep    chan struct{}
	seq     int
	stops   map[string]context.CancelFunc
	stopped map[string]bool
	term    [2]int
}

var ErrStopped = errors.New("closed before it opened")

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
	rt.changed(Event{Area: AreaSessions, Rows: true})
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

func (rt *Runtime) MarkDialing(eid, name string) {
	rt.editDials(func(m map[string]Dial) {
		delete(rt.sess.stopped, eid)
		rt.markDial(m, eid, name)
	})
}

func (rt *Runtime) noteOpening(eid, name string) bool {
	ok := true
	rt.editDials(func(m map[string]Dial) {
		if rt.sess.stopped[eid] {
			delete(rt.sess.stopped, eid)
			ok = false
			return
		}
		rt.markDial(m, eid, name)
	})
	return ok
}

func (rt *Runtime) markDial(m map[string]Dial, eid, name string) {
	d, ok := m[eid]
	if !ok || d.Failed {
		rt.sess.seq++
		d = Dial{EID: eid, seq: rt.sess.seq}
	}
	d.Attempt, d.Of, d.Failed, d.Waiting, d.LastErr = 1, 1, false, false, ""
	if name != "" {
		d.Name = name
	}
	m[eid] = d
}

func (rt *Runtime) startOpening(eid string, stop context.CancelFunc) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sess.stopped[eid] {
		delete(rt.sess.stopped, eid)
		return false
	}
	if rt.sess.stops == nil {
		rt.sess.stops = map[string]context.CancelFunc{}
	}
	rt.sess.stops[eid] = stop
	return true
}

func (rt *Runtime) endOpening(eid string) {
	rt.mu.Lock()
	delete(rt.sess.stops, eid)
	rt.mu.Unlock()
}

func (rt *Runtime) takeStopped(eid string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	stopped := rt.sess.stopped[eid]
	delete(rt.sess.stopped, eid)
	return stopped
}

func (rt *Runtime) Opening() []Dial {
	mgr := rt.Sessions()
	rt.mu.RLock()
	var out []Dial
	for _, d := range rt.sess.dials {
		if !d.Failed && !d.Aside && mgr.IndexOf(d.EID) < 0 {
			out = append(out, d)
		}
	}
	rt.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

func (rt *Runtime) DetachedCount() int {
	mgr := rt.Sessions()
	n := mgr.DetachedCount()
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, d := range rt.sess.dials {
		if d.Aside && !d.Failed {
			n++
		}
	}
	return n
}

func (rt *Runtime) DialDone(eid string) {
	rt.editDials(func(m map[string]Dial) { delete(m, eid) })
}

func (rt *Runtime) DialFailed(eid, reason string) {
	rt.editDials(func(m map[string]Dial) {
		d := m[eid]
		d.EID, d.Failed, d.Waiting, d.LastErr = eid, true, false, reason
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
	c.SetDialReporter(func(a api.DialAttempt) {
		rt.noteDialing(a.ElementID, func(d *Dial) {
			d.Attempt, d.Of = a.Attempt, a.Of
			if a.Err != nil {
				d.LastErr = a.Err.Error()
			}
		})
	})
}

func (rt *Runtime) noteDialing(eid string, edit func(d *Dial)) {
	var d Dial
	rt.editDials(func(m map[string]Dial) {
		d = m[eid]
		d.EID, d.Failed, d.Waiting = eid, false, false
		edit(&d)
		m[eid] = d
	})
	select {
	case rt.dialReports() <- d:
	default:
	}
}

func (rt *Runtime) Geometry() (int, int) {
	c, _ := rt.Get("session_cols")
	r, _ := rt.Get("session_rows")
	cols, _ := strconv.Atoi(c)
	rows, _ := strconv.Atoi(r)
	return cols, rows
}

func (rt *Runtime) SetTerminal(cols, rows int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.sess.term = [2]int{cols, rows}
}

func (rt *Runtime) dialSize() (int, int) {
	rt.mu.RLock()
	t := rt.sess.term
	rt.mu.RUnlock()
	if t[0] > 0 && t[1] > 0 {
		return t[0], t[1]
	}
	return rt.Geometry()
}

func (rt *Runtime) Reopen() (*ionterm.Session, string, error) {
	if s, ok := rt.Sessions().Reattach(); ok {
		return s, "", nil
	}
	eid, _, ok := rt.Sessions().LastClosed()
	if !ok {
		return nil, "", fmt.Errorf("nothing to reattach")
	}
	return nil, eid, nil
}

func (rt *Runtime) OpenSession(ctx context.Context, eid, name, tsg string) (*ionterm.Session, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if !rt.startOpening(eid, stop) {
		return nil, ErrStopped
	}
	defer rt.endOpening(eid)

	t := rt.TenantFor(tsg)
	c, err := rt.Connect(ctx, t)
	if err != nil {
		if rt.takeStopped(eid) {
			return nil, ErrStopped
		}
		return nil, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
	}
	rt.mu.RLock()
	dialer := rt.dev.Dialer
	rt.mu.RUnlock()
	var d ionterm.Dialer
	if dialer != nil {
		d = dialer(c)
	}
	if !rt.noteOpening(eid, name) {
		return nil, ErrStopped
	}
	if d == nil {
		rt.DialFailed(eid, "no way to dial a device")
		return nil, fmt.Errorf("%s: no way to dial a device", name)
	}
	cols, rows := rt.dialSize()
	user, pass := rt.Login(tsg)
	rt.reportDials(c)
	s, err := rt.dialUntilHeard(ctx, d, eid, name, cols, rows, ionterm.Credentials{User: user, Pass: pass})
	mgr := rt.Sessions()
	rt.mu.Lock()
	stopped, aside := rt.sess.stopped[eid], rt.sess.dials[eid].Aside
	delete(rt.sess.stopped, eid)
	switch {
	case err != nil, stopped:
	case aside:
		mgr.AddDetached(s)
		delete(rt.sess.dials, eid)
	default:
		mgr.Add(s)
		delete(rt.sess.dials, eid)
	}
	rt.mu.Unlock()
	switch {
	case stopped:
		if s != nil {
			s.Close()
		}
		return nil, ErrStopped
	case err != nil:
		rt.DialFailed(eid, err.Error())
		return nil, err
	}
	if aside {
		rt.keepDetached(s)
	}
	rt.DialDone(eid)
	return s, nil
}

func (rt *Runtime) asideOnceUp(eid string) bool {
	return rt.editOpening(eid, func(d *Dial) { d.Aside = true })
}

func (rt *Runtime) stopOpening(eid string) bool {
	return rt.editOpening(eid, func(d *Dial) {
		if rt.sess.stopped == nil {
			rt.sess.stopped = map[string]bool{}
		}
		rt.sess.stopped[eid] = true
		if stop := rt.sess.stops[eid]; stop != nil {
			stop()
		}
		d.Failed = true
	})
}

func (rt *Runtime) editOpening(eid string, edit func(d *Dial)) bool {
	mgr := rt.Sessions()
	rt.mu.Lock()
	d, dialing := rt.sess.dials[eid]
	ok := dialing && !d.Failed && mgr.IndexOf(eid) < 0
	if ok {
		edit(&d)
		if d.Failed {
			delete(rt.sess.dials, eid)
		} else {
			rt.sess.dials[eid] = d
		}
	}
	rt.mu.Unlock()
	if ok {
		rt.changed(Event{Area: AreaSessions, Rows: true})
	}
	return ok
}

const silentTries = 2

func (rt *Runtime) dialUntilHeard(ctx context.Context, d ionterm.Dialer, eid, name string, cols, rows int, creds ionterm.Credentials) (*ionterm.Session, error) {
	var err error
	for try := 1; try <= silentTries; try++ {
		var s *ionterm.Session
		if s, err = rt.Sessions().Dial(ctx, d, eid, name, cols, rows, creds); err != nil {
			return nil, err
		}
		rt.noteDialing(eid, func(x *Dial) { x.Waiting = true })
		if err = awaitState(ctx, s, s.Answered); err == nil {
			return s, nil
		}
		s.Close()
		if ctx.Err() != nil {
			return nil, err
		}
		rt.noteDialing(eid, func(x *Dial) { x.LastErr = err.Error() })
	}
	return nil, fmt.Errorf("%w (%d tries)", err, silentTries)
}

func (rt *Runtime) DetachedFor() time.Duration {
	v, _ := rt.Get("detached_for")
	d, _ := time.ParseDuration(v)
	return d
}

func (rt *Runtime) detachedChanged() <-chan struct{} {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sess.keep == nil {
		rt.sess.keep = make(chan struct{})
	}
	return rt.sess.keep
}

func (rt *Runtime) wakeDetached() {
	rt.mu.Lock()
	if rt.sess.keep != nil {
		close(rt.sess.keep)
		rt.sess.keep = nil
	}
	rt.mu.Unlock()
}

func (rt *Runtime) keepDetached(s *ionterm.Session) {
	mgr := rt.Sessions()
	since, left, ok := mgr.DetachedSince(s)
	if !ok {
		return
	}
	go func() {
		for rt.watchDetached(mgr, s, since, left) {
		}
	}()
}

func (rt *Runtime) watchDetached(mgr *ionterm.Manager, s *ionterm.Session, since time.Time, left <-chan struct{}) bool {
	changed, setting := s.Changed(), rt.detachedChanged()
	if gone(s) != nil {
		mgr.CloseDetached(s)
		return false
	}
	var expired <-chan time.Time
	if keep := rt.DetachedFor(); keep > 0 {
		t := time.NewTimer(time.Until(since.Add(keep)))
		defer t.Stop()
		expired = t.C
	}
	select {
	case <-left:
		return false
	case <-expired:
		mgr.CloseDetached(s)
		return false
	case <-changed:
	case <-setting:
	}
	return true
}

func Retryable(s *ionterm.Session) error {
	if s.Err() != nil || s.Hung() || s.Ended() {
		return nil
	}
	return fmt.Errorf("%s is still answering", s.Name)
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

func (rt *Runtime) waitLoggedIn(ctx context.Context) error {
	return waitReady(ctx, rt.Sessions().Active())
}

func waitReady(ctx context.Context, s *ionterm.Session) error {
	if s == nil {
		return nil
	}
	if err := awaitState(ctx, s, func() bool { return s.LoggedIn() && promptSeen(s) }); err != nil {
		return fmt.Errorf("%s never got to its prompt: %w", s.Name, err)
	}
	return nil
}

func awaitState(ctx context.Context, s *ionterm.Session, done func() bool) error {
	for {
		changed := s.Changed()
		if done() {
			return nil
		}
		if err := gone(s); err != nil {
			return err
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func gone(s *ionterm.Session) error {
	switch {
	case s.Err() != nil:
		return s.Err()
	case s.Ended():
		return errors.New("the session ended")
	case s.Hung():
		return errors.New("the session stopped answering")
	case s.Closed():
		return errors.New("the session was closed")
	}
	return nil
}

func promptSeen(s *ionterm.Session) bool {
	return s.Name == "" || strings.Contains(s.Buffer(), s.Name+"#")
}

func (rt *Runtime) sessionFor(c *Call, cmd string) (*ionterm.Session, error) {
	if eid := c.Arg("element"); eid != "" {
		if s := rt.Sessions().Find(eid); s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("%s: no session is open on element %s", cmd, eid)
	}
	if s := rt.Sessions().Active(); s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("%s: no session open", cmd)
}

type exchange struct {
	prompt, typed, expect string
	echoes, lines         int
	paged                 int
}

func newExchange(buf, prompt, text, expect string) *exchange {
	lines := strings.Split(buf, "\n")
	x := &exchange{prompt: prompt, typed: strings.TrimSpace(text), expect: expect, lines: len(lines), paged: -1}
	x.echoes = len(x.echoSpans(lines))
	return x
}

func (x *exchange) printable() bool {
	return strings.TrimFunc(x.typed, unicode.IsControl) != ""
}

func (x *exchange) echoSpans(lines []string) [][2]int {
	if !x.printable() {
		return nil
	}
	want := squash(x.typed)
	var spans [][2]int
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], x.prompt) {
			continue
		}
		got := squash(lines[i][len(x.prompt):])
		for j := i; got != "" && strings.HasPrefix(want, got); j++ {
			if got == want {
				spans = append(spans, [2]int{i, j})
				i = j
				break
			}
			if j+1 >= len(lines) {
				break
			}
			got += squash(lines[j+1])
		}
	}
	return spans
}

func squash(s string) string { return strings.Join(strings.Fields(s), "") }

func (x *exchange) atPrompt(line string) bool { return strings.TrimSpace(line) == x.prompt }

func (x *exchange) step(lines []string) (answer string, done bool, write string) {
	last := len(lines) - 1
	if last > x.paged && ioncli.IsPager(lines[last]) {
		x.paged = last
		return "", false, " "
	}
	from := min(x.lines, len(lines))
	if x.printable() {
		spans := x.echoSpans(lines)
		if len(spans) <= x.echoes {
			return "", false, ""
		}
		from = spans[len(spans)-1][1] + 1
	}
	if x.expect != "" {
		text := strings.Join(lines[min(from, len(lines)):], "\n")
		if i := strings.Index(text, x.expect); i >= 0 {
			return strings.Trim(text[:i], "\n"), true, ""
		}
		return "", false, ""
	}
	if last < from || !x.atPrompt(lines[last]) {
		return "", false, ""
	}
	var kept []string
	for _, l := range lines[from:last] {
		if rest, hit := ioncli.StripPager(l); hit {
			if rest == "" {
				continue
			}
			l = rest
		}
		kept = append(kept, l)
	}
	return strings.Trim(strings.Join(kept, "\n"), "\n"), true, ""
}

type SentLine struct {
	Output  string
	Planned bool
	Writes  []script.Write
}

func (r SentLine) PlannedWrites() []script.Write { return r.Writes }

func (r SentLine) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"output": r.Output, "planned": r.Planned})
}

func isWrite(line string) bool {
	return strings.TrimFunc(line, unicode.IsControl) != "" && !ioncli.IsReadOnly(line)
}

func (rt *Runtime) lineTarget(c *Call) (name, eid string) {
	if s, err := rt.sessionFor(c, ""); err == nil {
		return s.Name, s.ElementID
	}
	eid = c.Arg("element")
	if len(rt.Elements()) == 0 {
		rt.LoadCache()
	}
	for _, el := range rt.Elements() {
		if el.ID == eid {
			return el.Label(), eid
		}
	}
	return eid, eid
}

func awaitAnswer(ctx context.Context, s *ionterm.Session, x *exchange) (string, error) {
	var out string
	err := awaitState(ctx, s, func() bool {
		answer, done, write := x.step(strings.Split(s.Buffer(), "\n"))
		if write != "" {
			s.SendText(write)
		}
		out = answer
		return done
	})
	return out, err
}

func sessionCommands() []Command {
	return []Command{
		{
			Name: "session.open", Slow: true, Help: "open a device session on each element: elements=ID,ID (default: the selection)",
			Args: []Arg{{Name: "elements"}},
			Run: func(rt *Runtime, c *Call) error {
				rows := rt.ElementsArg(c, "elements")
				if len(rows) == 0 {
					return fmt.Errorf("session.open: no elements")
				}
				rows, note := rt.Reach(rows)
				if len(rows) == 0 {
					return errors.New(note)
				}
				if note != "" {
					c.Say("%s", note)
				}
				var errs []error
				for _, r := range rows {
					if _, err := rt.OpenSession(c.Ctx, r.ID, r.Label(), r.TSG); err != nil {
						rt.Refused(r.TSG, err)
						errs = append(errs, fmt.Errorf("%s: %w", r.Label(), err))
						continue
					}
					c.Say("%s open", r.Label())
				}
				return errors.Join(errs...)
			},
		},
		{
			Name: "session.retry", Slow: true, Help: "open a new session in place of one that failed, stopped answering or ended",
			Long: "Open a new session on the element of one that failed, stopped answering or ended, and close the old one once the new one's device responds. If the new one doesn't come up, the old one is kept.",
			Args: []Arg{{Name: "element", Help: "Element `ID` of the session (default: the active session)"}},
			Run: func(rt *Runtime, c *Call) error {
				s, err := rt.sessionFor(c, "session.retry")
				if err != nil {
					return err
				}
				if err := Retryable(s); err != nil {
					return err
				}
				rows, note := rt.Reach(rt.rowsFor([]string{s.ElementID}, c.Tenant))
				if len(rows) == 0 {
					return errors.New(note)
				}
				if _, err := rt.OpenSession(c.Ctx, s.ElementID, s.Name, rows[0].TSG); err != nil {
					rt.Refused(rows[0].TSG, err)
					return fmt.Errorf("%s: %w", s.Name, err)
				}
				rt.Sessions().CloseDeadOn(s.ElementID)
				c.Say("%s open again", s.Name)
				return nil
			},
		},
		{
			Name: "session.export", Help: "save a session's scrollback to a file under exports/",
			Args: []Arg{{Name: "element", Help: "Element `ID` of the session to save (default: the active session)"}},
			Run: func(rt *Runtime, c *Call) error {
				s, err := rt.sessionFor(c, "session.export")
				if err != nil {
					return err
				}
				where, err := rt.Export(s.Name, "txt", s.Buffer())
				if err == nil {
					c.Say("exported %s scrollback to %s", s.Name, where)
					c.Result = where
				}
				return err
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
			Name: "session.close", Help: "close a session",
			Args: []Arg{{Name: "element", Help: "Element `ID` of the session to close (default: the active session)"}},
			Run: func(rt *Runtime, c *Call) error {
				eid := c.Arg("element")
				if eid == "" {
					rt.Sessions().CloseActive()
					return nil
				}
				if rt.stopOpening(eid) {
					c.Say("stopped opening %s", rt.rowsFor([]string{eid}, c.Tenant)[0].Label())
					return nil
				}
				if !rt.Sessions().Close(eid) {
					return fmt.Errorf("session.close: no session is open or opening on element %s", eid)
				}
				return nil
			},
		},
		{
			Name: "session.detach", Help: "put a session aside, still connected; one still opening goes aside once it's up",
			Args: []Arg{{Name: "element", Help: "Element `ID` of the session (default: the active session)"}},
			Run: func(rt *Runtime, c *Call) error {
				mgr := rt.Sessions()
				if eid := c.Arg("element"); eid != "" {
					if rt.asideOnceUp(eid) {
						c.Say("%s goes aside once it's up", rt.rowsFor([]string{eid}, c.Tenant)[0].Label())
						return nil
					}
					i := mgr.IndexOf(eid)
					if i < 0 {
						return fmt.Errorf("session.detach: no session is open or opening on element %s", eid)
					}
					mgr.Select(i)
				}
				if s, ok := mgr.Detach(); ok {
					rt.keepDetached(s)
					c.Say("detached %s", s.Name)
				}
				return nil
			},
		},
		{
			Name: "session.reopen", Slow: true, Help: "bring back the last session put aside, else open again the last one closed",
			Run: func(rt *Runtime, c *Call) error {
				s, eid, err := rt.Reopen()
				switch {
				case err != nil:
					return err
				case s != nil:
					c.Say("reattached %s", s.Name)
					return nil
				}
				return rt.Exec(&Call{Ctx: c.Ctx, Out: c.Out, Args: map[string]string{"elements": eid}}, "session.open")
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
				if err := rt.waitLoggedIn(c.Ctx); err != nil {
					return fmt.Errorf("session.insert: %w", err)
				}
				return rt.InsertLine(c.Arg("text"))
			},
		},
		{
			Name: "session.send", Slow: true, Writes: true,
			Help: "type a line into a session once it's at its prompt, and wait for the answer",
			Long: "Type a line into a session once it's at its prompt, and wait for the answer. A line that isn't read-only (only dump, inspect, ping, dig and the other read-only verbs are) is listed instead of typed unless --doit is given.",
			Args: []Arg{{Name: "text", Required: true, Help: "The `line` to type"},
				{Name: "expect", Help: "The `text` that ends the answer (default: the session's prompt)"},
				{Name: "element", Help: "Element `ID` of the session to type into (default: the active session)"}},
			Run: func(rt *Runtime, c *Call) error {
				var writes []script.Write
				if line := strings.TrimSpace(c.Arg("text")); isWrite(line) {
					who, eid := rt.lineTarget(c)
					writes = []script.Write{{Method: "CLI", Path: eid, Body: line}}
					if !c.DoIt {
						c.Say("plan %s: %s", who, line)
						c.Result = SentLine{Planned: true, Writes: writes}
						return nil
					}
				}
				s, err := rt.sessionFor(c, "session.send")
				if err != nil {
					return err
				}
				if err := waitReady(c.Ctx, s); err != nil {
					return fmt.Errorf("session.send: %w", err)
				}
				text, expect := c.Arg("text"), c.Arg("expect")
				what := s.Name + "'s prompt never came back"
				if expect != "" {
					what = fmt.Sprintf("%q never showed up on %s", expect, s.Name)
				}
				done := rt.beginWait(Wait{What: s.Name + ": " + strings.TrimSpace(text), Since: time.Now()})
				defer done()
				x := newExchange(s.Buffer(), s.Name+"#", text, expect)
				s.SendText(text + "\r")
				out, err := awaitAnswer(c.Ctx, s, x)
				c.Say("%s", screenText(s.Render()))
				if err != nil {
					return fmt.Errorf("session.send: %s: %w", what, err)
				}
				c.Result = SentLine{Output: out, Writes: writes}
				return nil
			},
		},
	}
}

func screenText(screen string) string {
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func (rt *Runtime) ResetSessions() {
	rt.mu.Lock()
	rt.sess.mgr = ionterm.NewManager()
	rt.mu.Unlock()
}
