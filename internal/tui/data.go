package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

type (
	dataTab  = runtime.DataTab
	dataPane = runtime.DataPane
)

type dataLoadedMsg struct {
	eid      string
	view     string
	rendered dataview.Rendered
	err      string
}

func (m tuiModel) viewNames() []string { return m.runtime().ViewNames() }

func (m tuiModel) dataTabs() []dataTab { return m.runtime().DataTabs() }

func (m tuiModel) dataActive() int { return m.runtime().DataActive() }

func (m tuiModel) openDataTabs(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}
	if len(m.viewNames()) == 0 {
		m.status = "no data views are loaded"
		if e := m.runtime().ViewsErr(); e != "" {
			m.status = e
		}
		return m, nil
	}

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
		cmds = append(cmds, m.loadTab(m.runtime().OpenData(t.eid, t.name, t.tsg), c)...)
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

func (m tuiModel) loadTab(tab dataTab, c *api.Client) []tea.Cmd {
	set := m.runtime().Views()
	if set == nil {
		return nil
	}
	rt, ctx := m.runtime(), m.ctx
	cmds := make([]tea.Cmd, 0, len(set.Views))
	for _, v := range set.Views {
		cmds = append(cmds, func() tea.Msg {
			r, problem := rt.LoadView(ctx, c, tab, v)
			return dataLoadedMsg{eid: tab.EID, view: v.Name, rendered: r, err: problem}
		})
	}
	return cmds
}

func (m tuiModel) onDataLoaded(msg dataLoadedMsg) (tea.Model, tea.Cmd) {
	m.runtime().ApplyView(msg.eid, msg.view, msg.rendered, msg.err)
	return m, nil
}

func (m tuiModel) activeData() (dataTab, bool) { return m.runtime().ActiveData() }

func (m tuiModel) refreshData() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok {
		return m, nil
	}
	c := m.tenantClient(tab.TSG)
	if c == nil {
		m.status = "could not reach " + m.tenantName(tab.TSG)
		return m, nil
	}
	tab, _ = m.runtime().RefreshData()
	return m, tea.Batch(append(m.loadTab(tab, c), m.spinner.Tick)...)
}

func (m tuiModel) closeDataTab() tuiModel {
	if _, ok := m.activeData(); !ok {
		return m
	}
	if m.runtime().CloseData() == 0 {
		m.phase = phasePicking
	}
	return m
}

func (m tuiModel) onDataKey(key string) (tea.Model, tea.Cmd) {
	if next, cmd, ran := m.bindingOn(scrData, key); ran {
		return next, cmd
	}

	if next, handled := m.onTabKey(key); handled {
		return next, nil
	}

	switch key {
	case "esc", "q", "ctrl+]":
		if m.dataFilterOn || m.dataFilter.Value() != "" {
			m.dataFilterOn = false
			m.dataFilter.SetValue("")
			m.dataFilter.Blur()
			return m, nil
		}
		m.phase = phasePicking
		return m, nil
	case "/":
		if !m.dataFilterOn {
			m.dataFilter.SetValue("")
			m.dataFilterOn = true
			return m, m.dataFilter.Focus()
		}
	case "enter":
		if m.dataFilterOn {
			m.dataFilterOn = false
			m.dataFilter.Blur()
			return m, nil
		}
	case "r":
		if !m.dataFilterOn {
			return m.refreshData()
		}
	case "c":
		if !m.dataFilterOn {
			return m.startCompare()
		}
	case writeKey:
		if !m.dataFilterOn {
			return m.startWrite()
		}
	case exportKey:
		return m.exportDataPage()
	}

	tab, ok := m.activeData()
	if !ok {
		return m, nil
	}
	page := m.visiblePage(tab)
	_, rows := m.dataPaneSize()

	if m.dataFilterOn {
		switch key {
		case "down", "up", "pgdown", "pgup", "home", "end", "alt+down", "alt+up":
		default:
			return m, nil
		}
	}

	at := tab.Scroll
	switch key {
	case "down", "j":
		at++
	case "up", "k":
		at--
	case "pgdown", " ", "space":
		at += max(1, rows-1)
	case "pgup":
		at -= max(1, rows-1)
	case "home", "g":
		at = 0
	case "end", "G":
		at = len(page.lines)
	case "alt+down", "]", "tab":
		at = nextHead(page.heads, at, 1)
	case "alt+up", "[", "shift+tab":
		at = nextHead(page.heads, at, -1)
	default:
		return m, nil
	}
	m.runtime().ScrollData(clampScroll(at, len(page.lines), rows))
	return m, nil
}

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

func (m *tuiModel) visiblePage(tab dataTab) dataPage {
	page := m.dataPage(tab)
	q := strings.ToLower(strings.TrimSpace(m.dataFilter.Value()))
	if q == "" {
		return page
	}

	head, hdr := make([]int, len(page.lines)), make([]int, len(page.lines))
	at, under := -1, -1
	headAt, hdrAt := 0, 0
	for i := range page.lines {
		for headAt < len(page.heads) && page.heads[headAt] <= i {
			at = page.heads[headAt]
			headAt++
		}
		for hdrAt < len(page.hdrs) && page.hdrs[hdrAt] <= i {
			under = page.hdrs[hdrAt]
			hdrAt++
		}
		head[i], hdr[i] = at, under
	}

	var out dataPage
	kept := map[int]bool{}
	add := func(i int) {
		if i < 0 || kept[i] {
			return
		}
		kept[i] = true
		out.lines = append(out.lines, page.lines[i])
	}
	for i, line := range page.lines {
		if !strings.Contains(strings.ToLower(ui.Plain(line)), q) {
			continue
		}
		if h := head[i]; h >= 0 && !kept[h] {
			out.heads = append(out.heads, len(out.lines))
			add(h)
		}
		if d := hdr[i]; d >= 0 && d != i && d > head[i] {
			add(d)
		}
		add(i)
	}
	return out
}

func (m tuiModel) dataPaneSize() (int, int) {
	return max(20, m.width), max(3, m.height-6-m.statsRows())
}

type dataPage struct {
	lines []string
	heads []int
	hdrs  []int
	views []pageView
}

type pageView struct {
	at   int
	name string
}

func (p dataPage) String() string { return strings.Join(p.lines, "\n") }

func (p *dataPage) head(line string) {
	p.lines = append(p.lines, "")
	p.heads = append(p.heads, len(p.lines))
	p.lines = append(p.lines, line)
}

func (m tuiModel) dataPage(tab dataTab) dataPage {
	width, _ := m.dataPaneSize()
	var p dataPage

	for _, pane := range tab.Panes {
		p.views = append(p.views, pageView{at: len(p.lines), name: pane.View})
		switch {
		case pane.Loading:
			p.head(ui.StyleElement.Render("── "+pane.View+" ") +
				ui.StyleDim.Render(fmt.Sprintf("%s reading", m.spinner.View())))
			continue
		case pane.Err != "":
			p.head(ui.StyleElement.Render("── " + pane.View + " "))
			p.lines = append(p.lines, "  "+ui.StyleErr.Render(ui.Truncate(pane.Err, width-2)))
			continue
		}

		p.head(ui.StyleElement.Render("── " + pane.View + " "))
		for _, sec := range pane.Rendered.Sections {
			p.head("  " + ui.StyleCmd.Render(sec.Title))
			for _, row := range sec.Rows {
				if row.Value == "" {
					continue
				}
				p.lines = append(p.lines, "  "+ui.Label(row.Label)+" "+ui.Truncate(row.Value, width-ui.LabelWidth-4))
			}
			if table := m.tableLines(sec, width); len(table) > 0 {
				p.hdrs = append(p.hdrs, len(p.lines))
				p.lines = append(p.lines, table...)
			}
			if sec.Truncated > 0 {
				p.lines = append(p.lines, "  "+ui.StyleDim.Render(fmt.Sprintf("… %d more not shown", sec.Truncated)))
			}
		}
		for _, e := range pane.Rendered.Errs {
			p.lines = append(p.lines, "  "+ui.StyleWarn.Render(ui.Truncate(e, width-2)))
		}
		m.comparePane(&p, tab, pane.View, width)
	}
	return p
}

func (m tuiModel) viewAtScroll(tab dataTab) string {
	page := m.dataPage(tab)
	name := ""
	for _, v := range page.views {
		if v.at > tab.Scroll {
			break
		}
		name = v.name
	}
	if name == "" && len(tab.Panes) > 0 {
		name = tab.Panes[0].View
	}
	return name
}

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

	where := tab.Name
	if tab.Site != "" {
		where += ui.StyleDim.Render(" · " + tab.Site)
	}
	waiting := 0
	for _, pane := range tab.Panes {
		if pane.Loading {
			waiting++
		}
	}
	state := ""
	switch {
	case waiting > 0:
		state = fmt.Sprintf(" · reading %d of %d", len(tab.Panes)-waiting+1, len(tab.Panes))
	case !tab.At.IsZero():
		state = " · " + ui.Age(time.Since(tab.At)) + " old"
	}
	b.WriteString(ui.StyleElement.Render(where) +
		ui.StyleDim.Render(fmt.Sprintf("  %d views%s", len(tab.Panes), state)) + "\n")

	if m.dataFilterOn || m.dataFilter.Value() != "" {
		line := m.dataFilter.View()
		if m.dataFilter.Value() != "" {
			whole := len(m.dataPage(tab).lines)
			shown := len(m.visiblePage(tab).lines)
			line += ui.StyleDim.Render(fmt.Sprintf("   %d of %d lines", shown, whole))
		}
		b.WriteString(line + "\n")
	} else {
		b.WriteString(ui.StyleDim.Render("press / to filter") + "\n")
	}

	page := m.visiblePage(tab)
	lines := page.lines
	_, rows := m.dataPaneSize()
	at := clampScroll(tab.Scroll, len(lines), rows)
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
	hint := "alt+↑↓ next heading · ↑↓ scroll · / filter · r refresh · c CLI check · " +
		writeKeyHint + " · " + exportKeyHint + " · " + editKeyHint + " · alt+←→/hl tab · alt+k close · ctrl+] elements"
	if m.dataFilterOn {
		hint = "type to narrow the page · ↑↓ scroll · enter apply · esc clear"
	}
	width := max(10, m.width-1)
	b.WriteString(ui.Footer(ui.Truncate(status, width), ui.Truncate(hint, width)))
	return b.String()
}

func newDataFilter() textinput.Model {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "filter lines"
	in.SetWidth(26)
	return in
}

func (m tuiModel) onDataFilterKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "esc", "enter", "down", "up", "pgdown", "pgup", "home", "end",
		"alt+down", "alt+up", "alt+left", "alt+right", "alt+k", "alt+w",
		exportKey, editKey:
		return m, nil, false
	case "backspace":
		if m.dataFilter.Value() == "" {
			m.dataFilterOn = false
			m.dataFilter.Blur()
			return m, nil, true
		}
	}

	var cmd tea.Cmd
	m.dataFilter, cmd = m.dataFilter.Update(msg)
	if tab, ok := m.activeData(); ok {
		_, rows := m.dataPaneSize()
		m.runtime().ScrollData(clampScroll(tab.Scroll, len(m.visiblePage(tab).lines), rows))
	}
	return m, cmd, true
}
