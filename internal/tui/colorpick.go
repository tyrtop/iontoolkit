package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"tyrtop.com/iontk/internal/ui"
)

type colorPick struct {
	on   bool
	role string
	was  string
	grid bool

	list  string
	idx   int
	label string

	x, y int
	val  float64

	row, col int
}

const (
	wheelMinRows = 5
	wheelMaxRows = 11
	previewLines = 13
)

func (m tuiModel) wheelSize() (cols, rows int) {
	spare := max(6, m.height-6) - previewLines - 5
	rows = min(wheelMaxRows, max(wheelMinRows, spare))
	cols = rows * 2
	if w := max(20, m.width-16) - 4; cols > w {
		cols = max(11, w)
	}
	return cols, rows
}

func (m tuiModel) openTabPicker(list string, i int, label string) (tea.Model, tea.Cmd) {
	next, cmd := m.openPickerOn(colorPick{
		on: true, list: list, idx: i, label: label,
		was: m.tabColorAt(list, i),
	})
	return next, cmd
}

func (m tuiModel) openPicker(role string) (tea.Model, tea.Cmd) {
	return m.openPickerOn(colorPick{
		on: true, role: role, label: ui.RoleLabel(role), was: m.themeDraft.Role(role),
	})
}

func (m tuiModel) openPickerOn(p colorPick) (tea.Model, tea.Cmd) {
	cols, rows := m.wheelSize()

	p.grid = !ui.TrueColor()
	if r, c, ok := ui.GridCell(p.was); ok {
		p.grid, p.row, p.col = true, r, c
	}
	p.x, p.y, p.val = ui.WheelCell(p.was, cols, rows)

	m.pick = p
	m.status = ""
	return m, nil
}

func (m tuiModel) onPickerKey(key string) (tea.Model, tea.Cmd) {
	cols, rows := m.wheelSize()

	switch key {
	case "esc", "q":
		label, was := m.pick.label, m.pick.was
		nm, err := m.setPicked(was)
		if err != nil {
			nm.status = err.Error()
		}
		m = nm
		m.pick = colorPick{}
		m.status = label + " left as " + describeColor(was)
		return m.applyWorking()

	case "enter":
		was := m.pick.was
		kept := m.themeDraft.Clone()
		if back, err := m.setPicked(was); err == nil {
			m = m.remember(back.themeDraft)
		}
		m.themeDraft = kept
		m.pick = colorPick{}
		return m.saveDraft()

	case "tab":
		m.pick.grid = !m.pick.grid
		cur := m.themeDraft.Role(m.pick.role)
		if m.pick.grid {
			idx := cur
			if _, _, ok := ui.GridCell(cur); !ok {
				idx = strconv.Itoa(ui.NearestIndex(cur))
			}
			m.pick.row, m.pick.col, _ = ui.GridCell(idx)
			return m, nil
		}
		m.pick.x, m.pick.y, m.pick.val = ui.WheelCell(cur, cols, rows)
		return m, nil

	case "t":
		if m.pick.list != "" {
			m.status = "tab a color off the chart, or type one into a role"
			return m, nil
		}
		return m.openColorForm(m.pick.role)

	case "ctrl+d", "x":
		if m.pick.list != "" {
			return m, nil
		}
		next, cmd := m.resetRole(m.pick.role)
		nm := next.(tuiModel)
		if !nm.pick.on {
			return nm, cmd
		}
		nm.pick.x, nm.pick.y, nm.pick.val =
			ui.WheelCell(nm.themeDraft.Role(nm.pick.role), cols, rows)
		if r, c, ok := ui.GridCell(nm.themeDraft.Role(nm.pick.role)); ok {
			nm.pick.row, nm.pick.col = r, c
		}
		return nm, cmd
	}

	if m.pick.grid {
		return m.moveInGrid(key)
	}
	return m.moveOnWheel(key, cols, rows)
}

func (m tuiModel) moveOnWheel(key string, cols, rows int) (tea.Model, tea.Cmd) {
	dx, dy := 0, 0
	switch key {
	case "left", "h":
		dx = -1
	case "right", "l":
		dx = 1
	case "up", "k":
		dy = -1
	case "down", "j":
		dy = 1

	case "+", "=", "pgup", "K":
		m.pick.val = min(1, m.pick.val+0.08)
		return m.applyPicked()
	case "-", "_", "pgdown", "J":
		m.pick.val = max(0, m.pick.val-0.08)
		return m.applyPicked()
	default:
		return m, nil
	}

	x, y := m.pick.x+dx, m.pick.y+dy
	if ui.WheelAt(x, y, cols, rows, m.pick.val) == "" {
		return m, nil
	}
	m.pick.x, m.pick.y = x, y
	return m.applyPicked()
}

func (m tuiModel) moveInGrid(key string) (tea.Model, tea.Cmd) {
	chart := ui.Grid256()
	switch key {
	case "left", "h":
		m.pick.col = max(0, m.pick.col-1)
	case "right", "l":
		m.pick.col = min(len(chart[m.pick.row])-1, m.pick.col+1)
	case "up", "k":
		m.pick.row = max(0, m.pick.row-1)
	case "down", "j":
		m.pick.row = min(len(chart)-1, m.pick.row+1)
	default:
		return m, nil
	}
	m.pick.col = min(m.pick.col, len(chart[m.pick.row])-1)
	return m.applyPicked()
}

func (m tuiModel) pickedValue() string {
	if m.pick.grid {
		chart := ui.Grid256()
		row := min(max(m.pick.row, 0), len(chart)-1)
		col := min(max(m.pick.col, 0), len(chart[row])-1)
		return strconv.Itoa(chart[row][col])
	}
	cols, rows := m.wheelSize()
	return ui.WheelAt(m.pick.x, m.pick.y, cols, rows, m.pick.val)
}

func (m tuiModel) applyPicked() (tea.Model, tea.Cmd) {
	v := m.pickedValue()
	if v == "" {
		return m, nil
	}
	nm, err := m.setPicked(v)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	return nm.applyWorking()
}

func (m tuiModel) setPicked(v string) (tuiModel, error) {
	if m.pick.list != "" {
		return m.setTabColor(m.pick.list, m.pick.idx, v)
	}
	return m, m.themeDraft.SetRole(m.pick.role, v)
}

func (m tuiModel) pickedCurrent() string {
	if m.pick.list != "" {
		return m.tabColorAt(m.pick.list, m.pick.idx)
	}
	return m.themeDraft.Role(m.pick.role)
}

func (m tuiModel) applyWorking() (tea.Model, tea.Cmd) {
	if m.themeEdit {
		return m.applyTheme(m.themeDraft)
	}
	t, _ := ui.ThemeByName(m.cfg.Theme)
	return m.applyTheme(t)
}

func (m tuiModel) pickedStyle() lipgloss.Style {
	if m.pick.list != "" {
		return ui.ColorStyle(m.pickedCurrent())
	}
	return m.themeDraft.RoleStyle(m.pick.role)
}

func describeColor(spec string) string {
	if strings.TrimSpace(spec) == "" {
		return "unset"
	}
	return spec
}

func (m tuiModel) pickerContent() []string {
	role := m.pick.role
	value := m.pickedCurrent()
	shown := value
	if shown == "" {
		shown = "unset"
	}

	out := []string{
		ui.StyleCmd.Render("Picking "+m.pick.label) + " " +
			m.pickedStyle().Render("████") + " " +
			ui.StyleElement.Render(shown) +
			ui.StyleDim.Render("   "+roleHelp[role]),
		"",
	}

	if m.pick.grid {
		out = append(out, m.chartLines()...)
	} else {
		out = append(out, m.wheelLines()...)
		out = append(out, "", m.valueRamp())
	}

	where := "disc"
	if m.pick.grid {
		where = "256 chart"
	}
	return append(out, "",
		ui.StyleDim.Render("↑↓←→ move on the "+where+
			" · tab the other one · enter keep it · esc put it back"))
}

func (m tuiModel) wheelLines() []string {
	cols, rows := m.wheelSize()
	cursorRow, cursorCol := m.pick.y/2, m.pick.x

	out := make([]string, 0, rows)
	for r := range rows {
		var b strings.Builder
		for x := range cols {
			top := ui.WheelAt(x, r*2, cols, rows, m.pick.val)
			bottom := ui.WheelAt(x, r*2+1, cols, rows, m.pick.val)

			if r == cursorRow && x == cursorCol {
				under := ui.WheelAt(m.pick.x, m.pick.y, cols, rows, m.pick.val)
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(ui.Contrast(under))).
					Background(lipgloss.Color(under)).Render("+"))
				continue
			}

			switch {
			case top == "" && bottom == "":
				b.WriteString(" ")
			case top == "":
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(bottom)).Render("▄"))
			case bottom == "":
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(top)).Render("▀"))
			default:
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(top)).
					Background(lipgloss.Color(bottom)).Render("▀"))
			}
		}
		out = append(out, " "+b.String())
	}
	return out
}

func (m tuiModel) valueRamp() string {
	cols, rows := m.wheelSize()
	hue := ui.WheelAt(m.pick.x, m.pick.y, cols, rows, 1)
	var b strings.Builder
	for i := range 10 {
		v := float64(i+1) / 10
		c, _ := ui.RGBOf(hue)
		h, s, _ := ui.HSVOf(c)
		cell := "██"
		if int(m.pick.val*10+0.5) == i+1 {
			cell = "▐▌"
		}
		b.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(ui.HexFromHSV(h, s, v))).Render(cell))
	}
	return " " + b.String() + ui.StyleDim.Render(
		fmt.Sprintf("  +/- brightness %d%%", int(m.pick.val*100+0.5)))
}

func (m tuiModel) chartLines() []string {
	chart := ui.Grid256()
	out := make([]string, 0, len(chart)+2)
	for r, cells := range chart {
		var b strings.Builder
		for c, idx := range cells {
			spec := strconv.Itoa(idx)
			if r == m.pick.row && c == m.pick.col {
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(ui.Contrast(spec))).
					Background(lipgloss.Color(spec)).Render("+"))
				continue
			}
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(spec)).Render("█"))
		}
		out = append(out, " "+b.String())
	}
	return append(out, "",
		ui.StyleDim.Render(" the first sixteen, then the cube, then the greys"))
}
