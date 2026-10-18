package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/ui"
)

func newResultsTable() table.Model {
	return table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "element", Width: 26},
			{Title: "model", Width: 11},
			{Title: "cmds", Width: 5},
			{Title: "took", Width: 9},
		}),
		table.WithFocused(true),
	)
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

	if c := m.resTable.Cursor(); c >= len(m.resMatches) {
		m.resTable.SetCursor(max(0, len(m.resMatches)-1))
	}
}

func (m *tuiModel) rebuildResultRows() {
	rows := make([]table.Row, 0, len(m.resMatches))
	for _, i := range m.resMatches {
		r := m.results[i]
		mark := ui.MarkCheck.String()
		if r.Error != "" {
			mark = ui.MarkCross.String()
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
			mark,
			ui.Truncate(name, 26),
			ui.Truncate(r.Model, 11),
			fmt.Sprintf("%d", len(r.Commands)),
			took,
		})
	}
	m.resTable.SetRows(rows)
	m.resTable.SetWidth(min(m.width, 70))
	m.resTable.SetHeight(m.resTableHeight())
}

func (m tuiModel) resTableHeight() int {
	room := m.height - 7
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
	case "esc":
		switch {
		case m.resOpen:
			m.resOpen = false
			m.resScroll = 0
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

	case "q":
		if !m.resFilterOn {
			return m, tea.Quit
		}

	case "up", "down", "home", "end":
		if m.resOpen {
			m.resOpen = false
			m.resScroll = 0
			m.rebuildResultRows()
		}
		var cmd tea.Cmd
		m.resTable, cmd = m.resTable.Update(msg)
		return m, cmd

	case "pgup":
		if m.resOpen {
			m.resScroll = max(0, m.resScroll-5)
			return m, nil
		}
	case "pgdown":
		if m.resOpen {
			m.resScroll += 5
			return m, nil
		}

	case "enter":
		if _, ok := m.currentResult(); ok {
			m.resOpen = !m.resOpen
			m.resScroll = 0
			m.rebuildResultRows()
		}
		return m, nil

	case "/":
		if !m.resFilterOn {
			m.resFilterOn = true
			m.resOpen = false
			m.rebuildResultRows()
			return m, m.resFilter.Focus()
		}
	}

	if m.resFilterOn {
		var cmd tea.Cmd
		m.resFilter, cmd = m.resFilter.Update(msg)
		m.applyResultFilter()
		return m, cmd
	}

	var cmd tea.Cmd
	m.resTable, cmd = m.resTable.Update(msg)
	return m, cmd
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
	b.WriteString(header + "\n")

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
		used := len(strings.Split(strings.TrimRight(b.String(), "\n"), "\n"))
		room := max(2, m.height-used-2)
		start := min(m.resScroll, max(0, len(lines)-room))
		for i := start; i < len(lines) && i-start < room; i++ {
			b.WriteString(ui.Truncate(lines[i], max(10, m.width-1)) + "\n")
		}
		if rest := len(lines) - start - room; rest > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more lines (pgdn)", rest)) + "\n")
		}
	}

	body := strings.TrimRight(b.String(), "\n")
	used := len(strings.Split(body, "\n"))
	for i := used; i < m.height-2; i++ {
		body += "\n"
	}

	hint := "↑↓ move · / filter · enter open · esc back · q quit"
	if m.resOpen {
		hint = "↑↓ close and move · pgup/pgdn scroll · esc close · q quit"
	}
	return body + "\n" + ui.Footer(m.status, hint)
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
