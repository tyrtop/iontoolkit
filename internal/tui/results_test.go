package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
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

		if n := len(strings.Split(ui.ClipFrame(m.viewResults(), h), "\n")); n > h {
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

func TestResultsFormatKeysCycleAndPersist(t *testing.T) {
	m := resultsModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'f', Text: "f"}))
	m = mm.(tuiModel)
	if m.cfg.BatchFormat != output.FormatJSONL {
		t.Errorf("BatchFormat = %q, want %q after one cycle from the default", m.cfg.BatchFormat, output.FormatJSONL)
	}
	if !strings.Contains(m.status, "s saves") {
		t.Errorf("status = %q, want it to say what still has to be pressed", m.status)
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Text: "o"}))
	m = mm.(tuiModel)
	if m.cfg.BatchOrient != output.OrientCommand {
		t.Errorf("BatchOrient = %q, want %q", m.cfg.BatchOrient, output.OrientCommand)
	}

	saved, err := m.store.Config()
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if saved.BatchFormat != output.FormatJSONL || saved.BatchOrient != output.OrientCommand {
		t.Errorf("saved format %q orientation %q, want both persisted", saved.BatchFormat, saved.BatchOrient)
	}
}

func TestResultsFormatKeysStayOutOfTheFilter(t *testing.T) {
	m := resultsModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	for _, k := range []rune{'f', 'o', 's'} {
		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)}))
		m = mm.(tuiModel)
	}

	if m.cfg.BatchFormat != "" || m.cfg.BatchOrient != "" {
		t.Errorf("typing in the filter changed the save format to %q/%q", m.cfg.BatchFormat, m.cfg.BatchOrient)
	}
	if got := m.resFilter.Value(); got != "fos" {
		t.Errorf("filter = %q, want the letters typed into it", got)
	}
}

func TestResultsSaveKeyWritesTheCurrentFormat(t *testing.T) {
	dir := t.TempDir()
	m := resultsModel(t)
	m.cfg.BatchToFile, m.cfg.BatchDir, m.cfg.BatchFormat = true, dir, output.FormatCSV

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	m = mm.(tuiModel)
	if !strings.HasPrefix(m.status, "saved to ") {
		t.Fatalf("status = %q, want it to report where the file went", m.status)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".csv") {
		t.Errorf("wrote %q, want the extension to follow the format", entries[0].Name())
	}
	body, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{"element_id", "S0100-RTR-01", "hung session"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("saved csv is missing %q:\n%s", want, body)
		}
	}
}

func TestResultsSaveKeySaysWhenThereIsNoFolder(t *testing.T) {
	m := resultsModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	m = mm.(tuiModel)
	if !strings.Contains(m.status, "settings") {
		t.Errorf("status = %q, want it to say where to turn file output on", m.status)
	}
}

func TestResultsHeaderNamesTheSaveFormat(t *testing.T) {
	m := resultsModel(t)

	m.cfg.BatchFormat, m.cfg.BatchOrient = output.FormatCSV, output.OrientCommand
	if got := plain(m.viewResults()); !strings.Contains(got, "csv · command-first") {
		t.Errorf("results header does not name the format and orientation:\n%s", got)
	}

	// json has no rows to sort, so naming an orientation would claim something
	// the save does not do.
	m.cfg.BatchFormat = output.FormatJSON
	got := plain(m.viewResults())
	if !strings.Contains(got, "json") {
		t.Errorf("results header does not name the format:\n%s", got)
	}
	if strings.Contains(got, "command-first") {
		t.Errorf("results header claims an orientation json does not use:\n%s", got)
	}
}
