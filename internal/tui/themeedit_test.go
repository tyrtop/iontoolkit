package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

func editorOn(t *testing.T, name string) tuiModel {
	t.Helper()
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = "" })

	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	for i, th := range ui.Themes() {
		if th.Name == name {
			m.setCursor = i + 1
		}
	}

	next, _ := m.onSettingsKey("e")
	m = next.(tuiModel)
	if !m.themeEdit {
		t.Fatalf("e did not open the editor on %s", name)
	}
	return m
}

func TestEditingAThemeWritesAnOverrideAndAppliesIt(t *testing.T) {
	m := editorOn(t, "classic")

	before := m.themeDraft.OK
	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if m.themeDraft.OK == before {
		t.Fatalf("ok is still %q after a step along the palette", before)
	}
	if m.status != "" {
		t.Errorf("status = %q, want the edit to have gone through", m.status)
	}

	if got := ui.CurrentTheme().OK; got != m.themeDraft.OK {
		t.Errorf("CurrentTheme().OK = %q, want the edit %q", got, m.themeDraft.OK)
	}
	if ui.MarkCheck.String() == "✓" {
		t.Error("the ✓ was not restyled, so Apply did not reach the marks")
	}

	dir := app.ThemesPath(m.store())
	path := filepath.Join(dir, ui.ThemeFileName("classic"))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s was not written: %v", ui.ThemeFileName("classic"), err)
	}
	if !strings.Contains(string(b), "classic") {
		t.Errorf("the file does not hold the override:\n%s", b)
	}
	if err := ui.LoadThemes(dir); err != nil {
		t.Fatalf("the file it wrote will not load: %v", err)
	}
	reloaded, _ := ui.ThemeByName("classic")
	if reloaded.OK != m.themeDraft.OK {
		t.Errorf("reloaded ok = %q, want %q", reloaded.OK, m.themeDraft.OK)
	}
	if _, ok := ui.ThemeByName("nord"); !ok {
		t.Error("the override took the rest of the set with it")
	}
	saved, err := m.store().Config()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Theme != "classic" {
		t.Errorf("saved theme = %q, want the edited one", saved.Theme)
	}
}

func TestTypingAColorIsValidated(t *testing.T) {
	m := editorOn(t, "nord")
	next, _ := m.onSettingsKey("down")
	m = next.(tuiModel)
	next, _ = m.onSettingsKey("t")
	m = next.(tuiModel)
	if m.formKind != formColor {
		t.Fatalf("formKind = %v, want the color form", m.formKind)
	}

	m.form[0].input.SetValue("chartreuse")
	next, _ = m.submitColor()
	m = next.(tuiModel)
	if m.formKind != formColor {
		t.Error("the form closed on a color that will not parse")
	}
	if m.status == "" {
		t.Error("nothing said why the color was refused")
	}

	m.form[0].input.SetValue("#ff00aa")
	next, _ = m.submitColor()
	m = next.(tuiModel)
	if m.formKind != formNone {
		t.Errorf("the form stayed open on a good color: %s", m.status)
	}
	if got := m.themeDraft.Err; got != "#ff00aa" {
		t.Errorf("err = %q, want the typed color", got)
	}
	if got := ui.CurrentTheme().Err; got != "#ff00aa" {
		t.Errorf("CurrentTheme().Err = %q, want the typed color applied", got)
	}
}

func TestAnEditedRoleCanGoBackToTheBuiltin(t *testing.T) {
	m := editorOn(t, "classic")
	original := m.themeDraft.OK

	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if m.themeDraft.OK == original {
		t.Fatal("nothing changed, so there is nothing to undo")
	}

	next, _ = m.onSettingsKey("ctrl+d")
	m = next.(tuiModel)
	if m.themeDraft.OK != original {
		t.Errorf("ok = %q after ctrl+d, want the built-in's %q", m.themeDraft.OK, original)
	}
}

func TestTheEditorCyclesTheSpinnerShape(t *testing.T) {
	m := editorOn(t, "classic")
	roles := editRoles()
	m.themeRow = slices.Index(roles, spinnerRole)
	if m.themeRow < 0 {
		t.Fatalf("there is no spinner row in %v", roles)
	}

	before := m.themeDraft.Spinner
	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if m.themeDraft.Spinner == before {
		t.Errorf("spinner is still %q", before)
	}
	if !ui.KnownSpinner(m.themeDraft.Spinner) {
		t.Errorf("spinner = %q, which is not a shape", m.themeDraft.Spinner)
	}
}

func TestTheEditorNamesEveryRoleAndPreviewsTheDraft(t *testing.T) {
	m := editorOn(t, "Ben's Theme")

	out := plain(m.viewSettings())
	for _, role := range ui.RoleNames() {
		if !strings.Contains(out, ui.RoleLabel(role)) {
			t.Errorf("the editor does not list %q:\n%s", ui.RoleLabel(role), out)
		}
	}
	for _, help := range []string{"something worked", "a session about to go", "the row a list is on"} {
		if strings.Contains(out, help) {
			t.Errorf("the editor still carries the help column (%q):\n%s", help, out)
		}
	}
	if i, j := strings.Index(out, "Other"), strings.Index(out, "Background"); i < 0 || j < 0 || i > j {
		t.Errorf("other is not listed above background (%d, %d):\n%s", i, j, out)
	}
	if !strings.Contains(out, "Preview · Ben's Theme") {
		t.Errorf("the editor has no preview of the draft:\n%s", out)
	}
	if got := m.sectionRows(); got != 0 {
		t.Errorf("sectionRows() = %d while editing, want 0", got)
	}
	if !strings.Contains(out, "enter pick") {
		t.Errorf("the hint does not say how to change a color:\n%s", out)
	}

	next, _ := m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.themeEdit {
		t.Error("esc did not close the editor")
	}
	if got := plain(m.viewSettings()); !strings.Contains(got, "enter select") {
		t.Errorf("the list did not come back:\n%s", got)
	}
}

func TestRenderTheThemeEditor(t *testing.T) {
	m := editorOn(t, "Ben's Theme")
	t.Log("\n" + m.viewSettings())
}

func TestTheEditorAndPickerGetTheirKeysBeforeTheSettingsPage(t *testing.T) {
	m := editorOn(t, "nord")
	onNord := m.setCursor

	next, _ := m.onSettingsKey("down")
	m = next.(tuiModel)
	if m.themeRow != 1 {
		t.Errorf("themeRow = %d after down, want the role cursor to have moved", m.themeRow)
	}
	if m.setCursor != onNord {
		t.Errorf("setCursor = %d, want the theme list left on %d", m.setCursor, onNord)
	}

	next, _ = m.onSettingsKey("q")
	m = next.(tuiModel)
	if m.themeEdit {
		t.Fatal("q did not close the editor")
	}
	if m.phase != phaseSettings {
		t.Errorf("phase = %v after leaving the editor, want to still be in settings", m.phase)
	}
	if m.setFocus != focusContent {
		t.Errorf("setFocus = %v, want the theme list still focused", m.setFocus)
	}

	next, _ = m.onSettingsKey("down")
	m = next.(tuiModel)
	if m.setCursor != onNord+1 {
		t.Errorf("setCursor = %d after down, want the theme list to scroll to %d",
			m.setCursor, onNord+1)
	}
}

func TestLeavingThePickerLandsInTheEditor(t *testing.T) {
	m := pickerOn(t, "nord", "cmd")

	next, _ := m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.pick.on {
		t.Fatal("esc did not close the picker")
	}
	if !m.themeEdit {
		t.Error("esc left the editor as well as the picker")
	}
	if m.phase != phaseSettings {
		t.Errorf("phase = %v, want to still be in settings", m.phase)
	}

	next, _ = m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.themeEdit {
		t.Fatal("the editor did not close")
	}
	next, _ = m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.setFocus != focusNav {
		t.Errorf("setFocus = %v, want the section nav", m.setFocus)
	}
	next, _ = m.onSettingsKey("esc")
	if got := next.(tuiModel).phase; got != phaseMenu {
		t.Errorf("phase = %v, want the main menu", got)
	}
}

func TestTheEditorsActionRowsAreListedAndSayWhatTheyDo(t *testing.T) {
	m := editorOn(t, "nord")
	out := plain(m.viewSettings())
	for _, want := range []string{randomRow, similarRow, undoRow, "nothing to undo yet", "placeholder"} {
		if !strings.Contains(out, want) {
			t.Errorf("the editor does not offer %q:\n%s", want, out)
		}
	}
}

func TestUndoWalksBackOneChangeAtATime(t *testing.T) {
	m := editorOn(t, "nord")
	opened := m.themeDraft.Clone()

	m.themeRow = slices.Index(editRows(), randomRow)
	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	rolled := m.themeDraft.Clone()
	if rolled.Equal(opened) {
		t.Fatal("randomize changed nothing")
	}

	m.themeRow = slices.Index(editRows(), "warn")
	next, _ = m.onSettingsKey("s")
	m = next.(tuiModel)
	if m.themeDraft.Equal(rolled) {
		t.Fatal("the nudge changed nothing")
	}

	if out := plain(m.viewSettings()); !strings.Contains(out, "step(s) back") {
		t.Errorf("the undo row does not say what it will do:\n%s", out)
	}

	next, _ = m.onSettingsKey("u")
	m = next.(tuiModel)
	if !m.themeDraft.Equal(rolled) {
		t.Errorf("warn = %q after one undo, want the rolled %q",
			m.themeDraft.Warn, rolled.Warn)
	}
	if got := ui.CurrentTheme(); !got.Equal(rolled) {
		t.Error("undo did not reach the frame, only the draft")
	}

	next, _ = m.onSettingsKey("u")
	m = next.(tuiModel)
	if !m.themeDraft.Equal(opened) {
		t.Errorf("ok = %q after the second undo, want the opened %q",
			m.themeDraft.OK, opened.OK)
	}
	reloaded, _ := ui.ThemeByName("nord")
	if !reloaded.Equal(opened) {
		t.Errorf("the file holds %q for ok, want %q", reloaded.OK, opened.OK)
	}

	next, _ = m.onSettingsKey("u")
	if got := next.(tuiModel).status; !strings.Contains(got, "nothing to undo") {
		t.Errorf("status = %q with the stack empty", got)
	}
}

func TestPickingAColorIsOneUndoStep(t *testing.T) {
	m := pickerOn(t, "nord", "cmd")
	before := m.themeDraft.Cmd

	for range 4 {
		next, _ := m.onSettingsKey("left")
		m = next.(tuiModel)
	}
	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if m.themeDraft.Cmd == before {
		t.Fatal("the picker kept nothing")
	}
	if got := len(m.themeUndo); got != 1 {
		t.Errorf("the picker left %d undo steps, want 1", got)
	}

	next, _ = m.onSettingsKey("u")
	m = next.(tuiModel)
	if m.themeDraft.Cmd != before {
		t.Errorf("cmd = %q after undo, want %q", m.themeDraft.Cmd, before)
	}
}

func TestRandomizeRollsEveryColorAndKeepsItReadable(t *testing.T) {
	m := editorOn(t, "classic")
	m.themeRow = slices.Index(editRoles(), randomRow)

	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)

	bg, ok := ui.RGBOf(m.themeDraft.Bg)
	if !ok {
		t.Fatalf("randomize left bg = %q", m.themeDraft.Bg)
	}
	_, _, bgv := ui.HSVOf(bg)

	for _, role := range ui.RoleNames() {
		spec := m.themeDraft.Role(role)
		if spec == "" {
			t.Errorf("%s was left unset by randomize", role)
			continue
		}
		c, ok := ui.RGBOf(spec)
		if !ok {
			t.Errorf("%s = %q does not resolve", role, spec)
			continue
		}
		if role == "background" {
			continue
		}
		_, _, v := ui.HSVOf(c)
		if d := v - bgv; d < 0.18 && d > -0.18 {
			t.Errorf("%s (%s) is as bright as the background it rolled (%s)",
				role, spec, m.themeDraft.Bg)
		}
	}
	if !ui.KnownSpinner(m.themeDraft.Spinner) {
		t.Errorf("spinner = %q, which is not a shape", m.themeDraft.Spinner)
	}
}

func TestSimilarShiftsEveryColorWithoutLosingTheScheme(t *testing.T) {
	m := editorOn(t, "Ben's Theme")
	start := m.themeDraft
	m.themeRow = slices.Index(editRoles(), similarRow)

	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)

	for _, role := range []string{"ok", "err", "cmd", "element"} {
		before, _ := ui.RGBOf(start.Role(role))
		after, ok := ui.RGBOf(m.themeDraft.Role(role))
		if !ok {
			t.Fatalf("%s = %q", role, m.themeDraft.Role(role))
		}
		bh, _, _ := ui.HSVOf(before)
		ah, _, _ := ui.HSVOf(after)
		d := bh - ah
		if d < 0 {
			d = -d
		}
		if d > 60 && d < 300 {
			t.Errorf("%s moved %.0f degrees of hue, which is a different color", role, d)
		}
	}

	bg, _ := ui.RGBOf(m.themeDraft.Bg)
	if _, _, v := ui.HSVOf(bg); v > 0.4 {
		t.Errorf("the background came out at value %.2f, want it still dark", v)
	}
}

func TestSimilarAndRandomWorkOnTheRoleUnderTheCursor(t *testing.T) {
	m := editorOn(t, "nord")
	roles := editRoles()
	m.themeRow = slices.Index(roles, "warn")

	start := m.themeDraft
	next, _ := m.onSettingsKey("S")
	m = next.(tuiModel)
	if m.themeDraft.Warn == start.Warn {
		t.Error("S left the role under the cursor alone")
	}
	if m.themeDraft.OK != start.OK || m.themeDraft.Err != start.Err {
		t.Error("S on one role changed the others too")
	}

	start = m.themeDraft
	next, _ = m.onSettingsKey("R")
	m = next.(tuiModel)
	if m.themeDraft.Warn == start.Warn {
		t.Error("R left the role under the cursor alone")
	}
	if m.themeDraft.OK != start.OK {
		t.Error("R on one role changed the others too")
	}

	start = m.themeDraft
	next, _ = m.onSettingsKey("s")
	m = next.(tuiModel)
	if m.themeDraft.OK == start.OK {
		t.Error("s on a role row did not change the whole scheme")
	}
}

func TestANewSchemeIsCopiedFromTheOneInUseAndOpensInTheEditor(t *testing.T) {
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = "" })

	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	m.setCursor = 0

	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if m.formKind != formNewTheme {
		t.Fatalf("formKind = %v, want the new-scheme form", m.formKind)
	}

	m.form[0].input.SetValue("nord")
	next, _ = m.submitNewTheme()
	m = next.(tuiModel)
	if m.formKind != formNewTheme {
		t.Error("the form closed on a name that is already taken")
	}
	if !strings.Contains(m.status, "already exists") {
		t.Errorf("status = %q, want it to say the name is taken", m.status)
	}

	m.form[0].input.SetValue("  ")
	next, _ = m.submitNewTheme()
	if got := next.(tuiModel).status; !strings.Contains(got, "needs a name") {
		t.Errorf("status = %q on an empty name", got)
	}

	m.form[0].input.SetValue("workbench")
	next, _ = m.submitNewTheme()
	m = next.(tuiModel)
	if m.formKind != formNone {
		t.Fatalf("the form stayed open: %s", m.status)
	}
	if !m.themeEdit {
		t.Error("a new scheme did not open in the editor")
	}
	if m.themeDraft.Name != "workbench" {
		t.Errorf("the editor is on %q", m.themeDraft.Name)
	}
	if m.themeDraft.OK == "" || m.themeDraft.Cmd == "" {
		t.Errorf("the new scheme is empty: %+v", m.themeDraft)
	}
	if _, ok := ui.ThemeByName("workbench"); !ok {
		t.Error("the new scheme was not written where the picker reads from")
	}
	if ui.CurrentTheme().Name != "workbench" {
		t.Errorf("CurrentTheme() = %q, want the new scheme in use", ui.CurrentTheme().Name)
	}
	m.themeRow = slices.Index(editRoles(), randomRow)
	next, _ = m.onSettingsKey("enter")
	m = next.(tuiModel)
	next, _ = m.onSettingsKey("u")
	m = next.(tuiModel)
	if !m.themeDraft.Equal(m.themeStart) {
		t.Error("undo in a new scheme did not go back to the copy it started as")
	}
}
