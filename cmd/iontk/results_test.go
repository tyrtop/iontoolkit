package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func resultsModel(t *testing.T) tuiModel {
	t.Helper()
	m := pickModel(t)
	m.width, m.height = 100, 30

	rs := []sase.Result{
		{ElementID: "1", Name: "S0100-RTR-01", Model: "ion 2000", Connected: true,
			Commands: []ioncli.CommandOutput{{Command: "dump device status", Output: "up 5 days"}}},
		{ElementID: "2", Name: "S0100-RTR-02", Model: "ion 2000", Connected: true,
			Commands: []ioncli.CommandOutput{{Command: "dump device status", Output: "up 2 days"}}},
		{ElementID: "3", Name: "S9999-RTR-01", Model: "ion 9000", Error: "element 3: run: hung session"},
		{},
	}
	el := []time.Duration{142 * time.Millisecond, 3 * time.Second, 20 * time.Second, 0}
	return m.seedResultsForTest(rs, el)
}

func TestResultsListsOnlyCompleted(t *testing.T) {
	m := resultsModel(t)

	if got := m.completedCount(); got != 3 {
		t.Errorf("completedCount() = %d, want 3", got)
	}
	if got := len(m.resMatches); got != 3 {
		t.Errorf("rows = %d, want 3 — the pending element must not appear", got)
	}
	for _, i := range m.resMatches {
		if m.results[i].ElementID == "" {
			t.Error("a pending result leaked into the table")
		}
	}
}

func TestResultsFilterUsesTheSameLogicAsElements(t *testing.T) {
	m := resultsModel(t)

	m.resFilter.SetValue("0100")
	m.applyResultFilter()
	if got := len(m.resMatches); got != 2 {
		t.Errorf("substring filter matched %d, want 2", got)
	}

	m.resFilter.SetValue("ion 9000")
	m.applyResultFilter()
	if got := len(m.resMatches); got != 1 {
		t.Errorf("model filter matched %d, want 1", got)
	}

	m.resFilter.SetValue("zzz")
	m.applyResultFilter()
	if got := len(m.resMatches); got != 0 {
		t.Errorf("nonsense filter matched %d", got)
	}
}

func TestResultsEnterExpandsInlineNotANewTab(t *testing.T) {
	m := resultsModel(t)
	before := m.sessions.Count()

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	open := mm.(tuiModel)

	if !open.resOpen {
		t.Fatal("enter did not expand the row")
	}
	if open.sessions.Count() != before {
		t.Error("enter opened a session tab; it should expand inline")
	}
	if open.phase != phaseResults {
		t.Errorf("phase = %v, want to stay on results", open.phase)
	}

	got := plain(open.viewResults())
	if !strings.Contains(got, "S0100-RTR-01") {
		t.Error("expansion missing the element name")
	}
	if !strings.Contains(got, "up 5 days") {
		t.Errorf("expansion missing the command output:\n%s", got)
	}
}

func TestResultsScrollingDownClosesTheExpansion(t *testing.T) {
	m := resultsModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	open := mm.(tuiModel)
	if !open.resOpen {
		t.Fatal("expected an open row")
	}

	mm, _ = open.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	moved := mm.(tuiModel)

	if moved.resOpen {
		t.Error("moving down should close the expansion")
	}
	if moved.resTable.Cursor() != 1 {
		t.Errorf("cursor = %d, want it to have moved to 1", moved.resTable.Cursor())
	}
}

func TestResultsExpansionShowsErrors(t *testing.T) {
	m := resultsModel(t)
	m.resTable.SetCursor(2)
	m.resOpen = true

	got := plain(m.viewResults())
	if !strings.Contains(got, "hung session") {
		t.Errorf("expansion should show the failure:\n%s", got)
	}
}

func TestResultsEscapeLayers(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = true

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = mm.(tuiModel)
	if m.resOpen {
		t.Error("first esc should close the expansion")
	}
	if m.phase != phaseResults {
		t.Errorf("first esc left results, phase = %v", m.phase)
	}

	m.resFilterOn = true
	m.resFilter.SetValue("x")
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = mm.(tuiModel)
	if m.resFilterOn || m.resFilter.Value() != "" {
		t.Error("second esc should clear the filter")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phasePicking {
		t.Errorf("third esc phase = %v, want phasePicking", got)
	}
}

func TestResultsReachableDuringARun(t *testing.T) {
	m := resultsModel(t)
	m.phase = phaseRunning

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	if got := mm.(tuiModel).phase; got != phaseResults {
		t.Errorf("'/' during a run gave phase %v, want phaseResults", got)
	}

	empty := pickModel(t)
	empty.phase = phaseRunning
	mm, _ = empty.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	if got := mm.(tuiModel).phase; got != phaseRunning {
		t.Errorf("with nothing completed phase = %v, want to stay running", got)
	}
}

func TestResultsFrameFitsTheTerminal(t *testing.T) {
	for _, h := range []int{8, 14, 30, 60} {
		m := resultsModel(t)
		m.height = h
		m.resOpen = true
		m.rebuildResultRows()

		if n := len(strings.Split(clipFrame(m.viewResults(), h), "\n")); n > h {
			t.Errorf("height %d: frame is %d lines", h, n)
		}
	}
}

func TestRenderResults(t *testing.T) {
	m := resultsModel(t)
	t.Log("\n--- collapsed ---\n" + m.viewResults())

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	t.Log("\n--- expanded ---\n" + mm.(tuiModel).viewResults())
}
