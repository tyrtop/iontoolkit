package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
)

func exported(t *testing.T, m tuiModel, cmd tea.Cmd) (string, string) {
	t.Helper()
	if cmd != nil {
		t.Fatalf("the export issued a command (%v); nothing should reach the clipboard", cmd())
	}

	where := exportPath(t, m.status)
	b, err := os.ReadFile(where)
	if err != nil {
		t.Fatalf("read the export: %v", err)
	}
	return string(b), where
}

func exportPath(t *testing.T, status string) string {
	t.Helper()
	i := strings.LastIndex(status, " to ")
	if i < 0 {
		t.Fatalf("status = %q, want it to name the file it wrote", status)
	}
	return status[i+len(" to "):]
}

func TestTheElementListNeitherCopiesNorExports(t *testing.T) {
	m := pickModel(t)

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	if cmd != nil {
		t.Errorf("alt+y on the element list did something: %q", fmt.Sprint(cmd()))
	}
	if got := mm.(tuiModel).status; strings.Contains(got, "exported") {
		t.Errorf("status = %q, want nothing exported from the element list", got)
	}
	if out := plain(m.viewPicking()); strings.Contains(out, "copy id") {
		t.Errorf("the element list still advertises copy id:\n%s", out)
	}
}

func TestExportingAnOpenResultWritesItsOutput(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = true

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got, where := exported(t, m, cmd)

	if !strings.Contains(got, "dump device status") || !strings.Contains(got, "up 5 days") {
		t.Errorf("export = %q, want the command and its output", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("export = %q, want the styling stripped", got)
	}
	if filepath.Ext(where) != ".txt" {
		t.Errorf("export landed at %q, want a .txt", where)
	}
	if !strings.Contains(where, m.store.ExportsDir()) {
		t.Errorf("export landed at %q, want it under %q", where, m.store.ExportsDir())
	}
}

func TestExportingTheResultsPageWritesTheRunAsJSON(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = false

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got, where := exported(t, m, cmd)

	var rows []map[string]any
	if err := json.Unmarshal([]byte(got), &rows); err != nil {
		t.Fatalf("the export is not JSON: %v\n%s", err, got)
	}
	if len(rows) != m.completedCount() {
		t.Errorf("the export has %d results, want the %d completed", len(rows), m.completedCount())
	}
	if filepath.Ext(where) != ".json" {
		t.Errorf("the run landed at %q, want a .json", where)
	}
}

func TestARunTooBigForAClipboardIsExportedWhole(t *testing.T) {
	m := resultsModel(t)
	big := strings.Repeat("x", 200<<10)
	m.results[0].Commands[0].Output = big

	next, cmd := m.exportRunJSON()
	got, _ := exported(t, next.(tuiModel), cmd)
	if !strings.Contains(got, big) {
		t.Error("the export dropped output that would not have fitted a clipboard")
	}
}

func TestExportingAShellKeepsTheWholeBuffer(t *testing.T) {
	m, conn := favModel(t)
	conn.feed("S0100-RTR-01# dump device status\r\nSerial: 1234\r\n")

	s := m.sessions.Active()
	waitForTUI(t, func() bool { return strings.Contains(s.Buffer(), "Serial: 1234") })

	m, cmd := pressSessionKey(t, m, altKey('y'))
	got, where := exported(t, m, cmd)

	if !strings.Contains(got, "dump device status") || !strings.Contains(got, "Serial: 1234") {
		t.Errorf("export = %q, want what the device printed", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("export = %q, want the escape codes stripped", got)
	}
	if !strings.Contains(where, "s0100-rtr-01") {
		t.Errorf("export landed at %q, want the session named in the filename", where)
	}
	if strings.Contains(conn.sent(), "\x1be") {
		t.Errorf("alt+y reached the device: %q", conn.sent())
	}
}

func TestALongScrollbackKeepsItsHeadAsWellAsItsTail(t *testing.T) {
	m := pickModel(t)
	var b strings.Builder
	for b.Len() < 200<<10 {
		fmt.Fprintf(&b, "line %06d\n", b.Len())
	}
	full := strings.TrimRight(b.String(), "\n")

	next, cmd := m.exportText("scrollback", "shell", "txt", full)
	got, _ := exported(t, next, cmd)

	if !strings.HasPrefix(got, "line 000000") {
		t.Errorf("the export starts %q, want the oldest line; a file has no payload cap", got[:20])
	}
	if !strings.Contains(got, full) {
		t.Error("the export is missing some of the buffer")
	}
}

func TestAnEmptyPageExportsNothing(t *testing.T) {
	m := pickModel(t)

	next, cmd := m.exportText("scrollback", "shell", "txt", "   \n\n")
	if cmd != nil {
		t.Error("an empty page wrote something")
	}
	if !strings.Contains(next.status, "nothing to export") {
		t.Errorf("status = %q, want it to say there was nothing", next.status)
	}
	if entries, _ := os.ReadDir(next.store.ExportsDir()); len(entries) != 0 {
		t.Errorf("exports/ holds %d files, want none", len(entries))
	}
}

func TestAltYIsTheExportKeyAndTheFootersSayIt(t *testing.T) {
	m := resultsModel(t)
	if got := plain(m.viewResults()); !strings.Contains(got, "alt+y export") {
		t.Errorf("the results footer does not offer alt+y:\n%s", got)
	}

	mac := macroModel(t)
	if got := plain(mac.viewMacros()); strings.Contains(got, "alt+y") {
		t.Errorf("the macro page has nothing to export and offers alt+y anyway:\n%s", got)
	}

	if got := plain(m.viewResults()); strings.Contains(got, "alt+e") {
		t.Errorf("the results page is not drawn from a file and offers alt+e:\n%s", got)
	}
}

func TestExportingAFavouriteSlotWritesItsCommand(t *testing.T) {
	m, _ := favModel(t)
	if err := m.cmds.SetFavourite(3, "dump interface status all", "ion 1200"); err != nil {
		t.Fatal(err)
	}
	m.favPick, m.favCursor = true, 3

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	got, where := exported(t, mm.(tuiModel), cmd)

	if strings.TrimSpace(got) != "dump interface status all" {
		t.Errorf("export = %q, want the slot's command", got)
	}
	if !strings.Contains(where, "favourite-3") {
		t.Errorf("export landed at %q, want the slot named in the filename", where)
	}
}

func TestPastingCommandsIntoTheBatchBoxSplitsThem(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseCommand
	m.cmdInput.Focus()

	mm, _ := m.Update(tea.PasteMsg{Content: "dump device status\ndump interface status all\n\n uptime \n"})
	m = mm.(tuiModel)

	got := app.SplitCommands([]string{m.cmdInput.Value()})
	want := []string{"dump device status", "dump interface status all", "uptime"}
	if len(got) != len(want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPastingCommandsIntoTheMacroFormFillsALinePerCommand(t *testing.T) {
	m := macroModel(t)
	next, _ := m.openMacroForm(config.Macro{})
	m = next.(tuiModel)
	m.formCursor = 1

	mm, _ := m.Update(tea.PasteMsg{Content: "one\ntwo\nthree"})
	m = mm.(tuiModel)

	if got := m.macroFormCommands(); len(got) != 3 {
		t.Fatalf("commands = %q, want three", got)
	}
	if m.formValue(len(m.form)-1) != "" {
		t.Error("the form has no empty line left to type the next command on")
	}
	if m.formCursor != len(m.form)-1 {
		t.Errorf("formCursor = %d, want the empty line at the bottom", m.formCursor)
	}
	for i := 1; i < len(m.form); i++ {
		if want := macroCommandLabel(i - 1); m.form[i].label != want {
			t.Errorf("field %d is labelled %q, want %q", i, m.form[i].label, want)
		}
	}
}

func TestExportingADataPaneWritesTheWholePagePlain(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got, _ := exported(t, m, cmd)

	for _, want := range []string{el.Name, "Overview", "Interfaces", "Circuits"} {
		if !strings.Contains(got, want) {
			t.Errorf("the export is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Error("the exported page carries escape codes")
	}
	if n := strings.Count(got, "\n"); n < 24 {
		t.Errorf("the export has %d lines, want the whole page rather than the window", n)
	}
}
