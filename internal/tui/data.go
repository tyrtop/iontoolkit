package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

// Views is --views: a file of rich-data views to use instead of the one in the
// config directory. The command sets it before Run.
var Views string

// ViewsPath is where the views come from: --views if it was given, else
// views.yaml beside the config. A --debug or --fixtures run has its own throwaway
// root and so sees only the built-ins, which is why --views exists.
func ViewsPath(st *config.Store) string {
	if Views != "" {
		return Views
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, dataview.FileName)
}

// dataTab is one element's rich-data pane. It is a tab of its own rather than
// a panel on the list because the data is per element and worth keeping open
// while the list moves on, which is the same reason a shell is a tab.
type dataTab struct {
	eid    string
	name   string
	tsg    string
	site   string
	siteID string
	// cluster is the HA cluster the element record named, which the views that
	// ask about HA need and cannot derive.
	cluster string

	// One pane per view, in view order, all fetched at once: the whole of
	// what the portal knows about an element belongs on one scrollable page
	// rather than behind a mode switch per view. They arrive separately, so
	// each carries its own loading and error state and the page fills in.
	panes  []dataPane
	at     time.Time
	scroll int

	// The GUI-vs-CLI comparison, which exists only once asked for: it is the
	// one thing in this mode that dials the element.
	cmp compareState
}

type dataPane struct {
	view     string
	loading  bool
	err      string
	rendered dataview.Rendered
}

type dataLoadedMsg struct {
	eid      string
	view     string
	rendered dataview.Rendered
	err      string
}

func (m tuiModel) viewNames() []string {
	if m.views == nil {
		return nil
	}
	return m.views.Names()
}

// dataVars is what a view's paths are written against. The cluster is the one
// id that is not on the element row, so it comes from the record the view
// itself fetched on a previous pass -- or empty, and the HA sections stay
// gated off.
func (t dataTab) vars() dataview.Vars {
	return dataview.Vars{
		"element_id": t.eid,
		"site_id":    t.siteID,
		"cluster_id": t.cluster,
		"tenant_id":  t.tsg,
	}
}

func loadDataCmd(ctx contextLike, g dataview.Getter, v dataview.View, t dataTab) tea.Cmd {
	return func() tea.Msg {
		d, err := dataview.Fetch(ctx, g, v, t.vars())
		msg := dataLoadedMsg{eid: t.eid, view: v.Name}
		if err != nil {
			msg.err = err.Error()
		}
		if d != nil {
			msg.rendered = dataview.Render(v, d)
		}
		return msg
	}
}

// openDataTabs opens a pane per element. No device login is asked for: the
// portal half of iontk is the first useful thing someone has before they have
// ION credentials at all, which is the whole reason this mode exists.
func (m tuiModel) openDataTabs(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}
	if len(m.viewNames()) == 0 {
		m.status = "no data views are loaded"
		if m.viewsErr != "" {
			m.status = m.viewsErr
		}
		return m, nil
	}

	// A tenant that has not authenticated yet is connected first and the
	// request replayed, the way opening a session does it.
	if m.needsSignIn(tsgsOf(targets)) {
		m.resumeData = targets
		return m.startSignIn()
	}
	if conn := m.ensureClients(tsgsOf(targets)); len(conn) > 0 {
		m.resumeData = targets
		m.connectWait = len(conn)
		m.status = ""
		return m, tea.Batch(append(conn, m.spinner.Tick)...)
	}

	var cmds []tea.Cmd
	var unreachable []string
	for _, t := range targets {
		c := m.tenantClient(t.tsg)
		if c == nil {
			unreachable = append(unreachable, t.name)
			continue
		}

		tab := dataTab{eid: t.eid, name: t.name, tsg: t.tsg}
		// An element the list already knows carries its site and cluster, so
		// the first fetch can ask for interfaces without waiting on the
		// record to come back first.
		if el, ok := m.elementByKey(t.tsg + "/" + t.eid); ok {
			tab.siteID = el.SiteID
			tab.site = m.siteName(el)
			tab.cluster = el.ClusterID()
		}

		for _, name := range m.viewNames() {
			tab.panes = append(tab.panes, dataPane{view: name, loading: true})
		}

		// Opening an element that already has a tab reuses it rather than
		// stacking a second page of the same data.
		if i := m.dataIndex(t.tsg, t.eid); i >= 0 {
			tab.scroll = m.data[i].scroll
			m.data[i] = tab
			m.dataActive = i
		} else {
			m.data = append(m.data, tab)
			m.dataActive = len(m.data) - 1
		}
		cmds = append(cmds, m.loadTab(tab, c)...)
	}
	if len(cmds) == 0 {
		m.status = "could not reach " + strings.Join(unreachable, ", ")
		return m, nil
	}
	if len(unreachable) > 0 {
		m.status = "skipped " + strings.Join(unreachable, ", ") + ": tenant unreachable"
	}

	m.phase = phaseData
	m.status = ""
	return m, tea.Batch(append(cmds, m.spinner.Tick)...)
}

func (m tuiModel) dataIndex(tsg, eid string) int {
	for i, t := range m.data {
		if t.eid == eid && t.tsg == tsg {
			return i
		}
	}
	return -1
}

// loadTab is one fetch per view. They run as separate commands so the page
// paints as each answers: the whole set is thirty-odd requests on a big
// element, and waiting for the last of them to show the first would be the
// difference between a pause and a hang.
func (m tuiModel) loadTab(tab dataTab, c *api.Client) []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.views.Views))
	for _, v := range m.views.Views {
		cmds = append(cmds, loadDataCmd(m.ctx, c, v, tab))
	}
	return cmds
}

// contextLike is what the fetch needs of the model's context, which is only
// cancellation.
type contextLike = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

func (m tuiModel) onDataLoaded(msg dataLoadedMsg) (tea.Model, tea.Cmd) {
	for i := range m.data {
		if m.data[i].eid != msg.eid {
			continue
		}
		for j := range m.data[i].panes {
			if m.data[i].panes[j].view != msg.view {
				continue
			}
			m.data[i].panes[j].loading = false
			m.data[i].panes[j].err = msg.err
			m.data[i].panes[j].rendered = msg.rendered
			m.data[i].at = time.Now()
			return m, nil
		}
	}
	return m, nil
}

func (t dataTab) loading() bool {
	for _, p := range t.panes {
		if p.loading {
			return true
		}
	}
	return false
}

// failed marks the tab in the bar: one view refused is worth a mark, since
// the rest of the page looks complete without one.
func (t dataTab) failed() bool {
	for _, p := range t.panes {
		if p.err != "" {
			return true
		}
	}
	return false
}

func (m tuiModel) activeData() (dataTab, bool) {
	if m.dataActive < 0 || m.dataActive >= len(m.data) {
		return dataTab{}, false
	}
	return m.data[m.dataActive], true
}

// refreshData refetches the whole page, keeping the scroll: a refresh is for
// watching a value change, and being thrown back to the top to find it again
// defeats that.
func (m tuiModel) refreshData() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok {
		return m, nil
	}
	c := m.tenantClient(tab.tsg)
	if c == nil {
		m.status = "could not reach " + m.tenantName(tab.tsg)
		return m, nil
	}
	for i := range tab.panes {
		tab.panes[i].loading = true
		tab.panes[i].err = ""
	}
	m.data[m.dataActive] = tab
	return m, tea.Batch(append(m.loadTab(tab, c), m.spinner.Tick)...)
}

func (m tuiModel) closeDataTab() tuiModel {
	if _, ok := m.activeData(); !ok {
		return m
	}
	m.data = append(m.data[:m.dataActive], m.data[m.dataActive+1:]...)
	if m.dataActive >= len(m.data) {
		m.dataActive = len(m.data) - 1
	}
	if len(m.data) == 0 {
		m.phase = phasePicking
	}
	return m
}

func (m tuiModel) onDataKey(key string) (tea.Model, tea.Cmd) {
	if next, handled := m.onTabKey(key); handled {
		return next, nil
	}

	switch key {
	case "esc", "q", "ctrl+]":
		m.phase = phasePicking
		return m, nil
	case "r":
		return m.refreshData()
	case "c":
		return m.startCompare()
	}

	tab, ok := m.activeData()
	if !ok {
		return m, nil
	}
	page := m.dataPage(tab)
	_, rows := m.dataPaneSize()
	switch key {
	case "down", "j":
		tab.scroll++
	case "up", "k":
		tab.scroll--
	case "pgdown", " ", "space":
		tab.scroll += max(1, rows-1)
	case "pgup":
		tab.scroll -= max(1, rows-1)
	case "home", "g":
		tab.scroll = 0
	case "end", "G":
		tab.scroll = len(page.lines)
	// The page is long and its headers are what someone is looking for, so
	// these move a heading at a time rather than a line.
	case "alt+down", "]", "tab":
		tab.scroll = nextHead(page.heads, tab.scroll, 1)
	case "alt+up", "[", "shift+tab":
		tab.scroll = nextHead(page.heads, tab.scroll, -1)
	default:
		return m, nil
	}
	tab.scroll = clampScroll(tab.scroll, len(page.lines), rows)
	m.data[m.dataActive] = tab
	return m, nil
}

// nextHead is the header before or after where the pane is held. Going down
// stops at the last one rather than wrapping: a page that jumps back to the
// top when you press down again has lost your place.
func nextHead(heads []int, at, dir int) int {
	if len(heads) == 0 {
		return at
	}
	if dir > 0 {
		for _, h := range heads {
			if h > at {
				return h
			}
		}
		return at
	}
	for i := len(heads) - 1; i >= 0; i-- {
		if heads[i] < at {
			return heads[i]
		}
	}
	return 0
}

func clampScroll(at, total, rows int) int {
	return max(0, min(at, max(0, total-rows)))
}

func (m tuiModel) dataPaneSize() (int, int) {
	return max(20, m.width), max(3, m.height-6-m.statsRows())
}

// dataPage is the whole pane as text, with where its headers are. Built whole
// and then windowed: a table needs every row's width before it can line up
// the first, and alt+up/down needs to know which lines are headers.
type dataPage struct {
	lines []string
	heads []int
}

func (p dataPage) String() string { return strings.Join(p.lines, "\n") }

func (p *dataPage) head(line string) {
	p.lines = append(p.lines, "")
	p.heads = append(p.heads, len(p.lines))
	p.lines = append(p.lines, line)
}

// dataPage lays every view out one after another, so the whole of what the
// portal knows about an element is one page to scroll rather than a mode to
// switch. Each view's own header is a jump target.
func (m tuiModel) dataPage(tab dataTab) dataPage {
	width, _ := m.dataPaneSize()
	var p dataPage

	for _, pane := range tab.panes {
		switch {
		case pane.loading:
			p.head(ui.StyleElement.Render("── "+pane.view+" ") +
				ui.StyleDim.Render(fmt.Sprintf("%s reading", m.spinner.View())))
			continue
		case pane.err != "":
			p.head(ui.StyleElement.Render("── " + pane.view + " "))
			p.lines = append(p.lines, "  "+ui.StyleErr.Render(ui.Truncate(pane.err, width-2)))
			continue
		}

		p.head(ui.StyleElement.Render("── " + pane.view + " "))
		for _, sec := range pane.rendered.Sections {
			p.head("  " + ui.StyleCmd.Render(sec.Title))
			for _, row := range sec.Rows {
				if row.Value == "" {
					continue
				}
				p.lines = append(p.lines, "  "+ui.Label(row.Label)+" "+ui.Truncate(row.Value, width-ui.LabelWidth-4))
			}
			p.lines = append(p.lines, m.tableLines(sec, width)...)
			if sec.Truncated > 0 {
				p.lines = append(p.lines, "  "+ui.StyleDim.Render(fmt.Sprintf("… %d more not shown", sec.Truncated)))
			}
		}
		for _, e := range pane.rendered.Errs {
			p.lines = append(p.lines, "  "+ui.StyleWarn.Render(ui.Truncate(e, width-2)))
		}
		m.comparePane(&p, tab, pane.view, width)
	}
	return p
}

// tableLines lays a section's table out. Columns are padded plain and styled
// afterwards: padding a styled cell counts its escape bytes toward the width
// and the columns drift apart row by row.
func (m tuiModel) tableLines(sec dataview.RenderedSection, width int) []string {
	if len(sec.Headers) == 0 {
		return nil
	}
	if len(sec.Table) == 0 {
		if sec.Empty == "" {
			return nil
		}
		return []string{"  " + ui.StyleDim.Render(sec.Empty)}
	}

	// Sized to content, then capped by whatever the view asked for: an
	// element's interface names run from "1" to a 40-character Prisma tunnel,
	// and sizing to the longest would push every other column off the pane.
	widths := make([]int, len(sec.Headers))
	for i, h := range sec.Headers {
		widths[i] = ui.Width(h)
		for _, row := range sec.Table {
			if i < len(row.Cells) {
				widths[i] = max(widths[i], ui.Width(row.Cells[i]))
			}
		}
		if cap := sec.Widths[i]; cap > 0 {
			widths[i] = min(widths[i], max(cap, ui.Width(h)))
		}
	}

	pad := func(cells []string) string {
		var b strings.Builder
		for i := range sec.Headers {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			cell = ui.Truncate(cell, widths[i])
			if i == len(sec.Headers)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(fmt.Sprintf("%-*s", widths[i]+2, cell))
		}
		return b.String()
	}

	out := []string{"  " + ui.StyleDim.Render(ui.Truncate(pad(sec.Headers), width-2))}
	for _, row := range sec.Table {
		line := "  " + ui.Truncate(pad(row.Cells), width-2)
		if row.Err != "" {
			line = "  " + ui.StyleDim.Render(ui.Truncate(pad(row.Cells)+"  "+row.Err, width-2))
		}
		out = append(out, line)
	}
	return out
}

func (m tuiModel) viewData() string {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")

	tab, ok := m.activeData()
	if !ok {
		b.WriteString("\n" + ui.StyleDim.Render("  no data tabs open") + "\n")
		b.WriteString("\n" + ui.StyleDim.Render("ctrl+] elements"))
		return b.String()
	}

	where := tab.name
	if tab.site != "" {
		where += ui.StyleDim.Render(" · " + tab.site)
	}
	// How much of the page is still arriving, since every view is fetched at
	// once and they answer separately.
	waiting := 0
	for _, pane := range tab.panes {
		if pane.loading {
			waiting++
		}
	}
	state := ""
	switch {
	case waiting > 0:
		state = fmt.Sprintf(" · reading %d of %d", len(tab.panes)-waiting+1, len(tab.panes))
	case !tab.at.IsZero():
		state = " · " + ui.Age(time.Since(tab.at)) + " old"
	}
	b.WriteString(ui.StyleElement.Render(where) +
		ui.StyleDim.Render(fmt.Sprintf("  %d views%s", len(tab.panes), state)) + "\n")

	page := m.dataPage(tab)
	lines := page.lines
	_, rows := m.dataPaneSize()
	at := clampScroll(tab.scroll, len(lines), rows)
	// The window is padded to the full height whether or not there is enough
	// to fill it: a frame that changes height makes the terminal twitch.
	for i := 0; i < rows; i++ {
		if at+i < len(lines) {
			b.WriteString(lines[at+i])
		}
		b.WriteString("\n")
	}

	if m.stats {
		b.WriteString(m.statsPane() + "\n")
	}

	status := m.status
	if len(lines) > rows {
		scrolled := fmt.Sprintf("%d-%d of %d lines", at+1, min(at+rows, len(lines)), len(lines))
		if status != "" {
			scrolled += " · "
		}
		status = scrolled + status
	}
	hint := "alt+↑↓ next heading · ↑↓ scroll · r refresh · c CLI check · alt+←→/hl tab · alt+k close · ctrl+] elements"
	width := max(10, m.width-1)
	b.WriteString(ui.Footer(ui.Truncate(status, width), ui.Truncate(hint, width)))
	return b.String()
}
