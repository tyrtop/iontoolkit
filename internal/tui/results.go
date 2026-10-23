package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/ui"
)

func newResultsTable() table.Model {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "", Width: 2},
			{Title: "element", Width: 26},
			{Title: "model", Width: 11},
			{Title: "cmds", Width: 5},
			{Title: "took", Width: 9},
		}),
		table.WithFocused(true),
	)
	t.SetStyles(tableStyles())
	return t
}

func (m *tuiModel) moveResultCursor(delta int) {
	rows := m.resTable.Rows()
	if len(rows) == 0 {
		return
	}
	n := max(0, min(m.resTable.Cursor()+delta, len(rows)-1))
	if at := m.resTable.Cursor(); n >= at {
		m.resTable.MoveDown(n - at)
		m.rebuildResultRows()
		return
	}

	fresh := newResultsTable()
	fresh.SetColumns(m.resTable.Columns())
	fresh.SetRows(rows)
	fresh.SetWidth(m.resTable.Width())
	fresh.SetHeight(m.resTableHeight())
	fresh.MoveDown(n)
	m.resTable = fresh
	m.rebuildResultRows()
}

func (m tuiModel) resOutputRoom() int {
	used := 4 + m.resTableHeight()
	return max(2, m.height-used-2-m.statsRows())
}

func (m tuiModel) moveResLine(delta int) tuiModel {
	idx, ok := m.currentResult()
	if !ok {
		return m
	}
	n := len(m.resultDetail(idx))
	room := m.resOutputRoom()

	m.resLine = max(0, min(m.resLine+delta, n-1))
	if m.resLine < m.resScroll {
		m.resScroll = m.resLine
	}
	if m.resLine >= m.resScroll+room {
		m.resScroll = m.resLine - room + 1
	}
	m.resScroll = max(0, min(m.resScroll, max(0, n-room)))
	return m
}

func newResultsFilter() textinput.Model {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "filter completed"
	in.ShowSuggestions = true
	in.SetWidth(26)
	return in
}

func (m tuiModel) completedCount() int {
	n := 0
	for _, r := range m.results {
		if r.ElementID != "" {
			n++
		}
	}
	return n
}

func (m *tuiModel) applyResultFilter() {
	was := m.cursorResultID()

	q := strings.ToLower(strings.TrimSpace(m.resFilter.Value()))
	m.resMatches = m.resMatches[:0]

	var names []string
	for i, r := range m.results {
		if r.ElementID == "" {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.ElementID
		}
		names = append(names, name)

		if q == "" ||
			strings.Contains(strings.ToLower(name), q) ||
			strings.Contains(strings.ToLower(r.ElementID), q) ||
			strings.Contains(strings.ToLower(r.Model), q) {
			m.resMatches = append(m.resMatches, i)
		}
	}
	m.resFilter.SetSuggestions(names)
	m.rebuildResultRows()
	m.keepResultCursorOn(was)
}

func (m tuiModel) cursorResultID() string {
	if idx, ok := m.currentResult(); ok {
		return m.results[idx].ElementID
	}
	return ""
}

func (m *tuiModel) keepResultCursorOn(eid string) {
	if eid != "" {
		for row, i := range m.resMatches {
			if m.results[i].ElementID == eid {
				m.resTable.SetCursor(row)
				return
			}
		}
	}
	if c := m.resTable.Cursor(); c >= len(m.resMatches) {
		m.resTable.SetCursor(max(0, len(m.resMatches)-1))
	}
}

func (m *tuiModel) rebuildResultRows() {
	rows := make([]table.Row, 0, len(m.resMatches))
	at := m.resTable.Cursor()
	for n, i := range m.resMatches {
		r := m.results[i]
		here := n == at

		mark := ui.MarkCheck.String()
		if r.Error != "" {
			mark = ui.MarkCross.String()
		}
		caret := ""
		if here {
			caret, mark = ">", ui.Plain(mark)
		}
		name := r.Name
		if name == "" {
			name = r.ElementID
		}
		took := "-"
		if i < len(m.elapsed) && m.elapsed[i] > 0 {
			took = short(m.elapsed[i])
		}
		rows = append(rows, table.Row{
			caret,
			mark,
			ui.Truncate(name, 26),
			ui.Truncate(r.Model, 11),
			fmt.Sprintf("%d", len(r.Commands)),
			took,
		})
	}
	m.resTable.SetRows(rows)
	m.resTable.SetWidth(min(m.width, 72))
	m.resTable.SetHeight(m.resTableHeight())
}

func (m tuiModel) resTableHeight() int {
	room := m.height - 7 - m.statsRows()
	if m.resOpen {
		room = min(room, max(3, m.height/3))
	}
	return max(1, min(len(m.resMatches)+1, max(1, room)))
}

func (m tuiModel) currentResult() (int, bool) {
	c := m.resTable.Cursor()
	if c < 0 || c >= len(m.resMatches) {
		return 0, false
	}
	return m.resMatches[c], true
}

func (m tuiModel) onResultsKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		if key == "q" && m.resFilterOn {
			break
		}
		switch {
		case m.resOpen:
			m.resOpen = false
			m.resScroll, m.resLine = 0, 0
			m.rebuildResultRows()
		case m.resFilterOn || m.resFilter.Value() != "":
			m.resFilterOn = false
			m.resFilter.SetValue("")
			m.resFilter.Blur()
			m.applyResultFilter()
		default:
			m.phase = phasePicking
		}
		return m, nil

	case "up", "down", "k", "j", "home", "end":
		if key == "k" || key == "j" {
			if m.resFilterOn {
				break
			}
		}
		if m.resOpen {
			switch key {
			case "up", "k":
				return m.moveResLine(-1), nil
			case "down", "j":
				return m.moveResLine(1), nil
			case "home":
				return m.moveResLine(-1 << 30), nil
			case "end":
				return m.moveResLine(1 << 30), nil
			}
		}
		switch key {
		case "up", "k":
			m.moveResultCursor(-1)
		case "down", "j":
			m.moveResultCursor(1)
		case "home":
			m.moveResultCursor(-1 << 30)
		case "end":
			m.moveResultCursor(1 << 30)
		}
		return m, nil

	case "pgup", "pgdown", "space", " ":
		step := max(1, m.resOutputRoom()-1)
		if !m.resOpen {
			step = max(1, m.resTable.Height()-1)
		}
		if key == "pgup" {
			step = -step
		}
		if m.resOpen {
			return m.moveResLine(step), nil
		}
		m.moveResultCursor(step)
		return m, nil

	case "enter":
		if _, ok := m.currentResult(); ok {
			m.resOpen = !m.resOpen
			m.resScroll, m.resLine = 0, 0
			m.rebuildResultRows()
		}
		return m, nil

	case exportKey:
		return m.exportResults()

	case "f", "o", "s":
		if !m.resFilterOn {
			return m.onResultsOutputKey(key)
		}

	case "/":
		if !m.resFilterOn {
			m.resFilter.SetValue("")
			m.resFilterOn = true
			m.resOpen = false
			m.applyResultFilter()
			return m, m.resFilter.Focus()
		}
	}

	if m.resFilterOn {
		if key == "backspace" && m.resFilter.Value() == "" {
			m.resFilterOn = false
			m.resFilter.Blur()
			m.applyResultFilter()
			return m, nil
		}
		var cmd tea.Cmd
		m.resFilter, cmd = m.resFilter.Update(msg)
		m.applyResultFilter()
		return m, cmd
	}

	return m, nil
}

func (m tuiModel) onResultsOutputKey(key string) (tea.Model, tea.Cmd) {
	if key == "s" {
		m.status = m.saveBatchResults()
		if m.status == "" {
			m.status = "no folder for batch output — set one in settings"
		}
		return m, nil
	}

	switch key {
	case "f":
		m.cfg.BatchFormat = output.NextFormat(m.batchFormat())
	case "o":
		m.cfg.BatchOrient = output.NextOrientation(m.batchOrient())
	}
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.status = "format " + m.batchFormat()
	switch {
	case output.Tabular(m.batchFormat()):
		m.status += " · " + output.OrientationLabel(m.batchOrient())
	case key == "o":
		m.status += " · " + output.OrientationLabel(m.batchOrient()) + ", which only table and csv sort by"
	}
	m.status += " · s saves"
	return m, nil
}

func (m tuiModel) resultDetail(idx int) []string {
	r := m.results[idx]
	var lines []string

	if r.Error != "" {
		lines = append(lines, ui.StyleErr.Render(r.Error))
	}
	if r.HWID != "" || r.Model != "" {
		lines = append(lines, ui.StyleDim.Render(fmt.Sprintf("%s  %s  %s  connected=%v",
			r.HWID, r.Model, r.Software, r.Connected)))
	}
	for _, c := range r.Commands {
		lines = append(lines, "", ui.StyleCmd.Render(c.Command))
		for _, out := range strings.Split(strings.TrimRight(c.Output, "\n"), "\n") {
			lines = append(lines, "  "+out)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, ui.StyleDim.Render("no output"))
	}
	return lines
}

func (m tuiModel) viewResults() string {
	var b strings.Builder

	done := m.completedCount()
	header := ui.StyleCmd.Render("results") +
		ui.StyleDim.Render(fmt.Sprintf("   %d/%d done", done, len(m.ran)))
	if m.failed > 0 {
		header += ui.StyleErr.Render(fmt.Sprintf("  %d failed", m.failed))
	}
	tag := m.batchFormat()
	if output.Tabular(m.batchFormat()) {
		tag += " · " + output.OrientationLabel(m.batchOrient())
	}
	header += ui.StyleDim.Render("   " + tag)
	b.WriteString(ui.Truncate(header, max(10, m.width-1)) + "\n")

	if m.resFilterOn || m.resFilter.Value() != "" {
		line := m.resFilter.View()
		if m.resFilter.Value() != "" {
			line += ui.StyleDim.Render(fmt.Sprintf("   %d", len(m.resMatches)))
		}
		b.WriteString(line + "\n")
	} else {
		b.WriteString(ui.StyleDim.Render("press / to filter") + "\n")
	}

	if len(m.resMatches) == 0 {
		b.WriteString(ui.StyleDim.Render("  nothing completed yet") + "\n")
	} else {
		b.WriteString(m.resTable.View() + "\n")
	}

	if idx, ok := m.currentResult(); ok && m.resOpen {
		r := m.results[idx]
		name := r.Name
		if name == "" {
			name = r.ElementID
		}

		b.WriteString(ui.StyleDim.Render(strings.Repeat("─", max(10, min(m.width, 70)))) + "\n")
		b.WriteString(ui.StyleElement.Render(name) + "\n")

		lines := m.resultDetail(idx)
		room := m.resOutputRoom()
		width := max(10, m.width-1)
		start := min(m.resScroll, max(0, len(lines)-room))
		for i := start; i < len(lines) && i-start < room; i++ {
			if i == m.resLine {
				row := fmt.Sprintf("%-*s", width, ui.Truncate(ui.Plain(lines[i]), width))
				b.WriteString(ui.StyleRow.Render(row) + "\n")
				continue
			}
			b.WriteString(ui.Truncate(lines[i], width) + "\n")
		}
	}

	body := strings.TrimRight(b.String(), "\n")
	used := len(strings.Split(body, "\n"))
	for i := used; i < m.height-2-m.statsRows(); i++ {
		body += "\n"
	}
	if m.stats {
		body += "\n" + m.statsPane()
	}

	status := m.status
	hint := "↑↓/jk move · / filter · enter open · f/o format · s save · " + exportKeyHint + " run as json · esc/q back"
	if m.resOpen {
		hint = "↑↓/jk scroll · pgup/pgdn page · home/end ends · " + exportKeyHint + " output · esc/q close"
		if idx, ok := m.currentResult(); ok {
			where := fmt.Sprintf("line %d of %d", m.resLine+1, len(m.resultDetail(idx)))
			if status != "" {
				where += " · "
			}
			status = where + status
		}
	}
	return body + "\n" + ui.Footer(status, ui.Truncate(hint, max(10, m.width-1)))
}

func (m tuiModel) seedResultsForTest(rs []sase.Result, el []time.Duration) tuiModel {
	m.ran = make([]string, len(rs))
	for i, r := range rs {
		m.ran[i] = r.ElementID
	}
	m.results = rs
	m.elapsed = el
	m.phase = phaseResults
	m.applyResultFilter()
	return m
}
