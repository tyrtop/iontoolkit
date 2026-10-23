package tui

import (
	"fmt"
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

func TestAnOpenResultScrollsWithAHighlight(t *testing.T) {
	m := resultsModel(t)
	m.height = 20

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	open := mm.(tuiModel)
	if !open.resOpen {
		t.Fatal("expected an open row")
	}
	if open.resLine != 0 {
		t.Errorf("resLine = %d, want the top of the output", open.resLine)
	}

	mm, _ = open.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	moved := mm.(tuiModel)
	if !moved.resOpen {
		t.Error("↓ closed the output instead of scrolling it")
	}
	if moved.resLine != 1 {
		t.Errorf("resLine = %d, want the highlight moved down one", moved.resLine)
	}
	if moved.resTable.Cursor() != 0 {
		t.Errorf("cursor = %d, want the list left where it was", moved.resTable.Cursor())
	}
	mm, _ = moved.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	moved = mm.(tuiModel)
	if moved.resLine != 2 {
		t.Errorf("resLine = %d after j, want 2", moved.resLine)
	}
	mm, _ = moved.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
	moved = mm.(tuiModel)
	if moved.resLine != 1 {
		t.Errorf("resLine = %d after k, want it back at 1", moved.resLine)
	}
	if got := moved.viewResults(); !strings.Contains(got, ui.StyleRow.Render("")[:4]) {
		t.Error("no highlight anywhere in the open output")
	}
	if got := plain(moved.viewResults()); !strings.Contains(got, "line 2 of") {
		t.Errorf("the footer does not say where the highlight is:\n%s", got)
	}

	mm, _ = moved.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	closed := mm.(tuiModel)
	if closed.resOpen {
		t.Fatal("esc did not close the output")
	}
	mm, _ = closed.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if got := mm.(tuiModel).resTable.Cursor(); got != 1 {
		t.Errorf("cursor = %d once the output is closed, want it to move", got)
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

	m.cfg.BatchFormat = output.FormatJSON
	got := plain(m.viewResults())
	if !strings.Contains(got, "json") {
		t.Errorf("results header does not name the format:\n%s", got)
	}
	if strings.Contains(got, "command-first") {
		t.Errorf("results header claims an orientation json does not use:\n%s", got)
	}
}

func TestTheResultsCursorFollowsItsElementAsResultsArrive(t *testing.T) {
	m := pickModel(t)
	m.width, m.height = 100, 30
	m.ran = []string{"1", "2", "3", "4"}
	m.results = make([]sase.Result, 4)
	m.elapsed = make([]time.Duration, 4)
	m.phase = phaseResults

	done := func(i int, name string) {
		m.results[i] = sase.Result{
			ElementID: fmt.Sprint(i + 1), Name: name, Model: "ion 2000",
			Commands: []ioncli.CommandOutput{{Command: "dump device status", Output: name + " output"}},
		}
		m.applyResultFilter()
	}

	done(3, "S0100-RTR-04")
	idx, ok := m.currentResult()
	if !ok || m.results[idx].Name != "S0100-RTR-04" {
		t.Fatalf("cursor is on %+v, want the only finished element", m.results[idx])
	}

	done(1, "S0100-RTR-02")
	idx, ok = m.currentResult()
	if !ok {
		t.Fatal("no result under the cursor")
	}
	if got := m.results[idx].Name; got != "S0100-RTR-04" {
		t.Errorf("cursor moved to %s, want it to stay on S0100-RTR-04", got)
	}

	m.resOpen = true
	out := plain(m.viewResults())
	if !strings.Contains(out, "S0100-RTR-04 output") {
		t.Errorf("the open pane is not the element under the cursor:\n%s", out)
	}

	m.moveResultCursor(-1)
	before, _ := m.currentResult()
	name := m.results[before].Name
	done(0, "S0100-RTR-01")
	after, _ := m.currentResult()
	if got := m.results[after].Name; got != name {
		t.Errorf("cursor moved from %s to %s when a result arrived", name, got)
	}
}

func TestBackspacingTheResultsQueryEmptiesItThenLeaves(t *testing.T) {
	m := resultsModel(t)
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = next.(tuiModel)
	if !m.resFilterOn {
		t.Fatal("/ did not open the results filter")
	}
	for _, r := range "S9999" {
		next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = next.(tuiModel)
	}
	if len(m.resMatches) != 1 {
		t.Fatalf("%d matches, want the query to have narrowed the list", len(m.resMatches))
	}

	for range "S9999" {
		next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
		m = next.(tuiModel)
	}
	if !m.resFilterOn {
		t.Error("the results filter closed on the press that emptied the query")
	}
	if len(m.resMatches) != m.completedCount() {
		t.Errorf("%d matches, want every completed result back", len(m.resMatches))
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if next.(tuiModel).resFilterOn {
		t.Error("the results filter is still open after backspacing the empty query")
	}
}

func TestTheResultsCursorIsTheCaretAndHighlightEveryOtherListUses(t *testing.T) {
	m := resultsModel(t)
	m.rebuildResultRows()

	rows := m.resTable.Rows()
	if len(rows) < 2 {
		t.Fatalf("%d rows, want a few to move between", len(rows))
	}
	if got := rows[0][0]; got != ">" {
		t.Errorf("the row under the cursor starts %q, want the caret", got)
	}
	if got := rows[1][0]; got != "" {
		t.Errorf("row 1 starts %q, want nothing in the caret column", got)
	}
	if got := rows[0][1]; got != plain(got) {
		t.Errorf("the mark on the cursor's row is %q; a highlight around colour ends at its reset", got)
	}
	if got := rows[1][1]; got == plain(got) {
		t.Errorf("the mark on row 1 is %q, want it coloured", got)
	}

	m.moveResultCursor(1)
	rows = m.resTable.Rows()
	if rows[1][0] != ">" || rows[0][0] != "" {
		t.Errorf("the caret did not follow the cursor: %q then %q", rows[0][0], rows[1][0])
	}
	if got := rows[1][1]; got != plain(got) {
		t.Errorf("the new cursor row keeps its colour: %q", got)
	}
}

func TestSpaceOnTheResultsPagePagesRatherThanConfusingTheTable(t *testing.T) {
	m := resultsModel(t)
	m.rebuildResultRows()
	m.resTable.SetHeight(2)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}))
	got := next.(tuiModel)

	if got.resTable.Cursor() == 0 {
		t.Fatal("space moved nothing; the page key did not reach the list")
	}
	rows := got.resTable.Rows()
	at := got.resTable.Cursor()
	if rows[at][0] != ">" {
		t.Errorf("the caret is not on row %d after space: %q", at, rows[at][0])
	}
	if rows[0][0] == ">" {
		t.Error("the caret stayed where it was, so the widget moved the cursor behind our back")
	}
	if mark := rows[at][1]; mark != plain(mark) {
		t.Errorf("the new cursor row keeps its colour: %q", mark)
	}
}

func TestSpaceInAnOpenResultPagesTheOutput(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = true
	m.rebuildResultRows()
	was := m.resLine

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}))
	got := next.(tuiModel)

	if got.resTable.Cursor() != m.resTable.Cursor() {
		t.Error("space moved the list while an output pane was open")
	}
	if got.resLine == was && len(m.results[0].Commands) > 0 {
		t.Log("the output is shorter than a page, so the line did not move")
	}
	if !got.resOpen {
		t.Error("space closed the output")
	}
}
