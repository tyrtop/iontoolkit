package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/view"
)

func openEditor(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg(altKey('e')))
	mm := next.(tuiModel)
	if !mm.edit.on || mm.phase != phaseEdit {
		t.Fatalf("alt+e did not open the editor: status %q", mm.status)
	}
	return mm
}

func typeInto(m tuiModel, text string) tuiModel {
	m.edit.area.SetValue(text)
	return m
}

func TestAltEOpensTheFileTheElementListIsDrawnFrom(t *testing.T) {
	m := openEditor(t, listModel(t, 3))

	if got := m.edit.target.what; got != elementsViewFile {
		t.Errorf("the editor opened on %q, want %q", got, elementsViewFile)
	}
	if got := m.edit.target.path; !strings.HasSuffix(got, filepath.Join(view.DirName, elementsViewFile)) {
		t.Errorf("path = %q, want the element list's file under views/", got)
	}
	if m.edit.note != "new file" {
		t.Errorf("note = %q, want it to say the file is new", m.edit.note)
	}

	seed := m.edit.area.Value()
	for _, want := range []string{"kind: " + view.KindList, "columns:",
		"name: site", `value: "{{ .name }}"`, "go: status", "when:"} {
		if !strings.Contains(seed, want) {
			t.Errorf("the starter does not mention %q:\n%s", want, seed)
		}
	}
	if !strings.Contains(seed, "go: "+colCaret) {
		t.Error("the starter does not say where the caret comes from")
	}
	if got := plain(m.viewEdit()); !strings.Contains(got, "ctrl+s save") {
		t.Errorf("the pane does not say how to save:\n%s", got)
	}
}

func TestSavingTheFileRedrawsTheList(t *testing.T) {
	m := openEditor(t, listModel(t, 3))
	m = typeInto(m, "kind: list\ncolumns:\n  - name\n  - state\n")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	m = next.(tuiModel)

	if m.edit.err != "" {
		t.Fatalf("ctrl+s refused a good file: %q", m.edit.err)
	}
	if !strings.Contains(m.edit.note, "saved") {
		t.Errorf("note = %q, want it to say what happened", m.edit.note)
	}
	b, err := os.ReadFile(m.edit.target.path)
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if !strings.Contains(string(b), "- state") {
		t.Errorf("the file holds %q, want what was typed", string(b))
	}
	if got := columnNames(&m); strings.Join(got, " ") != colCaret+" "+colName+" "+colState {
		t.Errorf("columns = %v, want the list the file just asked for", got)
	}
}

func TestAFileThatWouldNotLoadIsNotWritten(t *testing.T) {
	m := openEditor(t, listModel(t, 3))
	m = typeInto(m, "kind: list\ncolumns:\n  - uptime\n")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	m = next.(tuiModel)

	if m.edit.err == "" {
		t.Fatal("ctrl+s took a file that draws nothing")
	}
	if !strings.Contains(m.edit.err, "uptime") {
		t.Errorf("err = %q, want the column named", m.edit.err)
	}
	if _, err := os.Stat(m.edit.target.path); err == nil {
		t.Error("the refused file was written anyway")
	}
	if !m.edit.on {
		t.Error("the editor closed on a refusal, taking the buffer with it")
	}
}

func TestLeavingWithChangesTakesTwoPresses(t *testing.T) {
	m := openEditor(t, listModel(t, 3))
	m = typeInto(m, "kind: list\ncolumns:\n  - name\n")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = next.(tuiModel)
	if !m.edit.on {
		t.Fatal("one esc left the editor with changes in the buffer")
	}
	if !strings.Contains(m.edit.note, "esc again") {
		t.Errorf("note = %q, want it to say what the next esc does", m.edit.note)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = next.(tuiModel)
	if m.edit.on || m.phase != phasePicking {
		t.Errorf("the second esc left phase %v, edit on %v", m.phase, m.edit.on)
	}
}

func TestTheDataPaneEditsItsViewsAndExportsWithAltY(t *testing.T) {
	m := pickModel(t)
	m.runtime().SetDataTabs([]dataTab{{EID: "1", Name: "S0100-RTR-01", TSG: "1",
		Panes: []dataPane{{View: "Overview"}}}}, 0)
	m.phase = phaseData
	m.width = 200
	mm := openEditor(t, m)

	if got := mm.edit.target.what; got != "overview.yaml" {
		t.Errorf("the editor opened on %q, want the file of the view the page is showing", got)
	}
	if got := mm.edit.area.Value(); !strings.Contains(got, "kind: data") || !strings.Contains(got, "name: Overview") {
		t.Errorf("the starter is not the Overview view as iontk ships it:\n%s", got)
	}
	if got := plain(m.viewData()); !strings.Contains(got, "alt+y export") || !strings.Contains(got, editKeyHint) {
		t.Errorf("the footer offers neither key:\n%s", got)
	}
}

func TestAPageWithNoFileSaysSoRatherThanTypingTheKeyAtTheIon(t *testing.T) {
	m := resultsModel(t)
	next, cmd := m.Update(tea.KeyPressMsg(altKey('e')))
	mm := next.(tuiModel)

	if mm.phase == phaseEdit {
		t.Fatal("the results page opened an editor on nothing")
	}
	if cmd != nil {
		t.Error("alt+e did something on a page with no file")
	}
	if !strings.Contains(mm.status, "file") {
		t.Errorf("status = %q, want it to say the page is not drawn from one", mm.status)
	}
}
