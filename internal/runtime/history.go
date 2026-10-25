package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ioncmd"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase"
)

const AreaHistory = "history"

const WarnMacroModel = "macro/model"

type history struct {
	saved  *config.CommandStore
	from   *config.Store
	err    string
	undone []config.Macro
}

func (rt *Runtime) savedLocked() *config.CommandStore {
	if rt.hist.saved != nil && rt.hist.from == rt.store {
		return rt.hist.saved
	}
	rt.hist = history{saved: &config.CommandStore{}, from: rt.store}
	if rt.store == nil {
		return rt.hist.saved
	}
	c, err := rt.store.Commands()
	if err != nil {
		rt.hist.err = err.Error()
		return rt.hist.saved
	}
	rt.hist.saved = c
	return c
}

func (rt *Runtime) readSaved(read func(c *config.CommandStore)) {
	rt.mu.Lock()
	read(rt.savedLocked())
	rt.mu.Unlock()
}

func (rt *Runtime) editSaved(edit func(c *config.CommandStore) error) error {
	rt.mu.Lock()
	err := edit(rt.savedLocked())
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaHistory})
	return err
}

func (rt *Runtime) SaveCommands() error {
	rt.mu.Lock()
	st, c := rt.store, *rt.savedLocked()
	c.History = slices.Clone(c.History)
	c.Favourites = slices.Clone(c.Favourites)
	c.Macros = slices.Clone(c.Macros)
	c.Acknowledged = slices.Clone(c.Acknowledged)
	rt.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.SaveCommands(&c)
}

func (rt *Runtime) HistoryErr() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.savedLocked()
	return rt.hist.err
}

func (rt *Runtime) RecordUse(model string, cmds ...string) {
	now := time.Now()
	rt.editSaved(func(c *config.CommandStore) error {
		for _, cmd := range cmds {
			c.Record(cmd, model, now)
		}
		return nil
	})
}

func (rt *Runtime) History() []config.CommandUse {
	var out []config.CommandUse
	rt.readSaved(func(c *config.CommandStore) { out = slices.Clone(c.History) })
	return out
}

func (rt *Runtime) Ranked(model, q string) []config.CommandUse {
	var ranked []config.CommandUse
	rt.readSaved(func(c *config.CommandStore) { ranked = c.Ranked(model) })
	if q = strings.TrimSpace(q); q == "" {
		return ranked
	}
	cmds := make([]string, 0, len(ranked))
	for _, u := range ranked {
		cmds = append(cmds, u.Command)
	}
	keep := map[string]bool{}
	for _, hit := range ioncmd.Fuzzy(cmds, q) {
		keep[hit] = true
	}
	var out []config.CommandUse
	for _, u := range ranked {
		if keep[u.Command] {
			out = append(out, u)
		}
	}
	return out
}

func (rt *Runtime) ForgetUse(command, model string) bool {
	gone := false
	rt.editSaved(func(c *config.CommandStore) error { gone = c.Forget(command, model); return nil })
	return gone
}

func (rt *Runtime) Favourite(slot int) (config.Favourite, bool) {
	var f config.Favourite
	ok := false
	rt.readSaved(func(c *config.CommandStore) { f, ok = c.Favourite(slot) })
	return f, ok
}

func (rt *Runtime) Favourites() []config.Favourite {
	var out []config.Favourite
	rt.readSaved(func(c *config.CommandStore) { out = c.FavouriteList() })
	return out
}

func (rt *Runtime) SetFavourite(slot int, command, model string) error {
	return rt.editSaved(func(c *config.CommandStore) error { return c.SetFavourite(slot, command, model) })
}

func (rt *Runtime) ClearFavourite(slot int) bool {
	gone := false
	rt.editSaved(func(c *config.CommandStore) error { gone = c.ClearFavourite(slot); return nil })
	return gone
}

func (rt *Runtime) Macros() []config.Macro {
	var out []config.Macro
	rt.readSaved(func(c *config.CommandStore) { out = slices.Clone(c.Macros) })
	return out
}

func (rt *Runtime) Macro(name string) (config.Macro, bool) {
	var m config.Macro
	ok := false
	rt.readSaved(func(c *config.CommandStore) { m, ok = c.Macro(name) })
	return m, ok
}

func (rt *Runtime) SetMacro(m config.Macro) error {
	return rt.editSaved(func(c *config.CommandStore) error { return c.SetMacro(m) })
}

func (rt *Runtime) RemoveMacro(name string) bool {
	gone := false
	rt.editSaved(func(c *config.CommandStore) error {
		m, ok := c.Macro(name)
		if gone = ok && c.RemoveMacro(name); gone {
			rt.hist.undone = append(rt.hist.undone, m)
		}
		return nil
	})
	return gone
}

func (rt *Runtime) UndoMacro() (config.Macro, int, error) {
	var m config.Macro
	left := 0
	err := rt.editSaved(func(c *config.CommandStore) error {
		n := len(rt.hist.undone)
		if n == 0 {
			return fmt.Errorf("nothing to undo")
		}
		m = rt.hist.undone[n-1]
		if err := c.SetMacro(m); err != nil {
			return err
		}
		rt.hist.undone = rt.hist.undone[:n-1]
		left = n - 1
		return nil
	})
	return m, left, err
}

func (rt *Runtime) Ack(warning string) {
	rt.editSaved(func(c *config.CommandStore) error { c.Ack(warning); return nil })
}

func (rt *Runtime) Acked(warning string) bool {
	ok := false
	rt.readSaved(func(c *config.CommandStore) { ok = c.Acked(warning) })
	return ok
}

func (rt *Runtime) ModelOf(tsg, eid string) string {
	for _, el := range rt.Elements() {
		if el.TSG == tsg && el.ID == eid {
			return el.Model
		}
	}
	return ""
}

func (rt *Runtime) SessionModel() string {
	s := rt.Sessions().Active()
	if s == nil {
		return ""
	}
	return rt.modelOfSession(s)
}

func (rt *Runtime) modelOfSession(s *ionterm.Session) string {
	for _, el := range rt.Elements() {
		if el.ID == s.ElementID {
			return el.Model
		}
	}
	return ""
}

func (rt *Runtime) RecordLine(s *ionterm.Session) bool {
	line := s.Line()
	if line == "" {
		return false
	}
	rt.RecordUse(rt.modelOfSession(s), line)
	return true
}

func MacroMismatch(m config.Macro, rows []Element) string {
	if m.Model == "" {
		return ""
	}
	for _, r := range rows {
		if r.Model != "" && r.Model != m.Model {
			return fmt.Sprintf("built on %s, selection has %s", m.Model, r.Model)
		}
	}
	return ""
}

func (rt *Runtime) UseFavourite(slot int) (config.Favourite, string, error) {
	f, ok := rt.Favourite(slot)
	if !ok {
		return f, "", fmt.Errorf("favourite %d is empty", slot)
	}
	differs := ""
	if model := rt.SessionModel(); model != "" && f.Model != "" && f.Model != model {
		differs = fmt.Sprintf("saved on %s and this is %s", f.Model, model)
	}
	return f, differs, rt.InsertLine(f.Command)
}

func SplitList(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func SplitCommands(raw ...string) []string {
	var out []string
	for _, line := range raw {
		out = append(out, SplitList(line, ",")...)
	}
	return out
}

func (rt *Runtime) SelectionModel() string {
	model := ""
	for _, r := range rt.Selected() {
		switch {
		case r.Model == "":
			continue
		case model == "":
			model = r.Model
		case model != r.Model:
			return ""
		}
	}
	return model
}

func historyCommands() []Command {
	slot := func(c *Call) (int, error) {
		n, err := strconv.Atoi(c.Arg("slot"))
		if err != nil || n < 1 || n > config.FavouriteSlots {
			return 0, fmt.Errorf("slot is a number from 1 to %d", config.FavouriteSlots)
		}
		return n, nil
	}
	return []Command{
		{
			Name: "history", Help: "the device commands typed, most used first: model= puts that model's first, q= narrows",
			Args: []Arg{{Name: "model"}, {Name: "q"}},
			Run: func(rt *Runtime, c *Call) error {
				for _, u := range rt.Ranked(c.Arg("model"), c.Arg("q")) {
					line := fmt.Sprintf("%4d  %s", u.Uses, u.Command)
					if u.Model != "" {
						line += "  · " + u.Model
					}
					c.Say("%s", line)
				}
				return nil
			},
		},
		{
			Name: "history.forget", Help: "drop a command from the history: command= model=",
			Args: []Arg{{Name: "command", Required: true}, {Name: "model"}},
			Run: func(rt *Runtime, c *Call) error {
				if !rt.ForgetUse(c.Arg("command"), c.Arg("model")) {
					return fmt.Errorf("%q is not in the history", c.Arg("command"))
				}
				return rt.SaveCommands()
			},
		},
		{
			Name: "fav.list", Help: "the favourite device commands, by slot",
			Run: func(rt *Runtime, c *Call) error {
				for _, f := range rt.Favourites() {
					line := fmt.Sprintf("%d  %s", f.Slot, f.Command)
					if f.Model != "" {
						line += "  · " + f.Model
					}
					c.Say("%s", line)
				}
				return nil
			},
		},
		{
			Name: "fav.set", Help: "save a device command in a favourite slot: slot=1-9 command= model=",
			Args: []Arg{{Name: "slot", Required: true}, {Name: "command", Required: true}, {Name: "model"}},
			Run: func(rt *Runtime, c *Call) error {
				n, err := slot(c)
				if err != nil {
					return err
				}
				model := c.Arg("model")
				if model == "" {
					model = rt.SessionModel()
				}
				if err := rt.SetFavourite(n, c.Arg("command"), model); err != nil {
					return err
				}
				return rt.SaveCommands()
			},
		},
		{
			Name: "fav.clear", Help: "empty a favourite slot: slot=1-9",
			Args: []Arg{{Name: "slot", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				n, err := slot(c)
				if err != nil {
					return err
				}
				if !rt.ClearFavourite(n) {
					return fmt.Errorf("favourite %d is already empty", n)
				}
				return rt.SaveCommands()
			},
		},
		{
			Name: "fav.use", Slow: true, Help: "put a favourite on the active session's line, unsent: slot=1-9",
			Args: []Arg{{Name: "slot", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				n, err := slot(c)
				if err != nil {
					return err
				}
				rt.waitLoggedIn(c.Ctx)
				_, differs, err := rt.UseFavourite(n)
				if differs != "" {
					c.Say("favourite %d was %s — check it before sending", n, differs)
				}
				return err
			},
		},
		{
			Name: "macro.list", Help: "the saved macros and their commands",
			Run: func(rt *Runtime, c *Call) error {
				for _, m := range rt.Macros() {
					line := m.Name + ": " + strings.Join(m.Commands, ", ")
					if m.Model != "" {
						line += "  · " + m.Model
					}
					c.Say("%s", line)
				}
				return nil
			},
		},
		{
			Name: "macro.set", Help: "save a macro: name= commands=\"a, b\", or each command quoted after the name; model= was= its old name, to rename it",
			Args: []Arg{{Name: "name"}, {Name: "commands"},
				{Name: "model", Help: "the hardware it is for; with none, the selection's when the selection is one model"},
				{Name: "was", Help: "the name it had, when renaming"}},
			Run: func(rt *Runtime, c *Call) error {
				model := c.Arg("model")
				if model == "" {
					model = rt.SelectionModel()
				}
				cmds := SplitCommands(c.Arg("commands"))
				for _, w := range c.Rest {
					if w = strings.TrimSpace(w); w != "" {
						cmds = append(cmds, w)
					}
				}
				m := config.Macro{Name: c.Arg("name"), Commands: cmds, Model: model}
				if err := rt.SetMacro(m); err != nil {
					return err
				}
				if was := c.Arg("was"); was != "" && !strings.EqualFold(was, m.Name) {
					rt.RemoveMacro(was)
				}
				c.Result = m
				c.Say("saved %s, %d command(s)", m.Name, len(m.Commands))
				return rt.SaveCommands()
			},
		},
		{
			Name: "macro.remove", Help: "delete a macro: name=; macro.undo puts it back",
			Args: []Arg{{Name: "name", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				if !rt.RemoveMacro(c.Arg("name")) {
					return fmt.Errorf("no macro is called %q", c.Arg("name"))
				}
				return rt.SaveCommands()
			},
		},
		{
			Name: "macro.undo", Help: "put back the macro deleted last",
			Run: func(rt *Runtime, c *Call) error {
				m, _, err := rt.UndoMacro()
				if err != nil {
					return err
				}
				c.Say("%s is back", m.Name)
				return rt.SaveCommands()
			},
		},
		{
			Name: "macro.run", Slow: true, Help: "run a macro's commands on elements: name= elements=ID,ID (default: the selection) anyway=yes runs one built on other hardware",
			Args: []Arg{{Name: "name", Required: true}, {Name: "elements"}, {Name: "anyway", Bool: true, Help: "run one built on other hardware"}},
			Run: func(rt *Runtime, c *Call) error {
				rows := rt.ElementsArg(c, "elements")
				events, err := rt.StartMacro(c.Ctx, c.Arg("name"), rows, c.Arg("anyway") == "yes")
				var mm *MacroMismatchError
				if errors.As(err, &mm) {
					return fmt.Errorf("%w, anyway=yes runs it (and is not asked again)", err)
				}
				if err != nil {
					return err
				}
				return rt.sayBatch(c, events)
			},
		},
	}
}

type MacroMismatchError struct {
	Macro, Why string
}

func (e *MacroMismatchError) Error() string {
	return fmt.Sprintf("%s was %s; command syntax differs by model and version", e.Macro, e.Why)
}

func (rt *Runtime) StartMacro(ctx context.Context, name string, rows []Element, anyway bool) (<-chan sase.Event, error) {
	m, ok := rt.Macro(name)
	if !ok {
		return nil, fmt.Errorf("no macro is called %q", name)
	}
	if err := rt.CheckCommands(m.Commands); err != nil {
		return nil, fmt.Errorf("%s: %w", m.Name, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("nothing selected — pick elements, then run the macro")
	}
	if why := MacroMismatch(m, rows); why != "" && !rt.Acked(WarnMacroModel) {
		if !anyway {
			return nil, &MacroMismatchError{Macro: m.Name, Why: why}
		}
		rt.Ack(WarnMacroModel)
		if err := rt.SaveCommands(); err != nil {
			return nil, err
		}
	}
	return rt.StartBatch(ctx, m.Commands, rows)
}
