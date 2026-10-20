package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
)

func clipboardOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("the copy issued no command, so nothing reached the clipboard")
	}
	return fmt.Sprint(cmd())
}

func TestTheElementListNoLongerCopiesIds(t *testing.T) {
	m := pickModel(t)

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	if cmd != nil {
		t.Errorf("alt+y on the element list still copied %q", fmt.Sprint(cmd()))
	}
	if strings.Contains(m.status, "copied") {
		t.Errorf("status = %q, want nothing copied", m.status)
	}
	if out := plain(m.viewPicking()); strings.Contains(out, "copy id") {
		t.Errorf("the element list still advertises copy id:\n%s", out)
	}
}

func TestCopyingAnOpenResultTakesItsOutput(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = true

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got := clipboardOf(t, cmd)
	if !strings.Contains(got, "dump device status") || !strings.Contains(got, "up 5 days") {
		t.Errorf("clipboard = %q, want the command and its output", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("clipboard = %q, want the styling stripped", got)
	}
}

func TestCopyingTheResultsPageTakesTheRunAsJSON(t *testing.T) {
	m := resultsModel(t)
	m.resOpen = false

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got := clipboardOf(t, cmd)

	var rows []map[string]any
	if err := json.Unmarshal([]byte(got), &rows); err != nil {
		t.Fatalf("clipboard is not JSON: %v\n%s", err, got)
	}
	if len(rows) != m.completedCount() {
		t.Errorf("clipboard has %d results, want the %d completed", len(rows), m.completedCount())
	}
	if !strings.Contains(m.status, "json") {
		t.Errorf("status = %q, want it to say the run went as json", m.status)
	}
}

func TestTheMacroPageNoLongerCopies(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	if cmd != nil {
		t.Errorf("alt+y on the macro page still copied %q", fmt.Sprint(cmd()))
	}
	if strings.Contains(m.status, "copied") {
		t.Errorf("status = %q, want nothing copied", m.status)
	}
	if out := plain(m.viewMacros()); strings.Contains(out, "alt+y") {
		t.Errorf("the macro page still offers alt+y:\n%s", out)
	}
}

func TestAnOversizeCopyKeepsTheTailAndSaysItWasCut(t *testing.T) {
	m := pickModel(t)
	var b strings.Builder
	for b.Len() < clipLimit*2 {
		fmt.Fprintf(&b, "line %06d\n", b.Len())
	}
	full := b.String()

	nm, cmd := m.copyText("scrollback", full)
	got := clipboardOf(t, cmd)
	if len(got) > clipLimit {
		t.Errorf("clipboard is %d bytes, want it capped at %d", len(got), clipLimit)
	}
	if !strings.HasSuffix(strings.TrimRight(full, "\n"), got) {
		t.Error("the copy kept the start rather than the newest output")
	}
	if strings.HasPrefix(got, "line") == false {
		t.Errorf("the copy starts mid-line: %q", got[:20])
	}
	if !strings.Contains(nm.status, "would not fit") {
		t.Errorf("status = %q, want it to admit the copy was cut", nm.status)
	}
}

func TestARunTooBigForTheClipboardOffersTheFileInstead(t *testing.T) {
	m := resultsModel(t)
	big := strings.Repeat("x", clipLimit)
	m.results[0].Commands[0].Output = big

	nm, cmd := m.copyRunJSON()
	if cmd != nil {
		t.Error("an oversize run was sent to the clipboard anyway")
	}
	if !strings.Contains(nm.(tuiModel).status, "saves it to a file") {
		t.Errorf("status = %q, want it to offer the save", nm.(tuiModel).status)
	}
}

func TestCopyingAShellTakesItsScrollback(t *testing.T) {
	m, conn := favModel(t)
	conn.feed("S0100-RTR-01# dump device status\r\nSerial: 1234\r\n")

	s := m.sessions.Active()
	waitForTUI(t, func() bool { return strings.Contains(s.Buffer(), "Serial: 1234") })

	m, cmd := pressSessionKey(t, m, altKey('y'))
	got := clipboardOf(t, cmd)
	if !strings.Contains(got, "dump device status") || !strings.Contains(got, "Serial: 1234") {
		t.Errorf("clipboard = %q, want what the device printed", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("clipboard = %q, want the escape codes stripped", got)
	}
	if !strings.Contains(m.status, "scrollback") {
		t.Errorf("status = %q, want it to name what was copied", m.status)
	}
	if strings.Contains(conn.sent(), "y") {
		t.Errorf("alt+y reached the device: %q", conn.sent())
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

func TestCopyingADataPaneTakesTheWholePagePlain(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	mm, cmd := m.Update(tea.KeyPressMsg(altKey('y')))
	m = mm.(tuiModel)
	got := clipboardOf(t, cmd)

	for _, want := range []string{el.Name, "Overview", "Interfaces", "Circuits"} {
		if !strings.Contains(got, want) {
			t.Errorf("clipboard is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Error("the copied page carries escape codes")
	}
	if n := strings.Count(got, "\n"); n < 24 {
		t.Errorf("the copy has %d lines, want the whole page rather than the window", n)
	}
}
