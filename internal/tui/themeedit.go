package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

const spinnerRole = "spinner"

func editRoles() []string { return append(ui.RoleNames(), spinnerRole) }

// editing a built-in writes an override; the embedded file is not writable
func (m tuiModel) openThemeEdit() (tea.Model, tea.Cmd) {
	themes := ui.Themes()
	if m.setCursor < 0 || m.setCursor >= len(themes) {
		return m, nil
	}
	m.themeEdit = true
	m.themeDraft = themes[m.setCursor]
	m.themeRow = 0
	m.status = ""
	return m, nil
}

func (m tuiModel) onThemeEditKey(key string) (tea.Model, tea.Cmd) {
	roles := editRoles()
	role := roles[min(max(m.themeRow, 0), len(roles)-1)]

	switch key {
	case "esc", "q", "tab":
		m.themeEdit = false
		m.status = ""
		return m, nil

	case "up", "k":
		m.themeRow = max(0, m.themeRow-1)
		return m, nil
	case "down", "j":
		m.themeRow = min(len(roles)-1, m.themeRow+1)
		return m, nil

	// no typing, and the preview shows each value as it passes
	case "left", "h", "-":
		return m.stepRole(role, -1)
	case "right", "l", "+", " ", "space":
		return m.stepRole(role, 1)

	case "enter":
		if role == spinnerRole {
			return m.stepRole(role, 1)
		}
		return m.openColourForm(role)

	// the only way back once a built-in is overridden
	case "ctrl+d", "x":
		return m.resetRole(role)
	}
	return m, nil
}

func (m tuiModel) stepRole(role string, delta int) (tea.Model, tea.Cmd) {
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

func (m tuiModel) openColourForm(role string) (tea.Model, tea.Cmd) {
	f := newField(role, "#89b4fa or 0-255", false)
	f.input.SetValue(m.themeDraft.Role(role))
	m.formThemeRole = role
	return m.openForm(formColour, "Colour for "+role, phaseAddUser, []formField{f})
}

// a colour that will not parse would render as nothing
func (m tuiModel) submitColour() (tea.Model, tea.Cmd) {
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
	return nm, cmd
}

// applied as well as saved, so the whole frame shows the edit
func (m tuiModel) saveDraft() (tea.Model, tea.Cmd) {
	path := app.ThemesPath(m.store)
	if err := ui.SaveUserTheme(path, m.themeDraft); err != nil {
		m.status = "save failed: " + err.Error()
		return m, nil
	}
	m.cfg.Theme = m.themeDraft.Name
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.applyTheme(m.themeDraft)
}

func (m tuiModel) themeEditContent() []string {
	roles := editRoles()
	s := m.themeDraft.Styles()

	out := []string{
		ui.StyleCmd.Render("editing "+m.themeDraft.Name) +
			ui.StyleDim.Render("  saved to "+shortenPath(app.ThemesPath(m.store), 34)),
		"",
	}
	for i, role := range roles {
		cursor := "  "
		if i == m.themeRow {
			cursor = ui.StyleCmd.Render("> ")
		}
		if role == spinnerRole {
			out = append(out, cursor+ui.Label(role)+" "+
				s.Spinner.Render(ui.SpinnerFrame(m.themeDraft.Spinner))+" "+
				ui.StyleElement.Render(m.themeDraft.Spinner))
			continue
		}

		value := m.themeDraft.Role(role)
		shown := value
		if shown == "" {
			shown = "unset"
		}
		// the only way to tell #585b70 from #45475a
		sample := m.themeDraft.RoleStyle(role).Render("████")
		// "spinner_color" fills LabelWidth exactly, leaving no gap
		out = append(out, cursor+ui.Label(role)+" "+sample+" "+
			ui.StyleElement.Render(fmt.Sprintf("%-9s", shown))+
			ui.StyleDim.Render(roleHelp[role]))
	}
	return append(out, "", ui.StyleDim.Render(
		"←→ cycle · enter type one · ctrl+d back to the built-in · esc done"))
}

// "hot" and "row" mean nothing until you know where they are drawn
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
	"spinner_color": "every spinner",
}
