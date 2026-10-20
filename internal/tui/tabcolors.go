package tui

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

const (
	tabsRow    = "Tab Colors"
	tabModeRow = "Mode"
	listTabs   = "tabs"
	listShll   = "shell"
	listData   = "data"
)

type tabTarget struct {
	label string
	list  string
	idx   int
}

func (m tuiModel) tabTargets() []tabTarget {
	out := []tabTarget{{label: tabModeRow}}

	entries := func(list, word string, colors []string, grow bool) {
		for i := range colors {
			label := word
			if len(colors) > 1 || grow {
				label = fmt.Sprintf("%s %d", word, i+1)
			}
			out = append(out, tabTarget{label: label, list: list, idx: i})
		}
		if grow {
			out = append(out, tabTarget{label: "+ Another " + word, list: list, idx: -1})
		}
	}

	switch m.themeDraft.TabMode {
	case ui.TabByType:
		entries(listShll, "Shell", m.tabColors(listShll)[:1], false)
		entries(listData, "Rich Data", m.tabColors(listData)[:1], false)
	case ui.TabBoth:
		entries(listShll, "Shell", m.tabColors(listShll), true)
		entries(listData, "Rich Data", m.tabColors(listData), true)
	default:
		entries(listTabs, "Tab", m.tabColors(listTabs), true)
	}
	return out
}

func (m tuiModel) tabColors(list string) []string {
	var out []string
	switch list {
	case listShll:
		out = m.themeDraft.TabShell
	case listData:
		out = m.themeDraft.TabData
	default:
		out = m.themeDraft.Tabs
	}
	if len(out) == 0 {
		base := m.themeDraft.Tabs
		if len(base) == 0 {
			return []string{""}
		}
		at := ui.TabFallback(ui.TabShell)
		if list == listData {
			at = ui.TabFallback(ui.TabData)
		}
		return []string{base[at%len(base)]}
	}
	return out
}

func (m tuiModel) tabColorAt(list string, i int) string {
	set := m.tabColors(list)
	if i < 0 || i >= len(set) {
		return ""
	}
	return set[i]
}

func (m tuiModel) setTabColor(list string, i int, spec string) (tuiModel, error) {
	if err := ui.CheckColor(spec); err != nil {
		return m, err
	}
	set := slices.Clone(m.tabColors(list))
	if i < 0 || i >= len(set) {
		return m, fmt.Errorf("no tab color %d", i+1)
	}
	set[i] = spec

	switch list {
	case listShll:
		m.themeDraft.TabShell = set
	case listData:
		m.themeDraft.TabData = set
	default:
		m.themeDraft.Tabs = set
	}
	return m, nil
}

func (m tuiModel) openTabColors() (tea.Model, tea.Cmd) {
	m.tabEdit = true
	m.tabRow = 0
	m.status = ""
	return m, nil
}

func (m tuiModel) onTabColorsKey(key string) (tea.Model, tea.Cmd) {
	if m.pick.on {
		return m.onPickerKey(key)
	}

	rows := m.tabTargets()
	at := rows[min(max(m.tabRow, 0), len(rows)-1)]

	switch key {
	case "esc", "q", "tab":
		m.tabEdit = false
		m.status = ""
		return m, nil

	case "up", "k":
		m.tabRow = max(0, m.tabRow-1)
		return m, nil
	case "down", "j":
		m.tabRow = min(len(rows)-1, m.tabRow+1)
		return m, nil

	case "left", "h", "-":
		if at.list == "" {
			return m.cycleTabMode(-1)
		}
		return m, nil
	case "right", "l", "+", " ", "space":
		if at.list == "" {
			return m.cycleTabMode(1)
		}
		return m, nil

	case "enter", "e":
		switch {
		case at.list == "":
			return m.cycleTabMode(1)
		case at.idx < 0:
			return m.growTabList(at.list)
		}
		return m.openTabPicker(at.list, at.idx, at.label)

	case "S":
		if at.list == "" {
			return m, nil
		}
		return m.nudgeTabColor(at, false)
	case "R":
		if at.list == "" {
			return m, nil
		}
		return m.nudgeTabColor(at, true)

	case "ctrl+d", "x", "backspace":
		if at.list == "" || at.idx < 0 {
			return m, nil
		}
		return m.dropTabColor(at)
	}
	return m, nil
}

func (m tuiModel) cycleTabMode(delta int) (tea.Model, tea.Cmd) {
	modes := ui.TabModes()
	at := max(0, slices.Index(modes, m.tabModeName()))
	next := modes[((at+delta)%len(modes)+len(modes))%len(modes)]

	m = m.remember(m.themeDraft)
	m.themeDraft = m.themeDraft.Clone()
	m.themeDraft.TabMode = next
	m.tabRow = 0
	out, cmd := m.saveDraft()
	nm := out.(tuiModel)
	if nm.status == "" {
		nm.status = next + " · " + ui.TabModeBlurb[next]
	}
	return nm, cmd
}

func (m tuiModel) tabModeName() string {
	if m.themeDraft.TabMode == "" {
		return ui.TabRotate
	}
	return m.themeDraft.TabMode
}

func (m tuiModel) growTabList(list string) (tea.Model, tea.Cmd) {
	set := slices.Clone(m.tabColors(list))
	if len(set) >= maxTabColors {
		m.status = fmt.Sprintf("%d is as many as a rotation holds", maxTabColors)
		return m, nil
	}
	set = append(set, ui.SimilarColor(set[len(set)-1], 1))

	m = m.remember(m.themeDraft)
	m.themeDraft = m.themeDraft.Clone()
	switch list {
	case listShll:
		m.themeDraft.TabShell = set
	case listData:
		m.themeDraft.TabData = set
	default:
		m.themeDraft.Tabs = set
	}
	return m.saveDraft()
}

const maxTabColors = 12

func (m tuiModel) dropTabColor(at tabTarget) (tea.Model, tea.Cmd) {
	set := slices.Clone(m.tabColors(at.list))
	if len(set) <= 1 {
		m.status = "a rotation needs one color"
		return m, nil
	}
	set = slices.Delete(set, at.idx, at.idx+1)

	m = m.remember(m.themeDraft)
	m.themeDraft = m.themeDraft.Clone()
	switch at.list {
	case listShll:
		m.themeDraft.TabShell = set
	case listData:
		m.themeDraft.TabData = set
	default:
		m.themeDraft.Tabs = set
	}
	m.tabRow = max(1, min(m.tabRow, len(m.tabTargets())-1))
	return m.saveDraft()
}

func (m tuiModel) nudgeTabColor(at tabTarget, roll bool) (tea.Model, tea.Cmd) {
	if at.idx < 0 {
		return m, nil
	}
	cur := m.tabColorAt(at.list, at.idx)
	next := ui.SimilarColor(cur, 1)
	if roll || cur == "" {
		next = ui.RandomColor(m.draftIsLight())
	}

	m = m.remember(m.themeDraft)
	nm, err := m.setTabColor(at.list, at.idx, next)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	return nm.saveDraft()
}

func (m tuiModel) tabColorsContent() []string {
	if m.pick.on {
		return m.pickerContent()
	}

	mode := m.tabModeName()
	out := []string{
		ui.StyleCmd.Render("Tab Colors · "+m.themeDraft.Name) +
			ui.StyleDim.Render("  the bar across the top, once something is open"),
		"",
	}

	for i, at := range m.tabTargets() {
		cursor := "  "
		if i == m.tabRow {
			cursor = ui.StyleCmd.Render("> ")
		}

		if at.list == "" {
			out = append(out, cursor+ui.Label(tabModeRow)+
				ui.StyleElement.Render(fmt.Sprintf("%-8s", mode))+
				ui.StyleDim.Render(ui.TabModeBlurb[mode]))
			continue
		}
		if at.idx < 0 {
			out = append(out, cursor+ui.StyleGhost.Render(at.label))
			continue
		}

		spec := m.tabColorAt(at.list, at.idx)
		shown := spec
		if shown == "" {
			shown = "unset"
		}
		out = append(out, cursor+ui.Label(at.label)+" "+
			ui.ColorStyle(spec).Render("████")+" "+
			ui.StyleElement.Render(shown))
	}

	return append(out, "", ui.StyleDim.Render(
		"↑↓ row · ←→ mode · enter pick · S/R nudge or roll · ctrl+d drop · esc back"))
}
