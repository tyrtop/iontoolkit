package tui

import (
	"os"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

// editorOn is the Theme section with the editor open on one scheme.
func editorOn(t *testing.T, name string) tuiModel {
	t.Helper()
	pinTheme(t)
	t.Cleanup(func() { app.ThemesFile = "" })

	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	for i, th := range ui.Themes() {
		if th.Name == name {
			m.setCursor = i
		}
	}

	next, _ := m.onThemeKey("e")
	m = next.(tuiModel)
	if !m.themeEdit {
		t.Fatalf("e did not open the editor on %s", name)
	}
	return m
}

// Editing a built-in writes an override of it into the config root's
// themes.yaml, which is the only place anything can be written: the shipped
// set is embedded.
func TestEditingAThemeWritesAnOverrideAndAppliesIt(t *testing.T) {
	m := editorOn(t, "classic")

	// The first role, stepped one along the palette.
	before := m.themeDraft.OK
	next, _ := m.onThemeEditKey("right")
	m = next.(tuiModel)
	if m.themeDraft.OK == before {
		t.Fatalf("ok is still %q after a step along the palette", before)
	}
	if m.status != "" {
		t.Errorf("status = %q, want the edit to have gone through", m.status)
	}

	// Applied to the whole frame, not just the preview.
	if got := ui.CurrentTheme().OK; got != m.themeDraft.OK {
		t.Errorf("CurrentTheme().OK = %q, want the edit %q", got, m.themeDraft.OK)
	}
	if ui.MarkCheck.String() == "✓" {
		t.Error("the ✓ was not restyled, so Apply did not reach the marks")
	}

	// Written where the config root loads from, and it survives a reload.
	path := app.ThemesPath(m.store)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("themes.yaml was not written: %v", err)
	}
	if !strings.Contains(string(b), "classic") {
		t.Errorf("themes.yaml does not hold the override:\n%s", b)
	}
	if err := ui.LoadThemes(path); err != nil {
		t.Fatalf("the file it wrote will not load: %v", err)
	}
	reloaded, _ := ui.ThemeByName("classic")
	if reloaded.OK != m.themeDraft.OK {
		t.Errorf("reloaded ok = %q, want %q", reloaded.OK, m.themeDraft.OK)
	}
	// Overriding one scheme must not drop the others.
	if _, ok := ui.ThemeByName("nord"); !ok {
		t.Error("the override took the rest of the set with it")
	}
	// And the theme in use is the one that was edited, so a restart keeps it.
	saved, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Theme != "classic" {
		t.Errorf("saved theme = %q, want the edited one", saved.Theme)
	}
}

// Typing a colour is the other route, and it has to refuse one that will not
// parse: lipgloss renders an unparseable colour as nothing at all.
func TestTypingAColourIsValidated(t *testing.T) {
	m := editorOn(t, "nord")
	// Move to a role worth typing into, then open the form.
	next, _ := m.onThemeEditKey("down")
	m = next.(tuiModel)
	next, _ = m.onThemeEditKey("enter")
	m = next.(tuiModel)
	if m.formKind != formColour {
		t.Fatalf("formKind = %v, want the colour form", m.formKind)
	}

	m.form[0].input.SetValue("chartreuse")
	next, _ = m.submitColour()
	m = next.(tuiModel)
	if m.formKind != formColour {
		t.Error("the form closed on a colour that will not parse")
	}
	if m.status == "" {
		t.Error("nothing said why the colour was refused")
	}

	m.form[0].input.SetValue("#ff00aa")
	next, _ = m.submitColour()
	m = next.(tuiModel)
	if m.formKind != formNone {
		t.Errorf("the form stayed open on a good colour: %s", m.status)
	}
	if got := m.themeDraft.Err; got != "#ff00aa" {
		t.Errorf("err = %q, want the typed colour", got)
	}
	if got := ui.CurrentTheme().Err; got != "#ff00aa" {
		t.Errorf("CurrentTheme().Err = %q, want the typed colour applied", got)
	}
}

// Once a built-in is overridden, going back to what it shipped with is the
// one thing a user cannot do by hand without knowing the old value.
func TestAnEditedRoleCanGoBackToTheBuiltin(t *testing.T) {
	m := editorOn(t, "classic")
	original := m.themeDraft.OK

	next, _ := m.onThemeEditKey("right")
	m = next.(tuiModel)
	if m.themeDraft.OK == original {
		t.Fatal("nothing changed, so there is nothing to undo")
	}

	next, _ = m.onThemeEditKey("ctrl+d")
	m = next.(tuiModel)
	if m.themeDraft.OK != original {
		t.Errorf("ok = %q after ctrl+d, want the built-in's %q", m.themeDraft.OK, original)
	}
}

// The spinner is part of the scheme, so it is edited in the same list.
func TestTheEditorCyclesTheSpinnerShape(t *testing.T) {
	m := editorOn(t, "classic")
	roles := editRoles()
	m.themeRow = len(roles) - 1
	if roles[m.themeRow] != spinnerRole {
		t.Fatalf("last row is %q, want the spinner", roles[m.themeRow])
	}

	before := m.themeDraft.Spinner
	next, _ := m.onThemeEditKey("right")
	m = next.(tuiModel)
	if m.themeDraft.Spinner == before {
		t.Errorf("spinner is still %q", before)
	}
	if !ui.KnownSpinner(m.themeDraft.Spinner) {
		t.Errorf("spinner = %q, which is not a shape", m.themeDraft.Spinner)
	}
}

// The editor has to say what each role is for and show the colour it holds,
// and the preview under it follows the draft rather than the saved scheme.
func TestTheEditorNamesEveryRoleAndPreviewsTheDraft(t *testing.T) {
	m := editorOn(t, "Ben's Theme")

	out := plain(m.viewSettings())
	for _, role := range ui.RoleNames() {
		if !strings.Contains(out, role) {
			t.Errorf("the editor does not list %q:\n%s", role, out)
		}
	}
	for _, help := range []string{"something worked", "a session about to go", "the row a list is on"} {
		if !strings.Contains(out, help) {
			t.Errorf("the editor does not say what the roles are for (%q missing)", help)
		}
	}
	if !strings.Contains(out, "preview · Ben's Theme") {
		t.Errorf("the editor has no preview of the draft:\n%s", out)
	}
	// The theme list is out of the way while the editor is open, so the
	// section's own cursor cannot move it underneath.
	if got := m.sectionRows(); got != 0 {
		t.Errorf("sectionRows() = %d while editing, want 0", got)
	}
	if !strings.Contains(out, "←→ cycle") {
		t.Errorf("the hint does not say how to change a colour:\n%s", out)
	}

	// esc leaves the editor and goes back to the list.
	next, _ := m.onThemeKey("esc")
	m = next.(tuiModel)
	if m.themeEdit {
		t.Error("esc did not close the editor")
	}
	if got := plain(m.viewSettings()); !strings.Contains(got, "(in use)") {
		t.Errorf("the list did not come back:\n%s", got)
	}
}

func TestRenderTheThemeEditor(t *testing.T) {
	m := editorOn(t, "Ben's Theme")
	t.Log("\n" + m.viewSettings())
}
