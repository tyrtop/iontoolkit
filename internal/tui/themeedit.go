package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

const spinnerRole = "spinner"

const (
	randomRow  = "Randomize Everything!"
	similarRow = "Similar Theme"
	undoRow    = "Undo Last Change"
	animRow    = "Animation"
)

func actionRows() []string { return []string{randomRow, similarRow, undoRow} }

func editRows() []string {
	return append(append(ui.RoleNames(), spinnerRole, tabsRow, animRow), actionRows()...)
}

func editRoles() []string { return editRows() }

func isAction(row string) bool { return slices.Contains(actionRows(), row) }

func (m tuiModel) openThemeEdit() (tea.Model, tea.Cmd) {
	t, ok := m.themeAt(m.setCursor)
	if !ok {
		return m, nil
	}
	m.themeEdit = true
	m.themeDraft = t.Clone()
	m.themeStart = t.Clone()
	m.themeUndo = nil
	m.themeRow = 0
	m.status = ""
	return m, nil
}

const undoDepth = 64

func (m tuiModel) remember(before ui.Theme) tuiModel {
	m.themeUndo = append(slices.Clone(m.themeUndo), before.Clone())
	if len(m.themeUndo) > undoDepth {
		m.themeUndo = m.themeUndo[len(m.themeUndo)-undoDepth:]
	}
	return m
}

func (m tuiModel) onThemeEditKey(key string) (tea.Model, tea.Cmd) {
	if m.tabEdit {
		return m.onTabColorsKey(key)
	}
	if m.pick.on {
		return m.onPickerKey(key)
	}

	roles := editRoles()
	role := roles[min(max(m.themeRow, 0), len(roles)-1)]

	switch key {
	case "esc", "q", "tab":
		m.themeEdit = false
		m.status = ""
		return m.applyWorking()

	case "up", "k":
		m.themeRow = max(0, m.themeRow-1)
		return m, nil
	case "down", "j":
		m.themeRow = min(len(roles)-1, m.themeRow+1)
		return m, nil

	case "left", "h", "-":
		if isAction(role) || role == animRow || role == tabsRow {
			return m, nil
		}
		return m.stepRole(role, -1)
	case "right", "l", "+", " ", "space":
		if isAction(role) || role == animRow || role == tabsRow {
			return m, nil
		}
		return m.stepRole(role, 1)

	case "enter", "e":
		switch role {
		case spinnerRole:
			return m.stepRole(role, 1)
		case tabsRow:
			return m.openTabColors()
		case animRow:
			m.status = "animations are not wired up yet"
			return m, nil
		case randomRow:
			return m.randomiseDraft()
		case similarRow:
			return m.nudgeDraft()
		case undoRow:
			return m.undoDraft()
		}
		return m.openPicker(role)

	case "r":
		return m.randomiseDraft()
	case "s":
		return m.nudgeDraft()
	case "R":
		if isAction(role) || role == animRow || role == tabsRow {
			return m.randomiseDraft()
		}
		return m.randomiseRole(role)
	case "S":
		if isAction(role) || role == animRow || role == tabsRow {
			return m.nudgeDraft()
		}
		return m.nudgeRole(role)
	case "u":
		return m.undoDraft()

	case "t":
		if role == spinnerRole || role == animRow || role == tabsRow || isAction(role) {
			return m, nil
		}
		return m.openColorForm(role)

	case "ctrl+d", "x":
		if isAction(role) || role == animRow || role == tabsRow {
			return m, nil
		}
		return m.resetRole(role)
	}
	return m, nil
}

func (m tuiModel) randomiseDraft() (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	m.themeDraft = m.themeDraft.Clone()
	bg, fg, onLight := ui.RandomBackground()
	m.themeDraft.Mono = false
	for _, role := range ui.RoleNames() {
		switch role {
		case "background":
			_ = m.themeDraft.SetRole(role, bg)
		case "other":
			_ = m.themeDraft.SetRole(role, fg)
		default:
			_ = m.themeDraft.SetRole(role, ui.RandomColor(onLight))
		}
	}
	for i := range m.themeDraft.Tabs {
		m.themeDraft.Tabs[i] = ui.RandomColor(onLight)
	}
	m.themeDraft.Spinner = ui.RandomSpinner()
	return m.saveDraft()
}

func (m tuiModel) randomiseRole(role string) (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	if role == spinnerRole {
		m.themeDraft.Spinner = ui.RandomSpinner()
		return m.saveDraft()
	}
	if err := m.themeDraft.SetRole(role, ui.RandomColor(m.draftIsLight())); err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.saveDraft()
}

func (m tuiModel) nudgeDraft() (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	m.themeDraft = m.themeDraft.Clone()
	for _, role := range ui.RoleNames() {
		cur := m.themeDraft.Role(role)
		if cur == "" {
			continue
		}
		next := ui.SimilarColor(cur, 1)
		if role == "background" {
			next = ui.SimilarBackground(cur, 1)
		}
		_ = m.themeDraft.SetRole(role, next)
	}
	for i, c := range m.themeDraft.Tabs {
		m.themeDraft.Tabs[i] = ui.SimilarColor(c, 1)
	}
	return m.saveDraft()
}

func (m tuiModel) nudgeRole(role string) (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	if role == spinnerRole {
		m.themeDraft.Spinner = ui.NextSpinner(m.themeDraft.Spinner, 1)
		return m.saveDraft()
	}
	cur := m.themeDraft.Role(role)
	if cur == "" {
		return m.randomiseRole(role)
	}
	next := ui.SimilarColor(cur, 1)
	if role == "background" {
		next = ui.SimilarBackground(cur, 1)
	}
	if err := m.themeDraft.SetRole(role, next); err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.saveDraft()
}

func (m tuiModel) undoDraft() (tea.Model, tea.Cmd) {
	if len(m.themeUndo) == 0 {
		m.status = "nothing to undo"
		return m, nil
	}
	last := len(m.themeUndo) - 1
	m.themeDraft = m.themeUndo[last].Clone()
	m.themeUndo = m.themeUndo[:last]

	next, cmd := m.saveDraft()
	nm := next.(tuiModel)
	if nm.status == "" {
		left := "nothing left to undo"
		if n := len(nm.themeUndo); n > 0 {
			left = fmt.Sprintf("%d more to undo", n)
		}
		nm.status = "undone · " + left
	}
	return nm, cmd
}

func (m tuiModel) draftIsLight() bool {
	c, ok := ui.RGBOf(m.themeDraft.Bg)
	if !ok {
		return false
	}
	_, _, v := ui.HSVOf(c)
	return v > 0.5
}

func (m tuiModel) stepRole(role string, delta int) (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	if role == spinnerRole {
		m.themeDraft.Spinner = ui.NextSpinner(m.themeDraft.Spinner, delta)
	} else {
		next := ui.NextInPalette(m.themeDraft.Role(role), delta)
		if err := m.themeDraft.SetRole(role, next); err != nil {
			m.status = err.Error()
			return m, nil
		}
	}
	return m.saveDraft()
}

func (m tuiModel) resetRole(role string) (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	base, ok := ui.BuiltinTheme(m.themeDraft.Name)
	if !ok {
		m.status = m.themeDraft.Name + " is not a built-in, so there is nothing to go back to"
		return m, nil
	}
	if role == spinnerRole {
		m.themeDraft.Spinner = base.Spinner
	} else if err := m.themeDraft.SetRole(role, base.Role(role)); err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.saveDraft()
}

func (m tuiModel) openColorForm(role string) (tea.Model, tea.Cmd) {
	f := newField(role, "#89b4fa or 0-255", false)
	f.input.SetValue(m.themeDraft.Role(role))
	m.formThemeRole = role
	return m.openForm(formColor, "Color for "+role, phaseAddUser, []formField{f})
}

func (m tuiModel) submitColor() (tea.Model, tea.Cmd) {
	m = m.remember(m.themeDraft)
	if err := m.themeDraft.SetRole(m.formThemeRole, m.formValue(0)); err != nil {
		m.status = err.Error()
		return m, nil
	}
	next, cmd := m.saveDraft()
	nm := next.(tuiModel)
	if nm.status != "" && strings.Contains(nm.status, "save") {
		return nm, cmd
	}
	nm.form, nm.formKind = nil, formNone
	nm.phase = phaseSettings
	if nm.pick.on {
		cols, rows := nm.wheelSize()
		nm.pick.x, nm.pick.y, nm.pick.val = ui.WheelCell(nm.formValue(0), cols, rows)
		if r, c, ok := ui.GridCell(nm.formValue(0)); ok {
			nm.pick.row, nm.pick.col = r, c
		}
	}
	return nm, cmd
}

func (m tuiModel) saveDraft() (tea.Model, tea.Cmd) {
	if err := m.saveTheme(m.themeDraft); err != nil {
		m.status = "save failed: " + err.Error()
		return m, nil
	}
	return m.applyTheme(m.themeDraft)
}

func (m tuiModel) saveTheme(t ui.Theme) error {
	text, err := ui.ThemeYAML(t)
	if err != nil {
		return err
	}
	return m.runtime().Exec(&runtime.Call{Args: map[string]string{"text": text}}, "theme.save")
}

func (m tuiModel) themeEditContent() []string {
	if m.tabEdit {
		return m.tabColorsContent()
	}
	if m.pick.on {
		return m.pickerContent()
	}

	roles := editRoles()
	s := m.themeDraft.Styles()

	out := []string{
		ui.StyleCmd.Render("Editing "+m.themeDraft.Name) +
			ui.StyleDim.Render("  saved to "+shortenPath(app.ThemesPath(m.store()), 34)),
		"",
	}
	for i, role := range roles {
		cursor := "  "
		if i == m.themeRow {
			cursor = ui.StyleCmd.Render("> ")
		}
		if role == spinnerRole {
			out = append(out, cursor+ui.Label("Spinner")+" "+
				s.Spinner.Render(ui.SpinnerFrame(m.themeDraft.Spinner))+" "+
				ui.StyleElement.Render(m.themeDraft.Spinner))
			continue
		}

		if role == tabsRow {
			mode := m.tabModeName()
			out = append(out, cursor+ui.Label(role)+
				ui.StyleElement.Render(fmt.Sprintf("%-8s", mode))+
				ui.StyleDim.Render(fmt.Sprintf("%d color(s) · enter opens them",
					len(m.tabColors(listTabs)))))
			continue
		}

		if role == animRow {
			out = append(out, cursor+ui.StyleGhost.Render(ui.Label(role))+
				ui.StyleGhost.Render("placeholder")+
				ui.StyleDim.Render("    nothing animates yet"))
			continue
		}

		if isAction(role) {
			if role == actionRows()[0] {
				out = append(out, "")
			}
			out = append(out, cursor+m.actionRow(role))
			continue
		}

		value := m.themeDraft.Role(role)
		shown := value
		if shown == "" {
			shown = "unset"
		}
		sample := m.themeDraft.RoleStyle(role).Render("████")
		out = append(out, cursor+ui.Label(ui.RoleLabel(role))+" "+sample+" "+
			ui.StyleElement.Render(shown))
	}
	return append(out, "", ui.StyleDim.Render(
		"enter pick · ←→ cycle · t type · s/r whole theme · S/R this row · ctrl+d built-in · esc save"))
}

func (m tuiModel) actionRow(row string) string {
	switch row {
	case randomRow:
		return ui.StyleCmd.Render(row) +
			ui.StyleDim.Render("   every color, the tabs and the spinner")
	case similarRow:
		return ui.StyleCmd.Render(row) +
			ui.StyleDim.Render("           the same scheme, shifted")
	}
	if len(m.themeUndo) == 0 {
		return ui.StyleGhost.Render(row) + ui.StyleDim.Render("        nothing to undo yet")
	}
	return ui.StyleWarn.Render(row) +
		ui.StyleDim.Render(fmt.Sprintf("        u · %d step(s) back", len(m.themeUndo)))
}

var roleHelp = map[string]string{
	"ok":            "something worked, and the ✓",
	"err":           "something failed, and the ✗",
	"warn":          "worth noticing, not a failure",
	"cmd":           "headings and command text",
	"element":       "an element, tenant or value",
	"row":           "the row a list is on",
	"hot":           "a session about to go",
	"border":        "a modal's frame",
	"divider":       "the rule between panes",
	"background":    "the terminal's own background",
	"other":         "text no role has colored",
	"spinner_color": "every spinner",
}
