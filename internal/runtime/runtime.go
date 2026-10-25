package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/sase/write"
)

type Command struct {
	Name      string
	Help      string
	Long      string
	Source    string
	Args      []Arg
	Writes    bool
	Each      string
	Slow      bool
	FrontOnly bool
	Run       func(rt *Runtime, c *Call) error
}

type Arg struct {
	Name     string
	Help     string
	Required bool
	Default  string
	Bool     bool
}

type Call struct {
	Ctx  context.Context
	Args map[string]string
	Rest []string
	DoIt bool
	Out  func(line string)

	Result any
}

func (c *Call) Arg(name string) string { return c.Args[name] }

func (c *Call) Say(format string, a ...any) {
	if c.Out != nil {
		c.Out(fmt.Sprintf(format, a...))
	}
}

type Event struct {
	Area   string
	Status string
	Rows   bool
	Keys   []string
}

const (
	AreaList   = "list"
	AreaStatus = "status"
)

func (rt *Runtime) Warn(problem string) {
	rt.mu.Lock()
	rt.warnings = append(rt.warnings, problem)
	rt.mu.Unlock()
}

func (rt *Runtime) Warnings() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return append([]string(nil), rt.warnings...)
}

type Runtime struct {
	warnings []string

	mu   sync.RWMutex
	cmds map[string]Command
	opts map[string]Option

	store      *config.Store
	cfg        *config.Config
	portal     PortalFunc
	clients    *Clients
	connecting sync.Map
	conn       Connector
	dev        Device
	listCalls  *endpoint.Set
	writes     *write.Set
	writesErr  string

	resultsFilter string

	preferAdded bool

	list   listState
	ws     workspace
	fleet  fleetState
	ha     haState
	cells  cellsState
	batch  batchState
	sess   sessionsState
	data   dataState
	hist   history
	auth   authState
	status string

	subMu   sync.Mutex
	subs    map[int]chan Event
	nextS   int
	changes chan Event

	sweepMu      sync.Mutex
	sweeps       context.Context
	stopSweeps   context.CancelFunc
	sweepWG      *sync.WaitGroup
	sweepGen     int
	sweepsClosed bool
}

func New() *Runtime {
	rt := &Runtime{cmds: map[string]Command{}, opts: map[string]Option{}, subs: map[int]chan Event{}, changes: make(chan Event, 1024)}
	rt.subs[-1] = rt.changes
	for _, c := range concat(listCommands(), optionCommands(), fleetCommands(), haCommands(), batchCommands(), sessionCommands(), dataCommands(), historyCommands(), tenantCommands(), inspectCommands(), tabCommands(), resultCommands()) {
		rt.Register(c)
	}
	for _, o := range builtinOptions() {
		rt.RegisterOption(o)
	}
	if list, err := column.Builtin(); err == nil {
		rt.listCalls = list.Calls()
	}
	return rt
}

func (rt *Runtime) Register(c Command) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.cmds[c.Name] = c
}

func (rt *Runtime) Command(name string) (Command, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	c, ok := rt.cmds[name]
	return c, ok
}

func (rt *Runtime) Commands() []Command {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]Command, 0, len(rt.cmds))
	for _, c := range rt.cmds {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (rt *Runtime) Exec(c *Call, name string) error {
	cmd, ok := rt.Command(name)
	if !ok {
		return fmt.Errorf("no command is called %q", name)
	}
	if c.Ctx == nil {
		c.Ctx = context.Background()
	}
	args, err := cmd.check(c.Args)
	if err != nil {
		return err
	}
	c.Args = args
	if c.DoIt && !cmd.Writes {
		c.DoIt = false
	}
	return cmd.Run(rt, c)
}

func (rt *Runtime) CheckArgs(name string, given map[string]string) error {
	cmd, ok := rt.Command(name)
	if !ok {
		return fmt.Errorf("no command is called %q", name)
	}
	_, err := cmd.check(given)
	return err
}

func (cmd Command) check(given map[string]string) (map[string]string, error) {
	args := map[string]string{}
	known := map[string]bool{}
	var bad []string
	each := cmd.Each != "" && given[cmd.Each] != ""
	for _, a := range cmd.Args {
		known[a.Name] = true
		v, ok := given[a.Name]
		if !ok || v == "" {
			v = a.Default
		}
		if v == "" && a.Required && !each {
			bad = append(bad, a.Name+" is required")
			continue
		}
		if v != "" {
			args[a.Name] = v
		}
	}
	for k := range given {
		if !known[k] {
			bad = append(bad, k+" is not an argument of "+cmd.Name)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%s: %s", cmd.Name, strings.Join(bad, "; "))
	}
	return args, nil
}

func (rt *Runtime) Run(name string, args map[string]string, rest ...string) error {
	return rt.Exec(&Call{Args: args, Rest: rest}, name)
}

func (rt *Runtime) Subscribe() (<-chan Event, func()) {
	rt.subMu.Lock()
	defer rt.subMu.Unlock()
	id := rt.nextS
	rt.nextS++
	ch := make(chan Event, 64)
	rt.subs[id] = ch
	return ch, func() {
		rt.subMu.Lock()
		defer rt.subMu.Unlock()
		if c, ok := rt.subs[id]; ok {
			delete(rt.subs, id)
			close(c)
		}
	}
}

func (rt *Runtime) Changes() <-chan Event { return rt.changes }

func (rt *Runtime) Close() {
	rt.sweepMu.Lock()
	rt.sweepsClosed = true
	rt.sweepMu.Unlock()
	rt.StopSweeps()
	rt.subMu.Lock()
	defer rt.subMu.Unlock()
	if ch, ok := rt.subs[-1]; ok {
		delete(rt.subs, -1)
		close(ch)
	}
}

func (rt *Runtime) changed(e Event) {
	rt.subMu.Lock()
	defer rt.subMu.Unlock()
	for _, ch := range rt.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (rt *Runtime) Store() *config.Store {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.store
}

func (rt *Runtime) Config() *config.Config {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cfg
}

func (rt *Runtime) Status() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.status
}

func (rt *Runtime) SetStatus(s string) {
	rt.mu.Lock()
	rt.status = s
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaStatus, Status: s})
}

func (rt *Runtime) Forget(prefix string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for name := range rt.cmds {
		if strings.HasPrefix(name, prefix) {
			delete(rt.cmds, name)
		}
	}
}

func (rt *Runtime) ClearWarnings() {
	rt.mu.Lock()
	rt.warnings = nil
	rt.mu.Unlock()
}

func (rt *Runtime) CommandsNamed(prefix string) []Command {
	var out []Command
	for _, c := range rt.Commands() {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func concat(groups ...[]Command) []Command {
	var out []Command
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}
