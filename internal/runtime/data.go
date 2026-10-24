package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/search"
)

const AreaData = "data"

type DataPane struct {
	View     string
	Loading  bool
	Err      string
	Rendered dataview.Rendered
}

type Check struct {
	Loading bool
	Err     string
	At      time.Time
	ByView  map[string]dataview.Comparison
}

func (c Check) Ran() bool { return !c.At.IsZero() }

type DataTab struct {
	EID     string
	Name    string
	TSG     string
	Site    string
	SiteID  string
	Cluster string
	Panes   []DataPane
	At      time.Time
	Check   Check
	Scroll  int
}

func (t DataTab) Key() string { return t.TSG + "/" + t.EID }

func (t DataTab) Vars() dataview.Vars {
	return dataview.Vars{"element_id": t.EID, "site_id": t.SiteID, "cluster_id": t.Cluster, "tenant_id": t.TSG}
}

func (t DataTab) Loading() bool {
	for _, p := range t.Panes {
		if p.Loading {
			return true
		}
	}
	return false
}

func (t DataTab) Failed() bool {
	for _, p := range t.Panes {
		if p.Err != "" {
			return true
		}
	}
	return false
}

type dataState struct {
	views    *dataview.Set
	viewsErr string
	tabs     []DataTab
	active   int
}

func (rt *Runtime) SetViews(set *dataview.Set, problem string) {
	rt.mu.Lock()
	rt.data.views, rt.data.viewsErr = set, problem
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaData})
}

func (rt *Runtime) Views() *dataview.Set {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.data.views
}

func (rt *Runtime) ViewsErr() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.data.viewsErr
}

func (rt *Runtime) ViewNames() []string {
	if v := rt.Views(); v != nil {
		return v.Names()
	}
	return nil
}

func (rt *Runtime) editData(edit func(d *dataState)) {
	rt.mu.Lock()
	edit(&rt.data)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaData})
}

func (rt *Runtime) DataTabs() []DataTab {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]DataTab, len(rt.data.tabs))
	for i, t := range rt.data.tabs {
		t.Panes = append([]DataPane(nil), t.Panes...)
		out[i] = t
	}
	return out
}

func (rt *Runtime) DataActive() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.data.active
}

func (rt *Runtime) ActiveData() (DataTab, bool) {
	tabs, i := rt.DataTabs(), rt.DataActive()
	if i < 0 || i >= len(tabs) {
		return DataTab{}, false
	}
	return tabs[i], true
}

func (rt *Runtime) SelectData(i int) {
	rt.editData(func(d *dataState) { d.active = i })
}

func (rt *Runtime) SetDataTabs(tabs []DataTab, active int) {
	rt.editData(func(d *dataState) { d.tabs, d.active = tabs, active })
}

func (rt *Runtime) ScrollData(line int) {
	rt.editData(func(d *dataState) {
		if d.active >= 0 && d.active < len(d.tabs) {
			d.tabs[d.active].Scroll = line
		}
	})
}

func (rt *Runtime) OpenData(eid, name, tsg string) DataTab {
	tab := DataTab{EID: eid, Name: name, TSG: tsg}
	for _, el := range rt.Elements() {
		if el.TSG == tsg && el.ID == eid {
			tab.SiteID, tab.Cluster = el.SiteID, el.ClusterID()
			tab.Site = search.Of(el.Element, el.TSG, el.Tenant, rt.Sites()).SiteLabel()
		}
	}
	for _, v := range rt.ViewNames() {
		tab.Panes = append(tab.Panes, DataPane{View: v, Loading: true})
	}
	rt.editData(func(d *dataState) {
		for i, t := range d.tabs {
			if t.Key() == tab.Key() {
				tab.Scroll = t.Scroll
				d.tabs[i], d.active = tab, i
				return
			}
		}
		d.tabs = append(d.tabs, tab)
		d.active = len(d.tabs) - 1
	})
	return tab
}

func (rt *Runtime) RefreshData() (DataTab, bool) {
	var tab DataTab
	ok := false
	rt.editData(func(d *dataState) {
		if d.active < 0 || d.active >= len(d.tabs) {
			return
		}
		for i := range d.tabs[d.active].Panes {
			d.tabs[d.active].Panes[i].Loading = true
			d.tabs[d.active].Panes[i].Err = ""
		}
		tab, ok = d.tabs[d.active], true
	})
	return tab, ok
}

func (rt *Runtime) CloseData() int {
	left := 0
	rt.editData(func(d *dataState) {
		if d.active >= 0 && d.active < len(d.tabs) {
			d.tabs = append(d.tabs[:d.active], d.tabs[d.active+1:]...)
		}
		if d.active >= len(d.tabs) {
			d.active = len(d.tabs) - 1
		}
		left = len(d.tabs)
	})
	return left
}

func (rt *Runtime) LoadView(ctx context.Context, g dataview.Getter, tab DataTab, v dataview.View) (dataview.Rendered, string) {
	d, err := dataview.Fetch(ctx, g, v, tab.Vars())
	var r dataview.Rendered
	if d != nil {
		r = dataview.Render(v, d)
	}
	if err != nil {
		return r, err.Error()
	}
	return r, ""
}

func (rt *Runtime) ApplyView(eid, view string, r dataview.Rendered, problem string) {
	rt.editData(func(d *dataState) {
		for i := range d.tabs {
			if d.tabs[i].EID != eid {
				continue
			}
			for j := range d.tabs[i].Panes {
				if d.tabs[i].Panes[j].View != view {
					continue
				}
				d.tabs[i].Panes[j].Loading, d.tabs[i].Panes[j].Err, d.tabs[i].Panes[j].Rendered = false, problem, r
				d.tabs[i].At = time.Now()
				return
			}
		}
	})
}

func (rt *Runtime) SetCheck(eid string, c Check) {
	rt.editData(func(d *dataState) {
		for i := range d.tabs {
			if d.tabs[i].EID == eid {
				d.tabs[i].Check = c
			}
		}
	})
}

func (rt *Runtime) LoadTab(ctx context.Context, tab DataTab) error {
	set := rt.Views()
	if set == nil {
		return fmt.Errorf("no data views are loaded")
	}
	c := rt.Clients().Get(tab.TSG)
	if c == nil {
		return fmt.Errorf("could not reach %s", tab.Name)
	}
	done := make(chan struct{}, len(set.Views))
	for _, v := range set.Views {
		go func() {
			r, problem := rt.LoadView(ctx, c, tab, v)
			rt.ApplyView(tab.EID, v.Name, r, problem)
			done <- struct{}{}
		}()
	}
	for range set.Views {
		<-done
	}
	return nil
}

var ErrNoLogin = errors.New("no ION login saved")

func (rt *Runtime) BeginCheck() (DataTab, error) {
	tab, ok := rt.ActiveData()
	if !ok {
		return tab, fmt.Errorf("no data tab open")
	}
	if tab.Check.Loading {
		return tab, fmt.Errorf("%s is already being checked", tab.Name)
	}
	set := rt.Views()
	if set == nil || len(dataview.CompareViews(set.Views, tab.Vars())) == 0 {
		return tab, fmt.Errorf("no view asks this element's CLI anything")
	}
	name := tab.TSG
	if t, ok := rt.TenantOf(tab.TSG); ok {
		name = t.Name
	}
	if rt.Clients().Get(tab.TSG) == nil {
		return tab, fmt.Errorf("could not reach %s", name)
	}
	if user, pass := rt.Login(tab.TSG); user == "" || pass == "" {
		return tab, fmt.Errorf("%w for %s", ErrNoLogin, name)
	}
	rt.SetCheck(tab.EID, Check{Loading: true})
	return tab, nil
}

func (rt *Runtime) RunCheck(ctx context.Context, tab DataTab) Check {
	rt.mu.RLock()
	session := rt.dev.Session
	rt.mu.RUnlock()
	c := rt.Clients().Get(tab.TSG)
	out := Check{At: time.Now(), ByView: map[string]dataview.Comparison{}}
	set := rt.Views()
	if c == nil || set == nil || session == nil {
		out.Err = "could not reach " + tab.Name
		return out
	}
	views := dataview.CompareViews(set.Views, tab.Vars())
	user, pass := rt.Login(tab.TSG)
	answers, err := session(c).ToolkitSession(ctx, tab.EID, tab.Name+"#", user, pass, dataview.CompareCommands(views))
	if err != nil {
		out.Err = err.Error()
		return out
	}
	said := dataview.DeviceOutput{}
	for _, a := range answers {
		said[a.Command] = a.Output
	}
	for _, v := range views {
		d, err := dataview.FetchCompare(ctx, c, v, tab.Vars())
		cmp := dataview.RenderCompare(v, d, said)
		if err != nil {
			cmp.Errs = append(cmp.Errs, err.Error())
		}
		out.ByView[v.Name] = cmp
	}
	return out
}

func CheckSummary(byView map[string]dataview.Comparison) string {
	var differ, unknown, total int
	for _, cmp := range byView {
		for _, row := range cmp.Rows {
			total++
			switch row.Verdict {
			case dataview.Differ:
				differ++
			case dataview.Unknown:
				unknown++
			}
		}
	}
	switch {
	case total == 0:
		return ""
	case differ > 0:
		return fmt.Sprintf("%d of %d CLI checks disagree with the portal", differ, total)
	case unknown == total:
		return "the device answered none of the CLI checks"
	default:
		return fmt.Sprintf("the device agrees on all %d CLI checks", total-unknown)
	}
}

func DataText(t DataTab) []string {
	out := []string{fmt.Sprintf("%s · %s", t.Name, t.Site)}
	for _, p := range t.Panes {
		out = append(out, "", "── "+p.View)
		if p.Err != "" {
			out = append(out, "  ! "+p.Err)
		}
		for _, sec := range p.Rendered.Sections {
			out = append(out, "  "+sec.Title)
			for _, r := range sec.Rows {
				out = append(out, fmt.Sprintf("    %-16s %s", r.Label, r.Value))
			}
			if len(sec.Headers) > 0 {
				out = append(out, tableText(sec)...)
			}
		}
	}
	return out
}

func (rt *Runtime) indexOfData(key string) int {
	for i, t := range rt.DataTabs() {
		if t.Key() == key {
			return i
		}
	}
	return -1
}

func (rt *Runtime) checkInFront(c *Call) error {
	tab, err := rt.BeginCheck()
	if err != nil {
		return err
	}
	done := rt.RunCheck(c.Ctx, tab)
	rt.SetCheck(tab.EID, done)
	if done.Err != "" {
		return fmt.Errorf("%s: %s", tab.Name, done.Err)
	}
	c.Say("%s · %s", tab.Name, tab.Site)
	for _, l := range CheckText(done) {
		c.Say("%s", l)
	}
	return nil
}

func CheckText(c Check) []string {
	var out []string
	views := make([]string, 0, len(c.ByView))
	for v := range c.ByView {
		views = append(views, v)
	}
	sort.Strings(views)
	verdict := map[dataview.Verdict]string{dataview.Agree: "agree", dataview.Differ: "DIFFER", dataview.Unknown: "?"}
	for _, view := range views {
		cmp := c.ByView[view]
		out = append(out, "── "+view+" · "+strings.Join(cmp.Commands, " · "))
		for _, r := range cmp.Rows {
			out = append(out, fmt.Sprintf("  %-6s %-16s portal %-20s device %s", verdict[r.Verdict], r.Label, r.Portal, r.Device))
		}
		for _, e := range cmp.Errs {
			out = append(out, "  ! "+e)
		}
	}
	if s := CheckSummary(c.ByView); s != "" {
		out = append(out, s)
	}
	return out
}

func tableText(sec dataview.RenderedSection) []string {
	widths := make([]int, len(sec.Headers))
	for i, h := range sec.Headers {
		widths[i] = utf8.RuneCountInString(h)
		for _, row := range sec.Table {
			if i < len(row.Cells) {
				widths[i] = max(widths[i], utf8.RuneCountInString(row.Cells[i]))
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i := range sec.Headers {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteString(cell)
			if i < len(sec.Headers)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+2))
			}
		}
		return "    " + strings.TrimRight(b.String(), " ")
	}
	out := []string{line(sec.Headers)}
	for _, row := range sec.Table {
		l := line(row.Cells)
		if row.Err != "" {
			l += "  ! " + row.Err
		}
		out = append(out, l)
	}
	if len(sec.Table) == 0 && sec.Empty != "" {
		out = append(out, "    "+sec.Empty)
	}
	return out
}

func (rt *Runtime) openData(c *Call) ([]DataTab, error) {
	rows := rt.Targets()
	if ids := c.Arg("elements"); ids != "" {
		rows = rt.knownRows(c, strings.Split(ids, ","))
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no elements")
	}
	var out []DataTab
	for _, r := range rows {
		if t, ok := rt.TenantOf(r.TSG); ok {
			if _, err := rt.Connect(c.Ctx, t); err != nil {
				return out, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
			}
		}
		tab := rt.OpenData(r.ID, r.Label(), r.TSG)
		if err := rt.LoadTab(c.Ctx, tab); err != nil {
			return out, err
		}
		if t, ok := rt.ActiveData(); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func dataCommands() []Command {
	return []Command{
		{
			Name: "data.open", Slow: true, Help: "open an element's rich-data views and read them: elements=ID,ID (default: the selection)",
			Args: []Arg{{Name: "elements"}},
			Run: func(rt *Runtime, c *Call) error {
				tabs, err := rt.openData(c)
				for _, t := range tabs {
					for _, l := range DataText(t) {
						c.Say("%s", l)
					}
				}
				return err
			},
		},
		{
			Name: "data.refresh", Slow: true, Help: "read the tab in front again",
			Run: func(rt *Runtime, c *Call) error {
				tab, ok := rt.RefreshData()
				if !ok {
					return fmt.Errorf("data.refresh: no tab open")
				}
				return rt.LoadTab(c.Ctx, tab)
			},
		},
		{
			Name: "data.check", Slow: true, Help: "ask the device what its views name and compare with the portal: elements=ID,ID (default: the tab in front)",
			Args: []Arg{{Name: "elements"}},
			Run: func(rt *Runtime, c *Call) error {
				if c.Arg("elements") == "" {
					return rt.checkInFront(c)
				}
				tabs, err := rt.openData(c)
				if err != nil {
					return err
				}
				var failed []string
				for _, t := range tabs {
					rt.SelectData(rt.indexOfData(t.Key()))
					if err := rt.checkInFront(c); err != nil {
						failed = append(failed, err.Error())
					}
				}
				if len(failed) > 0 {
					return fmt.Errorf("%s", strings.Join(failed, "; "))
				}
				return nil
			},
		},
		{
			Name: "data.close", Help: "close the tab in front",
			Run: func(rt *Runtime, _ *Call) error { rt.CloseData(); return nil },
		},
		{
			Name: "data.next", Help: "the next data tab",
			Run: func(rt *Runtime, _ *Call) error {
				if n := len(rt.DataTabs()); n > 0 {
					rt.SelectData((rt.DataActive() + 1) % n)
				}
				return nil
			},
		},
		{
			Name: "data.prev", Help: "the previous data tab",
			Run: func(rt *Runtime, _ *Call) error {
				if n := len(rt.DataTabs()); n > 0 {
					rt.SelectData((rt.DataActive() - 1 + n) % n)
				}
				return nil
			},
		},
	}
}
