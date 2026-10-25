package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/fixture"
)

func settingsAt(t *testing.T, section int) tuiModel {
	t.Helper()
	m := settingsModel(t)
	m.phase, m.setSection, m.setFocus = phaseSettings, section, focusContent
	m.width, m.height = 110, 44
	return m
}

func TestTheTestingSectionSaysWhatTheFakeFleetHolds(t *testing.T) {
	m := settingsAt(t, secDebug)

	out := plain(strings.Join(m.sectionContent(), "\n"))
	for _, want := range []string{"Testing Mode", "off", "Download Logs"} {
		if !strings.Contains(out, want) {
			t.Errorf("Debug does not offer %q:\n%s", want, out)
		}
	}
	if got := m.sectionHint(); !strings.Contains(got, "enter testing mode") {
		t.Errorf("hint = %q, want it to offer the way in", got)
	}
	m.setCursor = logsRow
	if got := m.sectionHint(); !strings.Contains(got, "download") {
		t.Errorf("hint on the log row = %q", got)
	}
}

func TestEnterTakesYouIntoTheFakeFleetAndBackOut(t *testing.T) {
	m := settingsAt(t, secDebug)
	was := m.tenant().Name

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)

	if !m.real.held {
		t.Fatalf("enter did not swap the world in: status %q", m.status)
	}
	if m.tenant().Name != "Acme Co" {
		t.Errorf("tenant = %q, want the fake one", m.tenant().Name)
	}
	if !strings.Contains(m.status, "network") {
		t.Errorf("status = %q, want it to say nothing reaches the network", m.status)
	}
	if got := plain(m.testingLine()); !strings.Contains(got, "on  Acme Co") {
		t.Errorf("the row reads %q, want it to say which fleet is in", got)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)

	if m.real.held {
		t.Fatal("enter did not put the real world back")
	}
	if m.tenant().Name != was {
		t.Errorf("tenant = %q, want %q back", m.tenant().Name, was)
	}
}

func TestAnOpenSessionKeepsTheFakeFleetOut(t *testing.T) {
	m, _ := favModel(t)
	m.phase, m.setSection, m.setFocus = phaseSettings, secDebug, focusContent

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := next.(tuiModel)

	if got.real.held {
		t.Fatal("it swapped the world out from under an open session")
	}
	if !strings.Contains(got.status, "close the open sessions") {
		t.Errorf("status = %q, want the reason", got.status)
	}
}

func TestAltEEditsTheFakeFleetAndReloadsIt(t *testing.T) {
	m := settingsAt(t, secDebug)
	mm := openEditor(t, m)

	if got := mm.edit.target.what; got != fixture.FileName {
		t.Errorf("the editor opened on %q, want %q", got, fixture.FileName)
	}
	if got := mm.edit.area.Value(); !strings.Contains(got, "tenants:") || !strings.Contains(got, "Acme Co") {
		t.Errorf("the starter is not the fleet iontk ships:\n%s", got)
	}

	mm.edit.area.SetValue("tenants:\n  - name: Test Co\n    elements:\n      - name: rtr-01\n")
	next, _ := mm.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	mm = next.(tuiModel)

	if mm.edit.err != "" {
		t.Fatalf("ctrl+s refused a good fleet: %q", mm.edit.err)
	}
	b, err := os.ReadFile(mm.edit.target.path)
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if !strings.Contains(string(b), "Test Co") {
		t.Errorf("the file holds %q", string(b))
	}

	_, set, err := app.FakeFleet(mm.store())
	if err != nil {
		t.Fatalf("FakeFleet() error = %v", err)
	}
	if len(set.Tenants) != 1 || set.Tenants[0].Name != "Test Co" {
		t.Errorf("the fleet is %+v, want the one just written", set.Tenants)
	}
}

func TestAFakeFleetThatWillNotParseIsNotWritten(t *testing.T) {
	m := settingsAt(t, secDebug)
	mm := openEditor(t, m)
	mm.edit.area.SetValue("tenants:\n  - name: Test Co\n    nope: 1\n")

	next, _ := mm.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	mm = next.(tuiModel)

	if mm.edit.err == "" {
		t.Fatal("ctrl+s took a fleet that will not parse")
	}
	if _, err := os.Stat(mm.edit.target.path); err == nil {
		t.Error("the refused file was written anyway")
	}
}

func TestTheFileBesideTheConfigWinsOverTheOneIontkShips(t *testing.T) {
	m := settingsAt(t, secDebug)
	path := filepath.Join(m.store().Root, fixture.FileName)
	if err := os.WriteFile(path, []byte("tenants:\n  - name: Lab Co\n    elements:\n      - name: rtr-99\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, set, err := app.FakeFleet(m.store())
	if err != nil {
		t.Fatalf("FakeFleet() error = %v", err)
	}
	if set.Tenants[0].Name != "Lab Co" {
		t.Errorf("fleet = %q, want the file beside the config", set.Tenants[0].Name)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.(tuiModel).tenant().Name; got != "Lab Co" {
		t.Errorf("entered as %q, want the fleet from the file", got)
	}
}

func TestTheTabRingIsNotMixedBetweenWorlds(t *testing.T) {
	m := settingsAt(t, secDebug)
	m.runtime().SetDataTabs([]dataTab{{EID: "real-1", Name: "store-9999-rtr-01", TSG: m.tenant().TSGID}}, 0)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)
	if !m.real.held {
		t.Fatalf("enter did not swap the world in: %q", m.status)
	}
	if len(m.dataTabs()) != 0 {
		t.Errorf("%d data tabs came into the fake world: %+v", len(m.dataTabs()), m.dataTabs())
	}
	if got := tabCountOf(m); got != 1 {
		t.Errorf("tabCount() = %d, want the element list alone", got)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)
	if len(m.dataTabs()) != 1 || m.dataTabs()[0].EID != "real-1" {
		t.Errorf("data tabs after leaving = %+v, want the real one back", m.dataTabs())
	}
}

func TestDemoOpensOnTheMenuWithoutAsking(t *testing.T) {
	set, err := fixture.Tutorial()
	if err != nil {
		t.Fatal(err)
	}
	app.Fixture = set
	t.Cleanup(func() { app.Fixture = nil })

	st, err := app.Store()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.TutorialSeen = false

	m := newTUIModel(context.Background(), st, cfg).raiseTutorialAsk()
	if !m.askTutorial {
		t.Fatal("a config that has not seen the tutorial should raise the ask")
	}

	m = m.openDemo()
	if m.askTutorial {
		t.Error("the demo opened with the tutorial ask over it")
	}
	if m.phase != phaseMenu {
		t.Errorf("phase = %v (status %q), want the main menu", m.phase, m.status)
	}
	if m.cacheState() != cacheUpdating {
		t.Error("the demo does not load the fleet behind the menu")
	}
	if m.tenant().Name != "Acme Co" {
		t.Errorf("tenant = %q, want the first of the fake ones", m.tenant().Name)
	}
}

func TestARunLaunchedFakeSaysSoRatherThanSwappingAgain(t *testing.T) {
	app.LaunchedFake = "the fleet iontk ships"
	t.Cleanup(func() { app.LaunchedFake = "" })

	m := settingsAt(t, secDebug)
	if got := plain(m.testingLine()); !strings.Contains(got, "this whole run") {
		t.Errorf("the row reads %q, want it to say the run was launched fake", got)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := next.(tuiModel)
	if got.real.held {
		t.Error("it swapped a second fake world in over the one it was launched on")
	}
	if !strings.Contains(got.status, "launched on a fake fleet") {
		t.Errorf("status = %q, want the reason", got.status)
	}
}
