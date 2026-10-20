package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSuggestKeepsTheHighlightedItemAcrossARefilter(t *testing.T) {
	pool := []string{"dump device status", "dump spoke-ha status", "dump interface status"}

	s := newSuggest(pool, "")
	if _, ok := s.current(); ok {
		t.Fatal("a fresh suggestion list highlights something, want nothing until asked")
	}

	s = s.move(1)
	first, _ := s.current()
	if first != pool[0] {
		t.Fatalf("current = %q, want the first item", first)
	}

	s = s.refilter(pool, "spokeha")
	if got, ok := s.current(); ok && got != "dump spoke-ha status" {
		t.Errorf("current = %q, want the highlight to follow the one surviving item", got)
	}

	s = s.refilter(pool, "zzz")
	if _, ok := s.current(); ok {
		t.Error("an empty result still highlights something")
	}
	if s.cursor != -1 {
		t.Errorf("cursor = %d, want -1 with nothing to point at", s.cursor)
	}
}

func TestAPickerAlwaysHasASelection(t *testing.T) {
	pool := []string{"UTC", "local", "America/Chicago"}

	s := newPicker(pool, "")
	if got, ok := s.current(); !ok || got != "UTC" {
		t.Fatalf("current = %q ok=%v, want the first row picked", got, ok)
	}
	s = s.move(-5)
	if got, _ := s.current(); got != "UTC" {
		t.Errorf("current = %q, want up at the top to stay on the first row", got)
	}
	s = s.move(99)
	if got, _ := s.current(); got != "America/Chicago" {
		t.Errorf("current = %q, want down past the end to stay on the last row", got)
	}
}

func TestTheSuggestWindowKeepsTheCursorOnScreen(t *testing.T) {
	pool := make([]string, 40)
	for i := range pool {
		pool[i] = strings.Repeat("x", i+1)
	}
	s := newPicker(pool, "")
	for i := 0; i < 39; i++ {
		s = s.move(1)
		start, end := s.window(8)
		if s.cursor < start || s.cursor >= end {
			t.Fatalf("cursor %d outside window %d..%d", s.cursor, start, end)
		}
		if end-start != 8 {
			t.Fatalf("window is %d rows, want 8 so the frame height holds", end-start)
		}
	}
}

func TestTheSuggestViewIsAFixedNumberOfRows(t *testing.T) {
	s := newSuggest([]string{"one", "two"}, "")
	got := strings.Count(s.view(60, 8), "\n")
	if got != 8 {
		t.Errorf("view wrote %d lines, want 8 whatever it has to show", got)
	}
}

func TestTheBatchBoxFuzzySearchesTheCommandCatalog(t *testing.T) {
	m := historyModel(t)
	m.width, m.height = 120, 40
	m.phase = phaseCommand
	m.cmdInput.Focus()
	m.cmdPick = newSuggest(m.catalog(), "")

	if len(m.cmdPick.items) < 100 {
		t.Fatalf("the box opens with %d commands, want the catalog on offer", len(m.cmdPick.items))
	}

	m = typeString(t, m, "spokeha")
	if n := len(m.cmdPick.items); n == 0 || n > 10 {
		t.Fatalf("the query matched %d commands, want it narrowed to a few", n)
	}
	if _, ok := m.cmdPick.current(); ok {
		t.Error("something is highlighted before anything asked, so tab would stop cycling the ION user")
	}
	if out := plain(m.viewCommand()); !strings.Contains(out, "dump spoke-ha") {
		t.Errorf("the matches are not on the page:\n%s", out)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	m = mm.(tuiModel)
	pick, ok := m.cmdPick.current()
	if !ok {
		t.Fatal("ctrl+n highlighted nothing")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	if got := m.cmdInput.Value(); got != pick {
		t.Errorf("cmdInput = %q, want tab to complete to %q", got, pick)
	}
}

func TestTabCompletesOnlyTheCommandBeingTyped(t *testing.T) {
	m := historyModel(t)
	m.width, m.height = 120, 40
	m.phase = phaseCommand
	m.cmdInput.Focus()
	m.cmdInput.SetValue("dump device status, ")
	m.cmdInput.SetCursor(len(m.cmdInput.Value()))
	m = m.refilterCommands(lastSegment(m.cmdInput.Value()))

	m = typeString(t, m, "spokeha")
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	m = mm.(tuiModel)
	pick, ok := m.cmdPick.current()
	if !ok {
		t.Fatal("ctrl+n highlighted nothing")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	want := "dump device status, " + pick
	if got := m.cmdInput.Value(); got != want {
		t.Errorf("cmdInput = %q, want %q — only the segment being typed is replaced", got, want)
	}
}

func TestTheMacroFormFuzzySearchesCommands(t *testing.T) {
	m := macroModel(t)
	m.height = 40
	m.macroCursor = 0
	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	m = next.(tuiModel)
	if m.formKind != formMacro {
		t.Fatalf("formKind = %v, want the macro form", m.formKind)
	}

	m.form[0].input.SetValue("ha check 2")
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	if m.formCursor != 1 {
		t.Fatalf("formCursor = %d, want the first command line", m.formCursor)
	}

	m = typeString(t, m, "spokeha")
	if n := len(m.cmdPick.items); n == 0 || n > 10 {
		t.Fatalf("the query matched %d commands, want it narrowed", n)
	}
	if out := plain(m.viewForm()); !strings.Contains(out, "dump spoke-ha") {
		t.Errorf("the macro form does not show the matches:\n%s", out)
	}

	fields := len(m.form)
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	m = mm.(tuiModel)
	pick, ok := m.cmdPick.current()
	if !ok {
		t.Fatal("ctrl+n highlighted nothing")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	if got := m.formValue(1); got != pick {
		t.Errorf("command 1 = %q, want tab to complete it to %q", got, pick)
	}
	if len(m.form) != fields {
		t.Errorf("form grew to %d fields, want tab to complete rather than add a line", len(m.form))
	}
}
