package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

const AreaBatch = "batch"

type Device struct {
	Login   func(st *config.Store, t config.Tenant, user string) (string, string)
	Options func(st *config.Store, t config.Tenant, user string, cmds []string) (sase.Options, error)
	Session func(c *api.Client) sase.Client
	Dialer  func(c *api.Client) ionterm.Dialer
	Writes  func() bool
}

func (rt *Runtime) UseDevice(d Device) {
	rt.mu.Lock()
	rt.dev = d
	rt.mu.Unlock()
}

func (rt *Runtime) RunAs(tsg string) string {
	rt.mu.RLock()
	u := rt.ws.runAs[tsg]
	rt.mu.RUnlock()
	if u != "" {
		return u
	}
	if t, ok := rt.TenantOf(tsg); ok {
		return t.DefaultUser()
	}
	return ""
}

func (rt *Runtime) SetRunAs(tsg, user string) {
	rt.editWorkspace(func(w *workspace) {
		if w.runAs == nil {
			w.runAs = map[string]string{}
		}
		if user == "" {
			delete(w.runAs, tsg)
			return
		}
		w.runAs[tsg] = user
	})
}

func (rt *Runtime) ForgetRunAs() {
	rt.editWorkspace(func(w *workspace) { w.runAs = nil })
}

func (rt *Runtime) TenantOf(tsg string) (config.Tenant, bool) {
	if cfg := rt.Config(); cfg != nil {
		for _, t := range cfg.Tenants {
			if t.TSGID == tsg {
				return t, true
			}
		}
	}
	if cur, ok := rt.Tenant(); ok && tsg != "" && cur.TSGID == tsg {
		return cur, true
	}
	return config.Tenant{}, false
}

func (rt *Runtime) Login(tsg string) (string, string) {
	rt.mu.RLock()
	login, st := rt.dev.Login, rt.store
	rt.mu.RUnlock()
	t, ok := rt.TenantOf(tsg)
	if !ok || login == nil {
		return "", ""
	}
	return login(st, t, rt.RunAs(tsg))
}

func (rt *Runtime) CheckCommands(cmds []string) error {
	rt.mu.RLock()
	flag := rt.dev.Writes
	rt.mu.RUnlock()
	if flag != nil && flag() {
		return nil
	}
	if v, _ := rt.Get("allow_writes"); v == "true" {
		return nil
	}
	return ioncli.CheckReadOnly(cmds)
}

func (rt *Runtime) BatchTargets(rows []Element) ([]sase.Target, error) {
	out := make([]sase.Target, 0, len(rows))
	for _, r := range rows {
		t := rt.TargetFor(r)
		if t.User == "" || t.Pass == "" {
			return nil, fmt.Errorf("no ION login for %s", r.Tenant)
		}
		out = append(out, t)
	}
	return out, nil
}

func (rt *Runtime) TargetFor(r Element) sase.Target {
	rt.mu.RLock()
	session := rt.dev.Session
	rt.mu.RUnlock()
	user, pass := rt.Login(r.TSG)
	t := sase.Target{ElementID: r.ID, User: user, Pass: pass}
	if c := rt.Clients().Get(r.TSG); c != nil {
		t.Client = c
		if session != nil {
			t.Client = session(c)
		}
	} else if session != nil {
		t.Client = session(nil)
	}
	return t
}

func (rt *Runtime) RunOptions(cmds []string, targets []sase.Target) (sase.Options, error) {
	rt.mu.RLock()
	opts, st := rt.dev.Options, rt.store
	rt.mu.RUnlock()
	t, _ := rt.Tenant()
	if opts == nil {
		return sase.Options{}, fmt.Errorf("no way to run on devices")
	}
	o, err := opts(st, t, rt.IONUser(), cmds)
	if err != nil {
		return o, err
	}
	if (o.Username == "" || o.Password == "") && len(targets) > 0 {
		o.Username, o.Password = targets[0].User, targets[0].Pass
	}
	if err := o.Validate(); err != nil {
		return o, err
	}
	return o, nil
}

type Inflight struct {
	Element string
	Started time.Time
}

type ErrGroup struct {
	Msg   string
	Count int
	First string
}

type batchState struct {
	cmds     []string
	ids      []string
	results  []sase.Result
	elapsed  []time.Duration
	finished int
	failed   int
	running  []Inflight
	errs     []ErrGroup
}

type Batch struct {
	Commands []string
	IDs      []string
	Results  []sase.Result
	Elapsed  []time.Duration
	Finished int
	Failed   int
	Running  []Inflight
	Errs     []ErrGroup
}

func (b Batch) Total() int { return len(b.IDs) }

func (rt *Runtime) Batch() Batch {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	b := rt.batch
	return Batch{
		Commands: append([]string(nil), b.cmds...),
		IDs:      append([]string(nil), b.ids...),
		Results:  append([]sase.Result(nil), b.results...),
		Elapsed:  append([]time.Duration(nil), b.elapsed...),
		Finished: b.finished, Failed: b.failed,
		Running: append([]Inflight(nil), b.running...),
		Errs:    append([]ErrGroup(nil), b.errs...),
	}
}

func (rt *Runtime) editBatch(edit func(b *batchState)) {
	rt.mu.Lock()
	edit(&rt.batch)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaBatch})
}

func (rt *Runtime) BeginBatch(cmds, ids []string) {
	rt.editBatch(func(b *batchState) {
		*b = batchState{cmds: cmds, ids: ids,
			results: make([]sase.Result, len(ids)), elapsed: make([]time.Duration, len(ids))}
	})
}

func (rt *Runtime) ApplyBatchEvent(ev sase.Event) {
	rt.editBatch(func(b *batchState) {
		if !ev.Done {
			b.running = append(b.running, Inflight{Element: ev.Element, Started: time.Now()})
			return
		}
		if ev.Index < len(b.results) {
			b.results[ev.Index] = ev.Result
			b.elapsed[ev.Index] = ev.Elapsed
		}
		b.finished++
		for i, f := range b.running {
			if f.Element == ev.Element {
				b.running = append(b.running[:i], b.running[i+1:]...)
				break
			}
		}
		if ev.Result.Error != "" {
			b.failed++
			key := NormalizeErr(ev.Result.Error)
			for i := range b.errs {
				if b.errs[i].Msg == key {
					b.errs[i].Count++
					return
				}
			}
			b.errs = append(b.errs, ErrGroup{Msg: key, Count: 1, First: ev.Element})
		}
	})
}

func NormalizeErr(s string) string {
	if strings.HasPrefix(s, "element ") {
		if i := strings.Index(s, ": "); i >= 0 {
			return s[i+2:]
		}
	}
	return s
}

func (rt *Runtime) knownRows(c *Call, ids []string) []Element {
	known := func() bool {
		have := map[string]bool{}
		for _, r := range rt.Elements() {
			have[r.ID] = true
		}
		for _, id := range ids {
			if !have[id] {
				return false
			}
		}
		return true
	}
	if !known() && len(rt.Elements()) == 0 {
		rt.LoadCache()
	}
	if !known() {
		rt.Exec(&Call{Ctx: c.Ctx, Out: c.Out}, "fleet.refresh")
	}
	return rt.rowsFor(ids)
}

func (rt *Runtime) fleetLoaded(c *Call) error {
	if len(rt.Elements()) > 0 {
		return nil
	}
	if n, _, _ := rt.LoadCache(); n > 0 {
		return nil
	}
	if err := rt.Exec(&Call{Ctx: c.Ctx, Out: c.Out}, "fleet.refresh"); err != nil && len(rt.Elements()) == 0 {
		return err
	}
	return nil
}

func (rt *Runtime) rowsFor(ids []string) []Element {
	if len(rt.Elements()) == 0 {
		rt.LoadCache()
	}
	byID := map[string]Element{}
	for _, r := range rt.Elements() {
		byID[r.ID] = r
	}
	t, _ := rt.Tenant()
	out := make([]Element, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, Element{Element: api.Element{ID: id}, TSG: t.TSGID, Tenant: t.Name})
	}
	return out
}

func batchCommands() []Command {
	return []Command{{
		Name: "batch.save", Help: "write the last run's results to a file: to=path, else a new file in dir= (default batch_dir) format= orient=",
		Args: []Arg{{Name: "to"}, {Name: "dir"}, {Name: "format"}, {Name: "orient"}},
		Run: func(rt *Runtime, c *Call) error {
			if rt.Batch().Total() == 0 {
				return fmt.Errorf("no run to save")
			}
			where, err := rt.SaveBatch(c.Arg("to"), c.Arg("dir"), c.Arg("format"), c.Arg("orient"))
			if err == nil {
				c.Say("saved to %s", where)
			}
			return err
		},
	}, {
		Name: "batch.run", Slow: true,
		Help: "run device CLI commands on elements: cmd=\"dump device status; show ip\" elements=ID,ID (default: the selection)",
		Args: []Arg{
			{Name: "cmd", Required: true, Help: "commands, separated by ;"},
			{Name: "elements", Help: "element IDs, comma separated; the selection, else the row under the cursor, when empty"},
		},
		Run: func(rt *Runtime, c *Call) error {
			cmds := splitCommands(c.Arg("cmd"))
			if err := rt.CheckCommands(cmds); err != nil {
				return err
			}
			rows := rt.Targets()
			if ids := c.Arg("elements"); ids != "" {
				var list []string
				for _, id := range strings.Split(ids, ",") {
					if id = strings.TrimSpace(id); id != "" {
						list = append(list, id)
					}
				}
				rows = rt.rowsFor(list)
			}
			if len(rows) == 0 {
				return fmt.Errorf("batch.run: no elements")
			}
			return rt.runBatch(c, cmds, rows)
		},
	}}
}

func (rt *Runtime) StartBatch(ctx context.Context, cmds []string, rows []Element) (<-chan sase.Event, error) {
	if err := rt.CheckCommands(cmds); err != nil {
		return nil, err
	}
	for _, tsg := range tenantsOf(rows) {
		if rt.Clients().Get(tsg) != nil {
			continue
		}
		t, ok := rt.TenantOf(tsg)
		if !ok {
			continue
		}
		if _, err := rt.Connect(ctx, t); err != nil {
			return nil, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
		}
	}
	targets, err := rt.BatchTargets(rows)
	if err != nil {
		return nil, err
	}
	opts, err := rt.RunOptions(cmds, targets)
	if err != nil {
		return nil, err
	}
	return rt.RunBatch(ctx, opts, targets), nil
}

func (rt *Runtime) RunBatch(ctx context.Context, opts sase.Options, targets []sase.Target) <-chan sase.Event {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ElementID
	}
	rt.BeginBatch(opts.Commands, ids)
	out := make(chan sase.Event)
	go func() {
		defer close(out)
		defer rt.EndBatch()
		for ev := range sase.RunStream(ctx, opts, targets) {
			rt.ApplyBatchEvent(ev)
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}
	}()
	return out
}

func (rt *Runtime) runBatch(c *Call, cmds []string, rows []Element) error {
	events, err := rt.StartBatch(c.Ctx, cmds, rows)
	if err != nil {
		return err
	}
	for ev := range events {
		if !ev.Done {
			continue
		}
		if ev.Result.Error != "" {
			c.Say("%s: %s", ev.Element, ev.Result.Error)
			continue
		}
		for _, out := range ev.Result.Commands {
			c.Say("== %s · %s", ev.Element, out.Command)
			c.Say("%s", strings.TrimRight(out.Output, "\n"))
		}
	}
	b := rt.Batch()
	c.Result = b
	c.Say("%d of %d elements done, %d failed", b.Finished, b.Total(), b.Failed)
	if b.Failed > 0 {
		return fmt.Errorf("%d of %d elements failed", b.Failed, b.Total())
	}
	return nil
}

func (rt *Runtime) SaveBatch(path, dir, format, orient string) (string, error) {
	if format == "" {
		format, _ = rt.Get("batch_format")
	}
	if orient == "" {
		orient, _ = rt.Get("batch_orient")
	}
	if path == "" && dir == "" {
		if cfg := rt.Config(); cfg != nil {
			dir = cfg.BatchOutDir()
		}
	}
	if path == "" && dir == "" {
		return "", fmt.Errorf("nowhere to save to: set batch_dir, or give to=")
	}
	out, where, err := output.Open(path, dir, format, time.Now())
	if err != nil {
		return "", err
	}
	defer out.Close()
	sink := output.NewSink(out, format, output.WithOrientation(orient))
	for i, r := range rt.Batch().Results {
		if r.ElementID == "" {
			continue
		}
		if err := sink.Add(i, r); err != nil {
			return "", err
		}
	}
	if err := sink.Close(); err != nil {
		return "", err
	}
	return where, out.Close()
}

func tenantsOf(rows []Element) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !seen[r.TSG] {
			seen[r.TSG] = true
			out = append(out, r.TSG)
		}
	}
	return out
}

func (rt *Runtime) LoadBatch(cmds []string, results []sase.Result, elapsed []time.Duration) {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ElementID
	}
	rt.editBatch(func(b *batchState) {
		*b = batchState{cmds: cmds, ids: ids, results: results, elapsed: elapsed, finished: len(results)}
		for _, r := range results {
			if r.Error != "" {
				b.failed++
			}
		}
	})
}

func (rt *Runtime) EndBatch() {
	rt.editBatch(func(b *batchState) { b.running = nil })
}

func (rt *Runtime) PickedRunAs(tsg string) string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.ws.runAs[tsg]
}
