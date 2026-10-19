package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/ui"
)

// prints the clock too: a zone set months ago is only checkable against it
func (m tuiModel) zoneLine() string {
	if p := clock.Problem(); p != "" {
		return ui.StyleWarn.Render(ui.Truncate(p, 44)) + ui.StyleDim.Render("  times are UTC")
	}
	return ui.StyleElement.Render(clock.Name()) +
		ui.StyleDim.Render("  now "+clock.Stamp(time.Now()))
}

// 600 IANA names have no order to walk, so only the typed one is kept
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

// a zone that will not resolve is a typo, so the saved value stays
func (m tuiModel) applyZone(spec string) (tuiModel, bool) {
	if err := clock.Valid(spec); err != nil {
		m.status = err.Error()
		return m, false
	}
	m.cfg.Zone = spec
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
		return m, false
	}
	_ = clock.Use(spec)
	if !strings.EqualFold(spec, clock.UTC) && !strings.EqualFold(spec, clock.Local) {
		m.typedZone = spec
	}
	m.status = "times are now " + clock.Stamp(time.Now())
	return m, true
}

func (m tuiModel) openZoneForm() (tea.Model, tea.Cmd) {
	f := newField("zone", "America/Chicago", false)
	f.input.SetValue(m.cfg.Zone)
	return m.openForm(formZone, "Zone for printed times", phaseAddUser, []formField{f})
}

func (m tuiModel) submitZone() (tea.Model, tea.Cmd) {
	nm, ok := m.applyZone(m.formValue(0))
	if !ok {
		// the form stays up with the reason on it
		return nm, nil
	}
	nm.form, nm.formKind = nil, formNone
	nm.phase = nm.formReturn
	return nm, nil
}

func (m tuiModel) themeContent() []string {
	if m.themeEdit {
		return append(m.themeEditContent(), m.themePreview(m.themeDraft)...)
	}

	themes := ui.Themes()
	cur := ui.CurrentTheme()

	out := make([]string, 0, len(themes)+10)
	for _, t := range themes {
		// padded plain, or the swatches drift row by row
		name := ui.StyleElement.Render(fmt.Sprintf("%-12s", ui.Truncate(t.Name, 12)))
		tag := ""
		if strings.EqualFold(t.Name, cur.Name) {
			tag = ui.StyleOK.Render("  (in use)")
		}
		// nothing to say about a scheme this terminal can show
		if ui.ThemeNote(t) != "" {
			tag += ui.StyleWarn.Render("  approximated here")
		}
		out = append(out, name+t.Swatch()+ui.StyleDim.Render("  "+t.Blurb)+tag)
	}

	if err := ui.ThemesErr(); err != "" {
		out = append(out, "", ui.StyleErr.Render(ui.Truncate(err, 60)))
	}

	// under the cursor, not in use: a preview is for before choosing
	at := min(max(m.setCursor, 0), len(themes)-1)
	if m.setFocus != focusContent {
		at = max(0, indexOfTheme(themes, cur.Name))
	}
	return append(out, m.themePreview(themes[at])...)
}

func indexOfTheme(themes []ui.Theme, name string) int {
	for i, t := range themes {
		if strings.EqualFold(t.Name, name) {
			return i
		}
	}
	return -1
}

// fixture-shaped rows: a swatch says what the colours are, a pane says
// what they look like.
var previewRows = []struct {
	name, site, model, state string
	ok, selected             bool
}{
	{"store-0003-rtr-01", "Store 0003", "ion 1200", "connected", true, true},
	{"store-0003-rtr-02", "Store 0003", "ion 1200-s", "connected", true, false},
	{"store-0100-rtr-01", "Store 0100", "ion 3200", "offline", false, false},
}

// a page of iontk drawn in one scheme
func (m tuiModel) themePreview(t ui.Theme) []string {
	s := t.Styles()
	width := max(40, min(m.width-20, 64))

	out := []string{
		"",
		ui.StyleDim.Render("preview · ") + ui.StyleElement.Render(t.Name),
		// tab colours are identity, not status
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
		// styled on its own: a highlight around the mark ends at its reset
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
			s.Dim.Render(" · alt+y copy · ")+s.Hot.Render("session closing"),
		" "+s.Spinner.Render(spinnerFrame(t))+s.Dim.Render(" reading interfaces")+
			s.Dim.Render("   spinner: ")+s.Element.Render(spinnerLabel(t)),
	)
	if note := ui.ThemeNote(t); note != "" {
		out = append(out, " "+ui.StyleWarn.Render(ui.Truncate(note, width)))
	}

	depth := ui.ProfileName()
	if depth == "" {
		depth = "not reported yet"
	}
	return append(out, "", ui.Label("terminal")+ui.StyleDim.Render(depth))
}

func previewTabs(t ui.Theme) string {
	s := t.Styles()
	labels := []string{" elements (shell) ", " store-0003-rtr-01 ", " store-0100-rtr-01 · data "}
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		st := s.Tabs[i%len(s.Tabs)]
		if i == 0 {
			st = st.Reverse(true).Bold(true)
		}
		parts = append(parts, st.Render(label))
	}
	return " " + strings.Join(parts, " ")
}

func spinnerFrame(t ui.Theme) string {
	return ui.SpinnerFrame(t.Spinner)
}

func spinnerLabel(t ui.Theme) string { return ui.SpinnerName(t.Spinner) }

func (m tuiModel) onThemeKey(key string) (tea.Model, tea.Cmd) {
	if m.themeEdit {
		return m.onThemeEditKey(key)
	}
	if key == "e" {
		return m.openThemeEdit()
	}
	if key != "enter" && key != " " && key != "space" {
		return m, nil
	}
	themes := ui.Themes()
	if m.setCursor < 0 || m.setCursor >= len(themes) {
		return m, nil
	}
	t := themes[m.setCursor]

	m.cfg.Theme = t.Name
	if err := m.store.SaveConfig(m.cfg); err != nil {
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

// the tables and the spinner copied a style when built, so rebuild them
func (m tuiModel) applyTheme(t ui.Theme) (tea.Model, tea.Cmd) {
	ui.Apply(t)
	m.table.SetStyles(tableStyles())
	m.resTable.SetStyles(tableStyles())
	return m.withSpinner()
}

func profileLabel() string {
	if n := ui.ProfileName(); n != "" {
		return n
	}
	return "depth not reported"
}
