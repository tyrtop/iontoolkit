package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

// pinZone and pinTheme put the process state back: both are global, so a test
// that left one set would decide what every later test rendered.
func pinZone(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = clock.Use(clock.UTC) })
}

func pinTheme(t *testing.T) {
	t.Helper()
	// LoadThemes("") is also how the set goes back to the built-ins, which a
	// test that saved an override has to do.
	t.Cleanup(func() {
		_ = ui.LoadThemes("")
		ui.Apply(ui.Default())
	})
}

// The times row is where someone finds out their panes are five hours out, so
// it has to show the clock and not only the zone's name.
func TestTimesRowShowsTheZoneAndTheClock(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection = secGeneral
	m.setFocus = focusContent

	out := plain(m.viewSettings())
	if !strings.Contains(out, "times") || !strings.Contains(out, "UTC") {
		t.Errorf("the General section does not say what zone times are in:\n%s", out)
	}

	// space cycles; enter on the same row types a name instead.
	m.setCursor = zoneRow
	mm, _ := m.onGeneralKey("space")
	got := mm.(tuiModel)
	if got.cfg.Zone != clock.Local {
		t.Errorf("cfg.Zone = %q, want the cycle to reach local", got.cfg.Zone)
	}
	saved, err := got.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Zone != clock.Local {
		t.Errorf("saved zone = %q, want it persisted", saved.Zone)
	}
	if clock.Name() != clock.Local {
		t.Errorf("clock.Name() = %q, want the change to apply now", clock.Name())
	}
}

// Cycling has to be able to get back to a typed zone: there are 600 IANA
// names and no order to walk them in.
func TestTheCycleKeepsTheTypedZone(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow

	m, ok := m.applyZone("America/Chicago")
	if !ok {
		t.Fatalf("applyZone() refused a real zone: %s", m.status)
	}

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		mm, _ := m.onGeneralKey("space")
		m = mm.(tuiModel)
		seen[m.cfg.Zone] = true
	}
	for _, want := range []string{clock.UTC, clock.Local, "America/Chicago"} {
		if !seen[want] {
			t.Errorf("the cycle never reached %q: %v", want, seen)
		}
	}
}

func TestEnterOnTheTimesRowOpensTheZoneForm(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow

	mm, _ := m.onGeneralKey("enter")
	m = mm.(tuiModel)
	if m.formKind != formZone {
		t.Fatalf("formKind = %v, want the zone form", m.formKind)
	}

	m.form[0].input.SetValue("Europe/London")
	mm, _ = m.submitZone()
	m = mm.(tuiModel)
	if m.formKind != formNone {
		t.Error("the form stayed open after a zone that loads")
	}
	if clock.Name() != "Europe/London" {
		t.Errorf("clock.Name() = %q, want the typed zone", clock.Name())
	}
}

// A zone nobody can resolve is a typo. The form keeps it and says why rather
// than saving it and printing UTC from then on.
func TestAZoneThatWillNotLoadIsRefusedByTheForm(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow

	mm, _ := m.onGeneralKey("enter")
	m = mm.(tuiModel)
	m.form[0].input.SetValue("Mars/Olympus_Mons")
	mm, _ = m.submitZone()
	m = mm.(tuiModel)

	if m.formKind != formZone {
		t.Error("the form closed on a zone that does not exist")
	}
	if m.status == "" {
		t.Error("nothing said why the zone was refused")
	}
	if m.cfg.Zone != "" {
		t.Errorf("cfg.Zone = %q, want the saved value left alone", m.cfg.Zone)
	}
}

func TestThemeSectionListsEveryThemeAndMarksTheOneInUse(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection = secTheme
	m.setFocus = focusContent

	out := plain(m.viewSettings())
	for _, th := range ui.Themes() {
		if !strings.Contains(out, th.Name) {
			t.Errorf("the Theme section is missing %q:\n%s", th.Name, out)
		}
	}
	if !strings.Contains(out, "(in use)") {
		t.Errorf("nothing marks the theme in use:\n%s", out)
	}
	if got := m.sectionRows(); got != len(ui.Themes()) {
		t.Errorf("sectionRows() = %d, want one per theme", got)
	}
	t.Log("\n" + m.viewSettings())
}

func TestPickingAThemeAppliesAndPersistsIt(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent

	// mono is the one whose effect is visible without colour support.
	want := 0
	for i, th := range ui.Themes() {
		if th.Name == "mono" {
			want = i
		}
	}
	m.setCursor = want

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if ui.CurrentTheme().Name != "mono" {
		t.Errorf("CurrentTheme() = %q, want mono", ui.CurrentTheme().Name)
	}
	saved, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Theme != "mono" {
		t.Errorf("saved theme = %q, want it persisted", saved.Theme)
	}
	// The spinner carries a style built when it was made, so the change has to
	// rebuild it -- and that comes with a tick.
	if cmd == nil {
		t.Error("picking a theme issued no command, so the spinner kept the old one")
	}
	if ui.StyleOK.Render("x") != "x" {
		t.Errorf("mono still colours StyleOK: %q", ui.StyleOK.Render("x"))
	}
}

// A theme saved by a later version, or deleted from one, must not stop the TUI
// opening.
func TestAnUnknownSavedThemeOpensInTheDefault(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.cfg.Theme = "something from the future"
	if err := m.store.SaveConfig(m.cfg); err != nil {
		t.Fatal(err)
	}

	ui.UseTheme(m.cfg.Theme)
	if ui.CurrentTheme().Name != "classic" {
		t.Errorf("CurrentTheme() = %q, want classic", ui.CurrentTheme().Name)
	}
}

// A swatch says what the colours are; only a pane says what they look like in
// the thing people read. The preview follows the cursor, not the scheme in
// use, because its job is showing one before you pick it.
func TestThePreviewFollowsTheCursorNotTheThemeInUse(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent

	themes := ui.Themes()
	last := len(themes) - 1
	m.setCursor = last

	out := plain(m.viewSettings())
	if !strings.Contains(out, "preview · "+themes[last].Name) {
		t.Errorf("the preview is not of the scheme under the cursor:\n%s", out)
	}
	// Something recognisable from a real page, with fixture-shaped data.
	for _, want := range []string{"store-0003-rtr-01", "Store 0003", "ion 1200",
		"connected", "offline", "dump device status", "elements (shell)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview is missing %q:\n%s", want, out)
		}
	}
	if ui.CurrentTheme().Name == themes[last].Name {
		t.Fatal("this test needs the cursor on a scheme that is not in use")
	}

	// On the nav pane there is no cursor in the list, so it shows the one in use.
	m.setFocus = focusNav
	if out := plain(m.viewSettings()); !strings.Contains(out, "preview · "+ui.CurrentTheme().Name) {
		t.Errorf("with the nav focused the preview should be the scheme in use:\n%s", out)
	}
}

// The preview must draw in the previewed scheme, not the one applied, or it
// would show the same thing for every row.
func TestThePreviewRendersInTheSchemeItNames(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.width, m.height = 110, 40

	mono, _ := ui.ThemeByName("mono")
	nord, _ := ui.ThemeByName("nord")

	monoLines := strings.Join(m.themePreview(mono), "\n")
	nordLines := strings.Join(m.themePreview(nord), "\n")
	if monoLines == nordLines {
		t.Error("two schemes previewed identically")
	}
	// nord's green is #a6e3a1-adjacent hex, which lands on an RGB escape.
	if !strings.Contains(nordLines, "38;2;") {
		t.Errorf("the nord preview has no truecolor in it:\n%q", nordLines)
	}
	if strings.Contains(monoLines, "38;2;") {
		t.Errorf("the mono preview emitted colour:\n%q", monoLines)
	}
	// Each scheme names its own spinner shape, and shows a frame of it.
	if !strings.Contains(plain(monoLines), mono.Spinner) {
		t.Errorf("the preview does not name mono's spinner %q", mono.Spinner)
	}
}

// The spinner moved into the theme, so picking one has to change the shape the
// model is actually ticking.
func TestPickingAThemeChangesTheSpinnerShape(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent

	themes := ui.Themes()
	want := -1
	for i, th := range themes {
		if th.Name == "Ben's Theme" {
			want = i
		}
	}
	if want < 0 {
		t.Fatal("no theme called Ben's Theme")
	}
	m.setCursor = want

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if cmd == nil {
		t.Error("picking a theme issued no command, so the spinner was not rebuilt")
	}
	if got := ui.CurrentTheme().Spinner; got != "monkey" {
		t.Errorf("spinner = %q, want the monkey the scheme asks for", got)
	}
	// The model's own spinner is the one that ticks, so it has to have changed.
	if got := plain(m.spinner.View()); got == "" {
		t.Error("the model's spinner renders nothing after the change")
	}
}

// A themes.yaml beside the config adds schemes, which is what makes the format
// worth having: --themes is the same escape hatch --views is.
func TestAUserThemesFileReachesThePicker(t *testing.T) {
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = "" })

	path := filepath.Join(t.TempDir(), ui.ThemesFileName)
	err := os.WriteFile(path, []byte(`
themes:
  - name: nocturne
    blurb: mine
    ok: "2"
    element: "4"
    row: "5"
    spinner: globe
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	app.ThemesFile = path

	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	m := newTUIModel(context.Background(), st, &config.Config{Theme: "nocturne"})
	m.width, m.height = 110, 40
	m.phase, m.setSection = phaseSettings, secTheme

	if got := ui.CurrentTheme().Name; got != "nocturne" {
		t.Errorf("CurrentTheme() = %q, want the scheme from the file", got)
	}
	if got := plain(m.viewSettings()); !strings.Contains(got, "nocturne") {
		t.Errorf("the picker does not list the file's scheme:\n%s", got)
	}
	if got := app.ThemesPath(st); got != path {
		t.Errorf("ThemesPath() = %q, want --themes to win", got)
	}
}
