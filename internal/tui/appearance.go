package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"tyrtop.com/iontk/internal/runtime"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/ui"
)

func (m tuiModel) zoneLine() string {
	if p := clock.Problem(); p != "" {
		return ui.StyleWarn.Render(ui.Truncate(p, 44)) + ui.StyleDim.Render("  times are UTC")
	}
	return ui.StyleElement.Render(clock.Name()) +
		ui.StyleDim.Render("  now "+clock.Stamp(time.Now()))
}

func (m tuiModel) cycleZone() (tea.Model, tea.Cmd) {
	order := []string{clock.UTC, clock.Local}
	if m.typedZone != "" {
		order = append(order, m.typedZone)
	}

	next := order[0]
	for i, z := range order {
		if strings.EqualFold(z, clock.Name()) {
			next = order[(i+1)%len(order)]
			break
		}
	}
	nm, _ := m.applyZone(next)
	return nm, nil
}

func (m tuiModel) applyZone(spec string) (tuiModel, bool) {
	if err := m.runtime().Set("zone", spec); err != nil {
		m.status = err.Error()
		return m, false
	}
	if !strings.EqualFold(spec, clock.UTC) && !strings.EqualFold(spec, clock.Local) {
		m.typedZone = spec
	}
	m.status = "times are now " + clock.Stamp(time.Now())
	return m, true
}

func (m tuiModel) themeAt(cursor int) (ui.Theme, bool) {
	themes := ui.Themes()
	if cursor < 1 || cursor > len(themes) {
		return ui.Theme{}, false
	}
	return themes[cursor-1], true
}

func (m tuiModel) themeContent() []string {
	if m.themeEdit {
		return append(m.themeEditContent(), m.themePreview(m.themeDraft)...)
	}

	themes := ui.Themes()
	cur := ui.CurrentTheme()

	out := make([]string, 0, len(themes)+10)
	const newRow = "+ New Theme"
	out = append(out, ui.StyleGhost.Render(newRow))

	for _, t := range themes {
		plain := fmt.Sprintf("%-12s", ui.Truncate(t.Name, 12))
		name := ui.StyleElement.Render(plain)
		tag := ""
		if strings.EqualFold(t.Name, cur.Name) {
			name = ui.StyleRow.Render(plain)
		}
		if ui.ThemeNote(t) != "" {
			tag += ui.StyleWarn.Render("  approximated here")
		}
		out = append(out, name+t.Swatch()+ui.StyleDim.Render("  "+t.Blurb)+tag)
	}

	if err := ui.ThemesErr(); err != "" {
		out = append(out, "", ui.StyleErr.Render(ui.Truncate(err, 60)))
	}

	out = append(out, "", ui.StyleDim.Render(m.themeControls()))

	at := max(0, indexOfTheme(themes, cur.Name))
	if m.setFocus == focusContent {
		if _, ok := m.themeAt(m.setCursor); ok {
			at = m.setCursor - 1
		}
	}
	return append(out, m.themePreview(themes[min(at, len(themes)-1)])...)
}

func indexOfTheme(themes []ui.Theme, name string) int {
	for i, t := range themes {
		if strings.EqualFold(t.Name, name) {
			return i
		}
	}
	return -1
}

var previewRows = []struct {
	name, site, model, state string
	ok, selected             bool
}{
	{"store-0003-rtr-01", "Store 0003", "ion 1200", "connected", true, true},
	{"store-0003-rtr-02", "Store 0003", "ion 1200-s", "connected", true, false},
	{"store-0100-rtr-01", "Store 0100", "ion 3200", "offline", false, false},
}

func (m tuiModel) themePreview(t ui.Theme) []string {
	s := t.Styles()
	width := max(40, min(m.width-20, 64))

	out := []string{
		"",
		ui.StyleDim.Render("Preview · ") + ui.StyleElement.Render(t.Name),
		previewTabs(t),
	}

	for i, r := range previewRows {
		mark := s.OK.SetString("✓").String()
		state := s.OK.Render(r.state)
		if !r.ok {
			mark = s.Err.SetString("✗").String()
			state = s.Warn.Render(r.state)
		}
		box := "[ ]"
		if r.selected {
			box = s.OK.Render("[x]")
		}
		block := fmt.Sprintf("%-20s%-12s%-11s", r.name, r.site, r.model)
		style := s.Element
		if i == 0 {
			style = s.Row
		}
		out = append(out, " "+mark+" "+box+" "+style.Render(block)+" "+state)
	}

	out = append(out,
		" "+s.Cmd.Render("dump device status")+
			s.Dim.Render("   3 elements · 1 selected"),
		" "+s.Warn.Render("ion offline")+
			s.Dim.Render(" · "+exportKeyHint+" · ")+s.Hot.Render("session closing"),
		" "+s.Spinner.Render(m.previewFrame(t))+s.Dim.Render(" reading interfaces")+
			s.Dim.Render("   spinner: ")+s.Element.Render(spinnerLabel(t))+
			" "+s.Spinner.Render(m.previewFrame(t)),
	)
	if note := ui.ThemeNote(t); note != "" {
		out = append(out, " "+ui.StyleWarn.Render(ui.Truncate(note, width)))
	}

	depth := ui.ProfileName()
	if depth == "" {
		depth = "not reported yet"
	}
	return append(out, "", ui.Label("Terminal")+ui.StyleDim.Render(depth))
}

func previewTabs(t ui.Theme) string {
	was := ui.CurrentTheme()
	ui.Apply(t)
	defer ui.Apply(was)

	type tab struct {
		kind      ui.TabKind
		ring, nth int
		label     string
	}
	bar := []tab{
		{ui.TabList, 0, 0, " elements "},
		{ui.TabShell, 1, 0, " rtr-01 "},
		{ui.TabShell, 2, 1, " rtr-02 "},
		{ui.TabData, 3, 0, " rtr-01 · data "},
		{ui.TabData, 4, 1, " rtr-02 · data "},
	}

	parts := make([]string, 0, len(bar))
	for _, b := range bar {
		parts = append(parts, ui.StyleTab(b.kind, b.ring, b.nth, b.ring == 0).Render(b.label))
	}
	return " " + strings.Join(parts, " ")
}

func spinnerLabel(t ui.Theme) string { return ui.SpinnerName(t.Spinner) }

func (m tuiModel) onThemeKey(key string) (tea.Model, tea.Cmd) {
	if m.themeEdit {
		return m.onThemeEditKey(key)
	}
	armed := m.themeDel
	m.themeDel = ""

	t, onScheme := m.themeAt(m.setCursor)

	if key == "x" || key == "ctrl+d" {
		if !onScheme {
			return m, nil
		}
		if strings.EqualFold(armed, t.Name) {
			return m.deleteTheme(t)
		}
		m.themeDel = t.Name
		m.status = "x again to " + m.deleteWord(t) + " " + t.Name
		return m, nil
	}

	if key == "e" {
		if !onScheme {
			return m.openNewThemeForm()
		}
		return m.openThemeEdit()
	}
	if key != "enter" && key != " " && key != "space" {
		return m, nil
	}
	if !onScheme {
		return m.openNewThemeForm()
	}

	if err := m.runtime().Set("theme", t.Name); err != nil {
		m.status = err.Error()
		return m, nil
	}
	next, cmd := m.applyTheme(t)
	nm := next.(tuiModel)
	nm.status = t.Name
	if note := ui.ThemeNote(t); note != "" {
		nm.status += " — " + note
	}
	return nm, cmd
}

func (m tuiModel) applyTheme(t ui.Theme) (tea.Model, tea.Cmd) {
	cur := ui.CurrentTheme()
	repaint := t.Bg != cur.Bg || t.Fg != cur.Fg

	ui.Apply(t)
	m.table.SetStyles(tableStyles())
	m.resTable.SetStyles(tableStyles())

	next, cmd := m.withSpinner()
	if repaint {
		return next, tea.Batch(cmd, tea.ClearScreen)
	}
	return next, cmd
}

func profileLabel() string {
	if n := ui.ProfileName(); n != "" {
		return n
	}
	return "depth not reported"
}

func (m tuiModel) openNewThemeForm() (tea.Model, tea.Cmd) {
	f := newField("name", "my scheme", false)
	return m.openForm(formNewTheme, "New scheme, copied from "+ui.CurrentTheme().Name,
		phaseAddUser, []formField{f})
}

func (m tuiModel) submitNewTheme() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.formValue(0))
	switch {
	case name == "":
		m.status = "a scheme needs a name"
		return m, nil
	case len(name) > 40:
		m.status = "that name is too long for the picker"
		return m, nil
	}
	if _, taken := ui.ThemeByName(name); taken {
		m.status = name + " already exists — edit it with e"
		return m, nil
	}

	t := ui.CurrentTheme()
	t.Name, t.Blurb = name, "copied from "+ui.CurrentTheme().Name
	t.Tabs = slices.Clone(t.Tabs)
	if err := m.saveTheme(t); err != nil {
		m.status = err.Error()
		return m, nil
	}

	nm := m
	nm.form, nm.formKind = nil, formNone
	nm.phase = phaseSettings
	nm.setFocus = focusContent
	for i, th := range ui.Themes() {
		if strings.EqualFold(th.Name, name) {
			nm.setCursor = i + 1
		}
	}
	next, _ := nm.openThemeEdit()
	out, cmd := next.(tuiModel).saveDraft()
	o := out.(tuiModel)
	if o.status == "" || !strings.Contains(o.status, "fail") {
		o.status = name
	}
	return o, cmd
}

func (m tuiModel) themeControls() string {
	if m.themeEdit {
		return ""
	}
	if _, onScheme := m.themeAt(m.setCursor); !onScheme {
		return "enter new scheme · ↑↓ move · esc back"
	}
	return "↑↓ move · enter select · e edit · x delete · esc back"
}

func (m tuiModel) previewFrame(t ui.Theme) string {
	return ui.SpinnerFrameAt(t.Spinner, m.spinTick)
}

func (m tuiModel) deleteWord(t ui.Theme) string {
	_, shipped := ui.BuiltinTheme(t.Name)
	switch {
	case ui.IsUserTheme(app.ThemesPath(m.store()), t.Name) && shipped:
		return "drop your changes to"
	case ui.IsUserTheme(app.ThemesPath(m.store()), t.Name):
		return "delete"
	}
	return "hide"
}

func (m tuiModel) deleteTheme(t ui.Theme) (tea.Model, tea.Cmd) {
	var said []string
	if err := m.runtime().Exec(&runtime.Call{Args: map[string]string{"name": t.Name}, Out: func(l string) { said = append(said, l) }}, "theme.hide"); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.setCursor = min(m.setCursor, max(0, m.sectionRows()-1))
	now, _ := ui.ThemeByName(m.cfg().Theme)
	next, cmd := m.applyTheme(now)
	nm := next.(tuiModel)
	nm.status = strings.Join(said, ", ")
	return nm, cmd
}

func (m tuiModel) restoreShippedThemes() (tea.Model, tea.Cmd) {
	var said []string
	if err := m.runtime().Exec(&runtime.Call{Out: func(l string) { said = append(said, l) }}, "theme.restore"); err != nil {
		m.status = err.Error()
		return m, nil
	}
	now, _ := ui.ThemeByName(m.cfg().Theme)
	next, cmd := m.applyTheme(now)
	nm := next.(tuiModel)
	nm.status = strings.Join(said, " ")
	return nm, cmd
}
