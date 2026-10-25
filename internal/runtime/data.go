package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/sase/api"
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
	filter   string
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

func (rt *Runtime) loadTab(ctx context.Context, c *api.Client, tab DataTab) error {
	set := rt.Views()
	if set == nil {
		return fmt.Errorf("no data views are loaded")
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

type Page struct {
	Lines []string
	Heads []int
	Hdrs  []int
}

func (p *Page) head(line string) {
	p.Heads = append(p.Heads, len(p.Lines))
	p.Lines = append(p.Lines, line)
}

func DataPage(t DataTab) Page {
	p := Page{Lines: []string{fmt.Sprintf("%s · %s", t.Name, t.Site)}}
	for _, pane := range t.Panes {
		p.Lines = append(p.Lines, "")
		p.head("── " + pane.View)
		if pane.Err != "" {
			p.Lines = append(p.Lines, "  ! "+pane.Err)
		}
		for _, sec := range pane.Rendered.Sections {
			p.head("  " + sec.Title)
			for _, r := range sec.Rows {
				p.Lines = append(p.Lines, fmt.Sprintf("    %-16s %s", r.Label, r.Value))
			}
			if len(sec.Headers) > 0 {
				p.Hdrs = append(p.Hdrs, len(p.Lines))
				p.Lines = append(p.Lines, tableText(sec)...)
			}
		}
	}
	if t.Check.Ran() {
		p.Lines = append(p.Lines, "")
		p.head("── CLI check")
		p.Lines = append(p.Lines, CheckText(t.Check)...)
	}
	return p
}

func DataText(t DataTab) []string { return DataPage(t).Lines }

func FilterPage(plain []string, heads, hdrs []int, q string) (keep, kept []int) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		keep = make([]int, len(plain))
		for i := range plain {
			keep[i] = i
		}
		return keep, heads
	}
	head, hdr := make([]int, len(plain)), make([]int, len(plain))
	at, under, headAt, hdrAt := -1, -1, 0, 0
	for i := range plain {
		for headAt < len(heads) && heads[headAt] <= i {
			at = heads[headAt]
			headAt++
		}
		for hdrAt < len(hdrs) && hdrs[hdrAt] <= i {
			under = hdrs[hdrAt]
			hdrAt++
		}
		head[i], hdr[i] = at, under
	}
	seen := map[int]bool{}
	add := func(i int) {
		if i >= 0 && !seen[i] {
			seen[i] = true
			keep = append(keep, i)
		}
	}
	for i, line := range plain {
		if !strings.Contains(strings.ToLower(line), q) {
			continue
		}
		if h := head[i]; h >= 0 && !seen[h] {
			kept = append(kept, len(keep))
			add(h)
		}
		if d := hdr[i]; d >= 0 && d != i && d > head[i] {
			add(d)
		}
		add(i)
	}
	return keep, kept
}

func (rt *Runtime) SetDataFilter(q string) {
	rt.mu.Lock()
	rt.data.filter = q
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaData})
}

func (rt *Runtime) DataFilter() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.data.filter
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
	ctx := c.Ctx
	if d := rt.elementTimeout(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	done := rt.RunCheck(ctx, tab)
	rt.SetCheck(tab.EID, done)
	c.Result = done
	if done.Err != "" {
		return fmt.Errorf("%s: %s", tab.Name, done.Err)
	}
	c.Say("%s · %s", tab.Name, tab.Site)
	for _, l := range CheckText(done) {
		c.Say("%s", l)
	}
	return nil
}

func (rt *Runtime) elementTimeout() time.Duration {
	rt.mu.RLock()
	opts, st := rt.dev.Options, rt.store
	rt.mu.RUnlock()
	if opts == nil {
		return 0
	}
	t, _ := rt.Tenant()
	o, err := opts(st, t, rt.IONUser(), nil)
	if err != nil {
		return 0
	}
	return o.ElementTimeout
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

func (rt *Runtime) Reach(rows []Element) ([]Element, string) {
	for _, r := range rows {
		rt.ClearIssue(r.TSG)
	}
	var reach []Element
	var unreachable []string
	for _, r := range rows {
		if rt.Reachable(r.TSG) {
			reach = append(reach, r)
			continue
		}
		unreachable = append(unreachable, r.Label())
	}
	switch {
	case len(reach) == 0 && len(unreachable) > 0:
		return nil, "could not reach " + strings.Join(unreachable, ", ")
	case len(unreachable) > 0:
		return reach, "skipped " + strings.Join(unreachable, ", ") + ": tenant unreachable"
	}
	return reach, ""
}

func (rt *Runtime) NeedsSignIn(tsgs []string) bool {
	if rt.TokenDead() == "" {
		return false
	}
	if len(tsgs) == 0 {
		t, _ := rt.Tenant()
		return t.UsesBrowser()
	}
	for _, tsg := range tsgs {
		if t, ok := rt.TenantOf(tsg); !ok || t.UsesBrowser() {
			return true
		}
	}
	return false
}

func (rt *Runtime) openData(c *Call) ([]DataTab, error) {
	rows := rt.ElementsArg(c, "elements")
	if len(rows) == 0 {
		return nil, fmt.Errorf("no elements")
	}
	rows, note := rt.Reach(rows)
	if len(rows) == 0 {
		return nil, errors.New(note)
	}
	if note != "" {
		c.Say("%s", note)
	}
	tabs := rt.OpenDataTabs(rows)
	err := rt.LoadTabs(c.Ctx, tabs)
	var out []DataTab
	for _, t := range tabs {
		if i := rt.indexOfData(t.Key()); i >= 0 {
			out = append(out, rt.DataTabs()[i])
		}
	}
	return out, err
}

func (rt *Runtime) OpenDataTabs(rows []Element) []DataTab {
	tabs := make([]DataTab, 0, len(rows))
	for _, r := range rows {
		tabs = append(tabs, rt.OpenData(r.ID, r.Label(), r.TSG))
	}
	return tabs
}

func (rt *Runtime) LoadTabs(ctx context.Context, tabs []DataTab) error {
	errs := make([]error, len(tabs))
	var wg sync.WaitGroup
	for i, tab := range tabs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := rt.TenantFor(tab.TSG)
			c, err := rt.Connect(ctx, t)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %s", t.Name, rt.explain(err))
				return
			}
			errs[i] = rt.loadTab(ctx, c, tab)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (rt *Runtime) Reachable(tsg string) bool {
	return rt.Clients().Get(tsg) != nil || rt.NeedsConnect(tsg)
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
				return rt.LoadTabs(c.Ctx, []DataTab{tab})
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
			Name: "data.export", Help: "write the tab in front to a file under exports/, as far as the data filter lets it through",
			Run: func(rt *Runtime, c *Call) error {
				tab, ok := rt.ActiveData()
				if !ok {
					return fmt.Errorf("no data tab open")
				}
				page := DataPage(tab)
				keep, _ := FilterPage(page.Lines, page.Heads, page.Hdrs, rt.DataFilter())
				lines := make([]string, 0, len(keep))
				for _, i := range keep {
					lines = append(lines, page.Lines[i])
				}
				where, err := rt.Export(tab.Name, "txt", strings.Join(lines, "\n"))
				if err == nil {
					c.Say("exported %s to %s", tab.Name, where)
					c.Result = where
				}
				return err
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
