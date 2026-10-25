package runtime

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/search"
)

type Element struct {
	api.Element
	TSG    string
	Tenant string
}

func (e Element) Key() string { return e.TSG + "/" + e.ID }

func (e Element) Label() string {
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}

const (
	ModeShell = "shell"
	ModeBatch = "batch"
	ModeData  = "rich data"
	ModeMacro = "macro"
)

var modes = []string{ModeShell, ModeBatch, ModeData}

type listState struct {
	rows     []Element
	sites    search.SiteMap
	query    string
	matches  []int
	cursor   int
	selected map[string]bool
	showSel  bool
	mode     string
	armed    string

	cleared     map[string]bool
	clearedView bool
}

func (rt *Runtime) SetElements(rows []Element) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].TSG < rows[j].TSG
	})
	rt.mu.Lock()
	l := &rt.list
	l.rows = rows
	if l.selected == nil {
		l.selected = map[string]bool{}
	}
	present := make(map[string]bool, len(rows))
	for _, r := range rows {
		present[r.Key()] = true
	}
	for k := range l.selected {
		if !present[k] {
			delete(l.selected, k)
		}
	}
	l.refilter()
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaList, Rows: true})
}

func (rt *Runtime) Elements() []Element {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.rows
}

func (rt *Runtime) Sites() search.SiteMap {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.sites
}

func (rt *Runtime) MergeSites(names map[string]string) {
	if len(names) == 0 {
		return
	}
	rt.mu.Lock()
	l := &rt.list
	next := make(search.SiteMap, len(l.sites)+len(names))
	for k, v := range l.sites {
		next[k] = v
	}
	for k, v := range names {
		next[k] = v
	}
	l.sites = next
	l.refilter()
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaList, Rows: true})
}

func (rt *Runtime) ClearSites() {
	rt.mu.Lock()
	rt.list.sites = nil
	rt.list.refilter()
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaList, Rows: true})
}

func (l *listState) refilter() {
	q := search.Parse(l.query)
	l.matches = make([]int, 0, len(l.rows))
	for i, el := range l.rows {
		if l.showSel && !l.selected[el.Key()] {
			continue
		}
		if q.Match(search.Of(el.Element, el.TSG, el.Tenant, l.sites)) {
			l.matches = append(l.matches, i)
		}
	}
	l.clamp()
}

func (l *listState) clamp() {
	l.cursor = min(l.cursor, len(l.matches)-1)
	l.cursor = max(l.cursor, 0)
}

type List struct {
	Rows         []Element
	Query        string
	Cursor       int
	Selected     map[string]bool
	ShowSelected bool
	Mode         string
	Total        int
}

func (rt *Runtime) List() List {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	l := rt.list
	out := List{
		Rows:         make([]Element, 0, len(l.matches)),
		Query:        l.query,
		Cursor:       l.cursor,
		Selected:     make(map[string]bool, len(l.selected)),
		ShowSelected: l.showSel,
		Mode:         l.modeOrDefault(rt.cfg),
		Total:        len(l.rows),
	}
	for _, i := range l.matches {
		out.Rows = append(out.Rows, l.rows[i])
	}
	for k := range l.selected {
		out.Selected[k] = true
	}
	return out
}

func (rt *Runtime) Current() (Element, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	l := rt.list
	if l.cursor < 0 || l.cursor >= len(l.matches) {
		return Element{}, false
	}
	return l.rows[l.matches[l.cursor]], true
}

func (rt *Runtime) Selected() []Element {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out []Element
	for _, r := range rt.list.rows {
		if rt.list.selected[r.Key()] {
			out = append(out, r)
		}
	}
	return out
}

func (rt *Runtime) Targets() []Element {
	if out := rt.Selected(); len(out) > 0 {
		return out
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	l := rt.list
	if l.cursor >= 0 && l.cursor < len(l.matches) {
		return []Element{l.rows[l.matches[l.cursor]]}
	}
	return nil
}

func (l listState) modeOrDefault(c *config.Config) string {
	if l.mode == "" {
		return defaultMode(c)
	}
	return l.mode
}

func defaultMode(c *config.Config) string {
	if c != nil && slices.Contains(modes, c.DefaultMode) {
		return c.DefaultMode
	}
	return ModeShell
}

func (rt *Runtime) editList(edit func(l *listState) (status string, set bool)) {
	rt.mu.Lock()
	if rt.list.selected == nil {
		rt.list.selected = map[string]bool{}
	}
	status, set := edit(&rt.list)
	if set {
		rt.status = status
	}
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaList, Status: status})
}

func listCommands() []Command {
	return []Command{
		{
			Name: "list.filter", Help: "narrow the list to rows matching a query, as --search does",
			Args: []Arg{{Name: "query", Help: "words that must all match; empty shows everything"}},
			Run: func(rt *Runtime, c *Call) error {
				q := c.Arg("query")
				if q == "" {
					q = strings.Join(c.Rest, " ")
				}
				rt.editList(func(l *listState) (string, bool) {
					l.query = q
					l.refilter()
					return "", false
				})
				return nil
			},
		},
		{
			Name: "list.cursor", Help: "move the cursor: down, up, top, bottom, or a signed count like +10",
			Args: []Arg{{Name: "to", Default: "down"}},
			Run: func(rt *Runtime, c *Call) error {
				to := c.Arg("to")
				if len(c.Rest) > 0 {
					to = c.Rest[0]
				}
				var bad error
				rt.editList(func(l *listState) (string, bool) {
					switch to {
					case "down":
						l.cursor++
					case "up":
						l.cursor--
					case "top":
						l.cursor = 0
					case "bottom":
						l.cursor = len(l.matches) - 1
					default:
						n, err := strconv.Atoi(to)
						if err != nil {
							bad = fmt.Errorf("list.cursor: %q is not down, up, top, bottom or a count", to)
							return "", false
						}
						l.cursor += n
					}
					l.clamp()
					return "", false
				})
				return bad
			},
		},
		{
			Name: "list.toggle", Help: "select the row under the cursor, or unselect it",
			Run: func(rt *Runtime, c *Call) error {
				rt.editList(func(l *listState) (string, bool) {
					if l.cursor < 0 || l.cursor >= len(l.matches) {
						return "", false
					}
					k := l.rows[l.matches[l.cursor]].Key()
					if l.selected[k] {
						delete(l.selected, k)
					} else {
						l.selected[k] = true
					}
					return "", false
				})
				return nil
			},
		},
		{
			Name: "list.select-matches", Help: "select every row the filter shows, or unselect them all when they already are",
			Run: func(rt *Runtime, c *Call) error {
				rt.editList(func(l *listState) (string, bool) {
					all := true
					for _, i := range l.matches {
						if !l.selected[l.rows[i].Key()] {
							all = false
							break
						}
					}
					for _, i := range l.matches {
						if all {
							delete(l.selected, l.rows[i].Key())
						} else {
							l.selected[l.rows[i].Key()] = true
						}
					}
					return "", false
				})
				return nil
			},
		},
		{
			Name: "list.show-selected", Help: "show only the selected rows, or everything again",
			Run: func(rt *Runtime, c *Call) error {
				rt.editList(func(l *listState) (string, bool) {
					if !l.showSel && len(l.selected) == 0 {
						return "nothing selected", true
					}
					l.showSel = !l.showSel
					l.refilter()
					l.cursor = 0
					return "", true
				})
				return nil
			},
		},
		{
			Name: "list.clear", Help: "unselect everything; list.undo-clear puts it back",
			Run: func(rt *Runtime, c *Call) error {
				rt.editList(func(l *listState) (string, bool) {
					if len(l.selected) == 0 {
						return "nothing selected", true
					}
					n := len(l.selected)
					l.cleared = l.selected
					l.clearedView = l.showSel
					l.selected = map[string]bool{}
					l.showSel = false
					l.refilter()
					return fmt.Sprintf("cleared %d selected — alt+z to undo", n), true
				})
				return nil
			},
		},
		{
			Name: "list.undo-clear", Help: "put back what the last list.clear unselected",
			Run: func(rt *Runtime, c *Call) error {
				rt.editList(func(l *listState) (string, bool) {
					if len(l.cleared) == 0 {
						return "nothing to undo", true
					}
					for k := range l.cleared {
						l.selected[k] = true
					}
					n := len(l.cleared)
					l.cleared = nil
					l.showSel = l.clearedView
					l.refilter()
					return fmt.Sprintf("restored %d selected", n), true
				})
				return nil
			},
		},
		{
			Name: "list.mode", Help: "what opening the selection does: shell, batch or rich data; with no mode, the next one",
			Args: []Arg{{Name: "mode"}},
			Run: func(rt *Runtime, c *Call) error {
				want := c.Arg("mode")
				if want == "" && len(c.Rest) > 0 {
					want = strings.Join(c.Rest, " ")
				}
				var bad error
				rt.editList(func(l *listState) (string, bool) {
					if want == "" {
						cur, next := l.modeOrDefault(rt.cfg), ModeShell
						for i, m := range modes {
							if m == cur {
								next = modes[(i+1)%len(modes)]
							}
						}
						l.mode, l.armed = next, ""
						return "", true
					}
					for _, m := range modes {
						if m == want {
							l.mode, l.armed = m, ""
							return "", true
						}
					}
					bad = fmt.Errorf("list.mode: %q is not one of %s", want, strings.Join(modes, ", "))
					return "", false
				})
				return bad
			},
		},
	}
}

func (rt *Runtime) MatchIndices() []int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.matches
}

func (rt *Runtime) Query() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.query
}

func (rt *Runtime) IsSelected(key string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.selected[key]
}

func (rt *Runtime) SelectedCount() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.list.selected)
}

func (rt *Runtime) ShowingSelected() bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.showSel
}

func (rt *Runtime) CanUndoClear() bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.list.cleared) > 0
}

func (rt *Runtime) Select(keys ...string) {
	rt.editList(func(l *listState) (string, bool) {
		for _, k := range keys {
			l.selected[k] = true
		}
		if l.showSel {
			l.refilter()
		}
		return "", false
	})
}

func (rt *Runtime) ToggleKey(key string) {
	rt.editList(func(l *listState) (string, bool) {
		if l.selected[key] {
			delete(l.selected, key)
		} else {
			l.selected[key] = true
		}
		return "", false
	})
}

func (rt *Runtime) ForgetSelection() {
	rt.editList(func(l *listState) (string, bool) {
		l.selected = map[string]bool{}
		l.cleared, l.showSel = nil, false
		l.refilter()
		return "", false
	})
}

func (rt *Runtime) SetShowSelected(on bool) {
	rt.editList(func(l *listState) (string, bool) {
		l.showSel = on
		l.refilter()
		return "", false
	})
}

func (rt *Runtime) WouldRestore(key string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.cleared[key]
}

func (rt *Runtime) Selection() map[string]bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make(map[string]bool, len(rt.list.selected))
	for k := range rt.list.selected {
		out[k] = true
	}
	return out
}

func (rt *Runtime) Cursor() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.cursor
}

func (rt *Runtime) SetCursor(n int) {
	rt.editList(func(l *listState) (string, bool) {
		l.cursor = n
		l.clamp()
		return "", false
	})
}

func (rt *Runtime) Mode() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.modeOrDefault(rt.cfg)
}

func (rt *Runtime) SetMode(mode string) {
	rt.editList(func(l *listState) (string, bool) {
		l.mode, l.armed = mode, ""
		return "", false
	})
}

func (rt *Runtime) ArmMacro(name string) {
	rt.editList(func(l *listState) (string, bool) {
		l.mode, l.armed = ModeMacro, name
		return "", false
	})
}

func (rt *Runtime) Disarm() {
	rt.editList(func(l *listState) (string, bool) {
		l.mode, l.armed = "", ""
		return "", false
	})
}

func (rt *Runtime) Armed() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.list.armed
}

func (rt *Runtime) Row(eid string) (Element, bool) {
	for _, r := range rt.Elements() {
		if r.ID == eid {
			return r, true
		}
	}
	return Element{}, false
}

func (rt *Runtime) RowByKey(key string) (Element, bool) {
	for _, r := range rt.Elements() {
		if r.Key() == key {
			return r, true
		}
	}
	return Element{}, false
}

func (rt *Runtime) RowTenants() []config.Tenant {
	var out []config.Tenant
	seen := map[string]bool{}
	for _, r := range rt.Elements() {
		if !seen[r.TSG] {
			seen[r.TSG] = true
			out = append(out, rt.TenantFor(r.TSG))
		}
	}
	return out
}

func (rt *Runtime) Scope() (int, string) {
	ts := rt.RowTenants()
	switch len(ts) {
	case 0:
		return 0, ""
	case 1:
		return 1, ts[0].Name
	}
	return len(ts), fmt.Sprintf("%d tenants", len(ts))
}
