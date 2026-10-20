package tui

import (
	"slices"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/ui"
)

func tabPageOn(t *testing.T, theme string) tuiModel {
	t.Helper()
	m := editorOn(t, theme)
	m.themeRow = slices.Index(editRows(), tabsRow)
	if m.themeRow < 0 {
		t.Fatal("there is no tab colours row in the editor")
	}
	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if !m.tabEdit {
		t.Fatal("enter did not open the tab colours page")
	}
	return m
}

func TestTheTabPageListsTheRowsItsModeNeeds(t *testing.T) {
	m := tabPageOn(t, "nord")

	out := plain(m.viewSettings())
	for _, want := range []string{"Tab Colours · nord", "Mode", "rotate", "Tab 1", "+ Another Tab"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page does not show %q:\n%s", want, out)
		}
	}

	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if got := m.themeDraft.TabMode; got != ui.TabByType {
		t.Fatalf("mode = %q after one step, want %q", got, ui.TabByType)
	}
	out = plain(m.viewSettings())
	for _, want := range []string{"Shell", "Rich Data"} {
		if !strings.Contains(out, want) {
			t.Errorf("the type mode does not list %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "+ Another") {
		t.Errorf("the type mode offers to grow a list of one:\n%s", out)
	}

	next, _ = m.onSettingsKey("right")
	m = next.(tuiModel)
	out = plain(m.viewSettings())
	for _, want := range []string{"Shell 1", "Rich Data 1", "+ Another Shell", "+ Another Rich Data"} {
		if !strings.Contains(out, want) {
			t.Errorf("the both mode does not list %q:\n%s", want, out)
		}
	}

	next, _ = m.onSettingsKey("right")
	m = next.(tuiModel)
	if got := m.themeDraft.TabMode; got != ui.TabShared {
		t.Errorf("mode = %q, want %q", got, ui.TabShared)
	}
	next, _ = m.onSettingsKey("right")
	m = next.(tuiModel)
	if got := m.themeDraft.TabMode; got != ui.TabRotate {
		t.Errorf("mode = %q after a full cycle, want %q", got, ui.TabRotate)
	}
	reloaded, _ := ui.ThemeByName("nord")
	if reloaded.TabMode != ui.TabRotate {
		t.Errorf("the file holds mode %q", reloaded.TabMode)
	}
}

func TestTheRotationGrowsAndShrinks(t *testing.T) {
	m := tabPageOn(t, "nord")
	before := len(m.themeDraft.Tabs)

	m.tabRow = len(m.tabTargets()) - 1
	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if got := len(m.themeDraft.Tabs); got != before+1 {
		t.Fatalf("the rotation has %d colours, want %d", got, before+1)
	}
	if last := m.themeDraft.Tabs[len(m.themeDraft.Tabs)-1]; last == "" {
		t.Error("the new entry is unset, which renders as no colour")
	}
	if at := m.tabTargets()[m.tabRow]; at.idx != before {
		t.Errorf("the cursor is on %+v, want the row that appeared", at)
	}

	next, _ = m.onSettingsKey("ctrl+d")
	m = next.(tuiModel)
	if got := len(m.themeDraft.Tabs); got != before {
		t.Errorf("the rotation has %d colours after a drop, want %d", got, before)
	}

	for range before + 2 {
		m.tabRow = 1
		next, _ = m.onSettingsKey("ctrl+d")
		m = next.(tuiModel)
	}
	if got := len(m.themeDraft.Tabs); got != 1 {
		t.Errorf("the rotation has %d colours, want it stopped at one", got)
	}
	if !strings.Contains(m.status, "needs one colour") {
		t.Errorf("status = %q, want it to say why", m.status)
	}
}

func TestSettingAShellColourMaterialisesTheList(t *testing.T) {
	m := tabPageOn(t, "nord")
	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if len(m.themeDraft.TabShell) != 0 {
		t.Fatal("the shell list is set before anything touched it")
	}

	m.tabRow = 1
	next, _ = m.onSettingsKey("R")
	m = next.(tuiModel)
	if len(m.themeDraft.TabShell) == 0 {
		t.Fatal("rolling the shell colour did not write the list")
	}
	if m.themeDraft.TabShell[0] == "" {
		t.Error("the shell colour is unset")
	}
	reloaded, _ := ui.ThemeByName("nord")
	if !slices.Equal(reloaded.Tabs, m.themeDraft.Tabs) {
		t.Error("setting a shell colour changed the rotation")
	}
}

func TestATabColourIsPickedWithTheSamePicker(t *testing.T) {
	m := tabPageOn(t, "nord")
	m.tabRow = 1
	before := m.themeDraft.Tabs[0]

	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if !m.pick.on {
		t.Fatal("enter did not open the picker")
	}
	if m.pick.list != listTabs || m.pick.idx != 0 {
		t.Fatalf("the picker is on %+v, want the first tab colour", m.pick)
	}
	if out := plain(m.viewSettings()); !strings.Contains(out, "Picking Tab 1") {
		t.Errorf("the picker does not name what it is on:\n%s", out)
	}

	for range 3 {
		next, _ = m.onSettingsKey("left")
		m = next.(tuiModel)
	}
	if m.themeDraft.Tabs[0] == before {
		t.Error("moving on the disc did not change the tab colour")
	}
	next, _ = m.onSettingsKey("enter")
	m = next.(tuiModel)
	if m.pick.on {
		t.Error("enter did not close the picker")
	}
	if !m.tabEdit {
		t.Error("closing the picker left the tab page too")
	}

	m.tabRow = 1
	kept := m.themeDraft.Tabs[0]
	next, _ = m.onSettingsKey("enter")
	m = next.(tuiModel)
	next, _ = m.onSettingsKey("left")
	m = next.(tuiModel)
	next, _ = m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.themeDraft.Tabs[0] != kept {
		t.Errorf("tab 1 = %q after esc, want %q", m.themeDraft.Tabs[0], kept)
	}
}

func TestLeavingTheTabPageLandsInTheEditor(t *testing.T) {
	m := tabPageOn(t, "nord")

	next, _ := m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.tabEdit {
		t.Fatal("esc did not close the tab page")
	}
	if !m.themeEdit {
		t.Error("esc left the editor as well")
	}
	if m.phase != phaseSettings {
		t.Errorf("phase = %v, want to still be in settings", m.phase)
	}
}

func TestThePreviewBarShowsTwoOfEachKind(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent
	m.setCursor = 1

	out := plain(m.viewSettings())
	if n := strings.Count(out, "· data"); n < 2 {
		t.Errorf("the preview bar has %d data tabs, want two:\n%s", n, out)
	}
	if !strings.Contains(out, "store-0003-rtr-02") {
		t.Errorf("the preview bar has only one shell tab:\n%s", out)
	}
	if ui.CurrentTheme().Name != m.cfg.Theme && m.cfg.Theme != "" {
		t.Errorf("the preview left %q applied", ui.CurrentTheme().Name)
	}
}

func TestRenderTheTabColoursPage(t *testing.T) {
	m := tabPageOn(t, "Ben's Theme")
	t.Log("\n" + m.viewSettings())
	for range 2 {
		next, _ := m.onSettingsKey("right")
		m = next.(tuiModel)
	}
	t.Log("\n" + m.viewSettings())
}
