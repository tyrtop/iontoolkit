package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

func pinZone(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = clock.Use(clock.UTC) })
}

func pinTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = ui.LoadThemes("")
		ui.Apply(ui.Default())
	})
}

func TestTimesRowShowsTheZoneAndTheClock(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection = secGeneral
	m.setFocus = focusContent

	out := plain(m.viewSettings())
	if !strings.Contains(out, "Times") || !strings.Contains(out, "UTC") {
		t.Errorf("the General section does not say what zone times are in:\n%s", out)
	}

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

func TestEnterOnTheTimesRowOpensTheZonePicker(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow

	mm, _ := m.onGeneralKey("enter")
	m = mm.(tuiModel)
	if m.phase != phaseZone {
		t.Fatalf("phase = %v, want the zone picker", m.phase)
	}
	if len(m.zonePick.items) < 100 {
		t.Fatalf("the picker offers %d zones, want the whole list to choose from", len(m.zonePick.items))
	}

	m = typeString(t, m, "london")
	if len(m.zonePick.items) == 0 {
		t.Fatal("a fuzzy query matched no zone at all")
	}
	if got, _ := m.zonePick.current(); got != "Europe/London" {
		t.Errorf("highlighted %q, want Europe/London first for %q", got, "london")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.phase != phaseSettings {
		t.Errorf("phase = %v, want to return to settings", m.phase)
	}
	if clock.Name() != "Europe/London" {
		t.Errorf("clock.Name() = %q, want the picked zone", clock.Name())
	}
	if m.cfg.Zone != "Europe/London" {
		t.Errorf("cfg.Zone = %q, want the pick persisted", m.cfg.Zone)
	}
}

func TestTheZonePickerScrollsAndPreviewsTheHighlightedZone(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow
	mm, _ := m.onGeneralKey("enter")
	m = mm.(tuiModel)

	first, _ := m.zonePick.current()
	for i := 0; i < suggestRows+2; i++ {
		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		m = mm.(tuiModel)
	}
	later, ok := m.zonePick.current()
	if !ok || later == first {
		t.Errorf("the list did not move with the cursor: %q then %q", first, later)
	}

	start, end := m.zonePick.window(suggestRows)
	if m.zonePick.cursor < start || m.zonePick.cursor >= end {
		t.Errorf("cursor %d is outside the shown window %d..%d, so it scrolled off screen",
			m.zonePick.cursor, start, end)
	}

	out := plain(m.viewZonePick())
	if !strings.Contains(out, later) {
		t.Errorf("the highlighted zone is not on the page:\n%s", out)
	}
	if !strings.Contains(out, "now") {
		t.Errorf("no clock preview to check the pick against:\n%s", out)
	}
}

func TestAZoneThatWillNotLoadIsRefusedByThePicker(t *testing.T) {
	pinZone(t)
	m := settingsModel(t)
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusContent, zoneRow
	mm, _ := m.onGeneralKey("enter")
	m = mm.(tuiModel)

	m = typeString(t, m, "Mars/Olympus_Mons")
	if _, ok := m.zonePick.current(); ok {
		t.Fatal("a zone that does not exist matched the list")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.phase != phaseZone {
		t.Error("the picker closed on a zone that does not exist")
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
	if !strings.Contains(out, "") {
		t.Errorf("nothing marks the theme in use:\n%s", out)
	}
	if got := m.sectionRows(); got != len(ui.Themes())+1 {
		t.Errorf("sectionRows() = %d, want %d", got, len(ui.Themes())+1)
	}
	if !strings.Contains(out, "+ New Theme") {
		t.Errorf("there is no way to make a scheme from the picker:\n%s", out)
	}
	t.Log("\n" + m.viewSettings())
}

func TestPickingAThemeAppliesAndPersistsIt(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent

	want := 0
	for i, th := range ui.Themes() {
		if th.Name == "mono" {
			want = i + 1
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
	if cmd == nil {
		t.Error("picking a theme issued no command, so the spinner kept the old one")
	}
	if ui.StyleOK.Render("x") != "x" {
		t.Errorf("mono still colors StyleOK: %q", ui.StyleOK.Render("x"))
	}
}

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

func TestThePreviewFollowsTheCursorNotTheThemeInUse(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent

	themes := ui.Themes()
	last := len(themes) - 1
	m.setCursor = last + 1

	out := plain(m.viewSettings())
	if !strings.Contains(out, "Preview · "+themes[last].Name) {
		t.Errorf("the preview is not of the scheme under the cursor:\n%s", out)
	}
	for _, want := range []string{"store-0003-rtr-01", "Store 0003", "ion 1200",
		"connected", "offline", "dump device status", "elements"} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview is missing %q:\n%s", want, out)
		}
	}
	if ui.CurrentTheme().Name == themes[last].Name {
		t.Fatal("this test needs the cursor on a scheme that is not in use")
	}

	m.setFocus = focusNav
	if out := plain(m.viewSettings()); !strings.Contains(out, "Preview · "+ui.CurrentTheme().Name) {
		t.Errorf("with the nav focused the preview should be the scheme in use:\n%s", out)
	}
}

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
	if !strings.Contains(nordLines, "38;2;") {
		t.Errorf("the nord preview has no truecolor in it:\n%q", nordLines)
	}
	if strings.Contains(monoLines, "38;2;") {
		t.Errorf("the mono preview emitted color:\n%q", monoLines)
	}
	if !strings.Contains(plain(monoLines), mono.Spinner) {
		t.Errorf("the preview does not name mono's spinner %q", mono.Spinner)
	}
}

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
	m.setCursor = want + 1

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if cmd == nil {
		t.Error("picking a theme issued no command, so the spinner was not rebuilt")
	}
	if got := ui.CurrentTheme().Spinner; got != "monkey" {
		t.Errorf("spinner = %q, want the monkey the scheme asks for", got)
	}
	if got := plain(m.spinner.View()); got == "" {
		t.Error("the model's spinner renders nothing after the change")
	}
}

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

func TestDeletingASchemeTakesTwoPresses(t *testing.T) {
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = "" })

	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent

	if err := ui.SaveUserTheme(app.ThemesPath(m.store),
		ui.Theme{Name: "workbench", OK: "2", Spinner: "dot"}); err != nil {
		t.Fatal(err)
	}
	at := 0
	for i, th := range ui.Themes() {
		if th.Name == "workbench" {
			at = i + 1
		}
	}
	if at == 0 {
		t.Fatal("the scheme was not written where the picker reads from")
	}
	m.setCursor = at

	next, _ := m.onSettingsKey("x")
	m = next.(tuiModel)
	if _, ok := ui.ThemeByName("workbench"); !ok {
		t.Fatal("one x deleted the scheme")
	}
	if !strings.Contains(m.status, "x again") {
		t.Errorf("status = %q, want it to ask for the second press", m.status)
	}

	next, _ = m.onSettingsKey("up")
	m = next.(tuiModel)
	m.setCursor = at
	next, _ = m.onSettingsKey("x")
	m = next.(tuiModel)
	if _, ok := ui.ThemeByName("workbench"); !ok {
		t.Error("the arm survived a cursor move, so x deleted on one press")
	}

	next, _ = m.onSettingsKey("x")
	m = next.(tuiModel)
	if _, ok := ui.ThemeByName("workbench"); ok {
		t.Errorf("the scheme is still there: %s", m.status)
	}
	if !strings.Contains(m.status, "gone") {
		t.Errorf("status = %q after deleting", m.status)
	}
	if m.setCursor >= m.sectionRows() {
		t.Errorf("the cursor is past the end of a shorter list: %d of %d",
			m.setCursor, m.sectionRows())
	}
}

func TestAShippedSchemeIsHiddenRatherThanRemoved(t *testing.T) {
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = ""; ui.HideThemes(nil) })

	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent
	for i, th := range ui.Themes() {
		if th.Name == "desert" {
			m.setCursor = i + 1
		}
	}

	next, _ := m.onSettingsKey("x")
	m = next.(tuiModel)
	if !strings.Contains(m.status, "hide") {
		t.Errorf("status = %q, want it to offer to hide the scheme", m.status)
	}
	next, _ = m.onSettingsKey("x")
	m = next.(tuiModel)

	if _, ok := ui.ThemeByName("desert"); ok {
		t.Error("desert is still in the picker after being hidden")
	}
	if !strings.Contains(m.status, "Reset") {
		t.Errorf("status = %q, want it to say where it comes back from", m.status)
	}
	saved, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(saved.HiddenThemes, "desert") {
		t.Errorf("hidden = %v, want desert in it", saved.HiddenThemes)
	}

	next, _ = m.restoreShippedThemes()
	m = next.(tuiModel)
	if _, ok := ui.ThemeByName("desert"); !ok {
		t.Errorf("desert did not come back: %s", m.status)
	}
	saved, err = m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.HiddenThemes) != 0 {
		t.Errorf("hidden = %v after restoring, want none", saved.HiddenThemes)
	}
}

func TestRestoringTheShippedSchemesKeepsYourOwn(t *testing.T) {
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = ""; ui.HideThemes(nil) })

	m := settingsModel(t)
	m.setSection, m.setFocus = secTheme, focusContent
	dir := app.ThemesPath(m.store)

	if err := ui.SaveUserTheme(dir, ui.Theme{Name: "nord", OK: "#ffffff", Spinner: "dot"}); err != nil {
		t.Fatal(err)
	}
	if err := ui.SaveUserTheme(dir, ui.Theme{Name: "workbench", OK: "2", Spinner: "dot"}); err != nil {
		t.Fatal(err)
	}

	next, _ := m.restoreShippedThemes()
	m = next.(tuiModel)

	got, _ := ui.ThemeByName("nord")
	if got.OK == "#ffffff" {
		t.Error("the override of a shipped scheme survived the restore")
	}
	if _, ok := ui.ThemeByName("workbench"); !ok {
		t.Errorf("a scheme of the user's own was taken with it: %s", m.status)
	}
	if !strings.Contains(m.status, "override") {
		t.Errorf("status = %q, want it to say what it did", m.status)
	}
}

func TestTheThemeSectionPrintsItsOwnControls(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	m.setCursor = 1

	out := plain(m.viewSettings())
	for _, want := range []string{"enter select", "e edit", "x delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("the controls do not offer %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tab back") {
		t.Errorf("the controls still mention tab:\n%s", out)
	}
	if got := strings.Count(out, "enter select"); got != 1 {
		t.Errorf("the controls are printed %d times, want once", got)
	}

	m.setCursor = 0
	if out := plain(m.viewSettings()); !strings.Contains(out, "enter new scheme") {
		t.Errorf("the new-scheme row does not say what enter does:\n%s", out)
	}
}

func TestThePreviewSpinnerAnimatesAndNamesItsShapeFirst(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	for i, th := range ui.Themes() {
		if th.Name == "Ben's Theme" {
			m.setCursor = i + 1
		}
	}

	out := plain(m.viewSettings())
	at := strings.Index(out, "spinner: monkey")
	if at < 0 {
		t.Fatalf("the preview does not name the shape:\n%s", out)
	}
	rest := out[at+len("spinner: monkey"):]
	if frame := strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0]); frame == "" {
		t.Errorf("nothing is drawn after the shape's name: %q", rest)
	}

	first := m.previewFrame(ui.Theme{Spinner: "minidot"})
	m.spinTick += 2
	if second := m.previewFrame(ui.Theme{Spinner: "minidot"}); second == first {
		t.Errorf("the preview frame did not advance: %q twice", first)
	}
}
