package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ionterm"
)

func favModel(t *testing.T) (tuiModel, *recConn) {
	t.Helper()
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions().CloseAll)

	conn := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}
	return m, conn
}

func pressSessionKey(t *testing.T, m tuiModel, key tea.Key) (tuiModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg(key))
	return next.(tuiModel), cmd
}

func altKey(r rune) tea.Key { return tea.Key{Code: r, Mod: tea.ModAlt} }

func TestAltDigitInsertsAFavourite(t *testing.T) {
	m, conn := favModel(t)
	if err := m.runtime().SetFavourite(1, "dump device status", "ion 1200"); err != nil {
		t.Fatal(err)
	}

	m, _ = pressSessionKey(t, m, altKey('1'))
	if m.status != "" {
		t.Errorf("status = %q, want nothing to say for a favourite from this model", m.status)
	}
	waitForTUI(t, func() bool { return strings.Contains(conn.sent(), "dump device status") })
	if strings.Contains(conn.sent(), "\r") {
		t.Errorf("the favourite was sent, not inserted: %q", conn.sent())
	}
}

func TestAnEmptySlotSaysHowToFillIt(t *testing.T) {
	m, _ := favModel(t)
	m, cmd := pressSessionKey(t, m, altKey('3'))
	if cmd != nil {
		t.Error("an empty slot queued work")
	}
	if !strings.Contains(m.status, "alt+f") {
		t.Errorf("status = %q, want it to name the key that fills a slot", m.status)
	}
}

func TestAFavouriteFromAnotherModelIsFlagged(t *testing.T) {
	m, conn := favModel(t)
	if err := m.runtime().SetFavourite(2, "dump interface status 8", "ion 3200"); err != nil {
		t.Fatal(err)
	}

	m, _ = pressSessionKey(t, m, altKey('2'))
	for _, want := range []string{"ion 3200", "ion 1200"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status = %q, want it to name %q", m.status, want)
		}
	}
	waitForTUI(t, func() bool { return strings.Contains(conn.sent(), "dump interface status 8") })
}

func TestAltFSavesThePromptLine(t *testing.T) {
	m, _ := favModel(t)
	m = typeString(t, m, "dump vpn count")

	m, _ = pressSessionKey(t, m, altKey('f'))
	if !m.favPick {
		t.Fatal("alt+f did not open the picker")
	}
	if got := plain(sessionText(m)); !strings.Contains(got, "favourites") {
		t.Errorf("the picker is not on screen:\n%s", got)
	}

	m, cmd := pressSessionKey(t, m, tea.Key{Code: '4', Text: "4"})
	if m.favPick {
		t.Error("the picker stayed open after saving")
	}
	f, ok := m.runtime().Favourite(4)
	if !ok || f.Command != "dump vpn count" {
		t.Fatalf("slot 4 = %+v, %v, want the line that was typed", f, ok)
	}
	if f.Model != "ion 1200" {
		t.Errorf("model = %q, want the element's", f.Model)
	}
	if cmd == nil {
		t.Fatal("saving queued no write")
	}
	runSave(t, cmd)
	saved, err := m.store().Commands()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Favourite(4); !ok {
		t.Error("the favourite did not reach the disk")
	}
}

func TestThePickerListsAndClearsSlots(t *testing.T) {
	m, _ := favModel(t)
	if err := m.runtime().SetFavourite(1, "dump device status", "ion 1200"); err != nil {
		t.Fatal(err)
	}
	if err := m.runtime().SetFavourite(9, "dump interface status 8", "ion 3200"); err != nil {
		t.Fatal(err)
	}

	m, _ = pressSessionKey(t, m, altKey('f'))
	got := plain(sessionText(m))
	for _, want := range []string{"favourites", "dump device status", "ion 3200", "empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("the picker is missing %q:\n%s", want, got)
		}
	}

	cleared, cmd := m.onFavouriteKey("ctrl+d")
	after := cleared.(tuiModel)
	if _, ok := after.runtime().Favourite(1); ok {
		t.Error("ctrl+d left the slot filled")
	}
	if cmd == nil {
		t.Error("clearing queued no write")
	}

	next, _ := after.onFavouriteKey("2")
	empty := next.(tuiModel)
	if !strings.Contains(empty.status, "nothing on the line") {
		t.Errorf("status = %q, want it to say there is nothing to save", empty.status)
	}
	if !empty.favPick {
		t.Error("the picker closed without saving anything")
	}

	closed, _ := empty.onFavouriteKey("esc")
	if closed.(tuiModel).favPick {
		t.Error("esc did not close the picker")
	}
}

func TestTheFavouriteFormSaves(t *testing.T) {
	m, _ := favModel(t)
	m, _ = pressSessionKey(t, m, altKey('f'))

	next, _ := m.onFavouriteKey("a")
	m = next.(tuiModel)
	if m.formKind != formFavourite {
		t.Fatalf("formKind = %v, want the favourite form", m.formKind)
	}
	if m.favPick {
		t.Error("the picker stayed open behind the form")
	}

	m.form[0].input.SetValue("dump spoke-ha status")
	m.form[1].input.SetValue("7")
	next, cmd := m.submitForm()
	m = next.(tuiModel)

	f, ok := m.runtime().Favourite(7)
	if !ok || f.Command != "dump spoke-ha status" {
		t.Fatalf("slot 7 = %+v, %v, want what the form said", f, ok)
	}
	if m.phase != phaseSession {
		t.Errorf("phase = %v, want back to the session the form was opened from", m.phase)
	}
	runSave(t, cmd)

	m.form = []formField{newField("command", "", false), newField("slot", "", false)}
	m.formKind = formFavourite
	m.form[0].input.SetValue("uptime")
	m.form[1].input.SetValue("12")
	next, _ = m.submitForm()
	if !strings.Contains(next.(tuiModel).status, "outside 1-9") {
		t.Errorf("status = %q, want the slot refused", next.(tuiModel).status)
	}
}

func TestNumericTabSelectionIsRetired(t *testing.T) {
	m, _ := favModel(t)
	conn2 := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn2}, "2", "S0100-RTR-02", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}
	m.sessions().Select(1)

	before := m.currentTab()
	m, _ = pressSessionKey(t, m, altKey('1'))
	if m.currentTab() != before {
		t.Errorf("alt+1 moved the tab from %d to %d, want it to reach a favourite instead", before, m.currentTab())
	}
	m, _ = pressSessionKey(t, m, tea.Key{Code: tea.KeyLeft, Mod: tea.ModAlt})
	if m.currentTab() == before {
		t.Error("alt+left did not move the tab")
	}
}

func TestFavouriteSlotsSurviveARestart(t *testing.T) {
	m, _ := favModel(t)
	if err := m.runtime().SetFavourite(5, "dump device status", "ion 1200"); err != nil {
		t.Fatal(err)
	}
	if err := m.runtime().SaveCommands(); err != nil {
		t.Fatal(err)
	}

	cfg, err := m.store().Config()
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTUIModel(context.Background(), m.store(), cfg)
	f, ok := restarted.runtime().Favourite(5)
	if !ok || f.Command != "dump device status" {
		t.Errorf("slot 5 after a restart = %+v, %v", f, ok)
	}
}

func TestTheFooterOffersTheFavouritesPickerNotTheChords(t *testing.T) {
	m, _ := favModel(t)
	m.width, m.termHeight = 160, 30
	m.height = m.usableHeight()

	got := plain(m.View().Content)
	if !strings.Contains(got, "alt+f favourites") {
		t.Errorf("the shell footer does not offer the picker:\n%s", got)
	}
	if strings.Contains(got, "alt+1-9 favourite") {
		t.Errorf("the shell footer still lists the chords:\n%s", got)
	}
}

func TestThePickerSaysHowToAddAndWhatWillBeAdded(t *testing.T) {
	m, conn := favModel(t)
	m.width, m.termHeight = 160, 30
	m.height = m.usableHeight()

	conn.feed("store-0003-rtr-01# dump interface status all")
	s := m.sessions().Active()
	waitForTUI(t, func() bool { return s.Line() == "dump interface status all" })

	next, _ := m.openFavourites()
	got := plain(next.(tuiModel).View().Content)

	if !strings.Contains(got, "press 1-9 to add favourite") {
		t.Errorf("the picker does not say how to add one:\n%s", got)
	}
	if !strings.Contains(got, "dump interface status all") {
		t.Errorf("the picker does not show what it would add:\n%s", got)
	}
	if !strings.Contains(got, "alt+1-9 puts one on the line") {
		t.Errorf("the picker does not say what the chords do:\n%s", got)
	}
}

func TestSavingAFavouriteTakesTheLineTheDeviceShows(t *testing.T) {
	m, conn := favModel(t)

	conn.feed("store-0003-rtr-01# dump interface status all")
	s := m.sessions().Active()
	waitForTUI(t, func() bool { return s.Line() == "dump interface status all" })

	next, _ := m.openFavourites()
	mm, _ := next.(tuiModel).Update(tea.KeyPressMsg(tea.Key{Code: '3', Text: "3"}))
	m = mm.(tuiModel)

	f, ok := m.runtime().Favourite(3)
	if !ok {
		t.Fatalf("slot 3 was not saved: %q", m.status)
	}
	if f.Command != "dump interface status all" {
		t.Errorf("slot 3 holds %q, want the command the device was showing", f.Command)
	}
}

func TestThePickerSaysWhenThereIsNothingToAdd(t *testing.T) {
	m, _ := favModel(t)
	m.width, m.termHeight = 160, 30
	m.height = m.usableHeight()

	next, _ := m.openFavourites()
	if got := plain(next.(tuiModel).View().Content); !strings.Contains(got, "nothing on the line") {
		t.Errorf("the picker claims it can add an empty line:\n%s", got)
	}
}
