package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

func macroModel(t *testing.T) tuiModel {
	t.Helper()
	m := historyModel(t)
	for _, mac := range []config.Macro{
		{Name: "wan check", Commands: []string{"dump interface status all", "dump vpn count"}, Model: "ion 1200"},
		{Name: "ha check", Commands: []string{"dump spoke-ha status"}, Model: "ion 1200"},
	} {
		if err := m.cmds.SetMacro(mac); err != nil {
			t.Fatal(err)
		}
	}
	m.width = 120
	next, _ := m.openMacros()
	return next.(tuiModel)
}

func TestTheMacroPageListsFiltersAndDeletes(t *testing.T) {
	m := macroModel(t)
	if m.phase != phaseMacros {
		t.Fatalf("phase = %v, want the macro page", m.phase)
	}
	if m.macroCursor != 1 {
		t.Errorf("cursor = %d, want the first macro", m.macroCursor)
	}

	got := plain(m.viewMacros())
	for _, want := range []string{"macros", "+ New Macro", "wan check", "2 command(s)", "ion 1200", "dump vpn count"} {
		if !strings.Contains(got, want) {
			t.Errorf("the page is missing %q:\n%s", want, got)
		}
	}

	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}), "/")
	m = next.(tuiModel)
	if !m.macroFilterOn {
		t.Fatal("/ did not open the filter")
	}
	m = typeString(t, m, "spoke")
	if rows := m.macroRows(); len(rows) != 1 || rows[0].Name != "ha check" {
		t.Errorf("filtered rows = %+v, want the macro whose command matches", rows)
	}
	if mac, ok := m.macroAt(m.macroCursor); !ok || mac.Name != "ha check" {
		t.Errorf("cursor %d = %+v, %v, want the match", m.macroCursor, mac, ok)
	}

	next, _ = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}), "esc")
	m = next.(tuiModel)
	if m.macroFilterOn || len(m.macroRows()) != 2 {
		t.Errorf("esc did not clear the filter: on=%v rows=%d", m.macroFilterOn, len(m.macroRows()))
	}

	m.macroCursor = 1
	next, cmd := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}), "ctrl+d")
	m = next.(tuiModel)
	if _, ok := m.cmds.Macro("wan check"); !ok {
		t.Fatal("ctrl+d deleted the macro")
	}
	if cmd != nil {
		t.Error("ctrl+d queued a write")
	}

	next, cmd = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
	m = next.(tuiModel)
	if _, ok := m.cmds.Macro("wan check"); !ok {
		t.Fatal("one x deleted the macro")
	}
	if !strings.Contains(m.status, "x again") {
		t.Errorf("status = %q, want it to ask for the second press", m.status)
	}
	next, cmd = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
	m = next.(tuiModel)
	if _, ok := m.cmds.Macro("wan check"); ok {
		t.Error("the second x left the macro in place")
	}
	if cmd == nil {
		t.Fatal("deleting queued no write")
	}
	runSave(t, cmd)
	saved, err := m.store.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Macro("wan check"); ok {
		t.Error("the deletion was not written through")
	}
}

func TestAMacroIsGatedAsAUnit(t *testing.T) {
	m := macroModel(t)
	mac := config.Macro{Name: "risky", Commands: []string{
		"dump device status",
		"config banner hello",
	}}
	if err := m.cmds.SetMacro(mac); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.runMacro(mac, m.all)
	got := next.(tuiModel)
	if got.phase == phaseRunning {
		t.Fatal("a macro with a write in it started running")
	}
	if cmd != nil {
		t.Error("the refused macro queued work")
	}
	for _, want := range []string{"risky", "not read-only", "config banner hello"} {
		if !strings.Contains(got.status, want) {
			t.Errorf("status = %q, want it to name %q", got.status, want)
		}
	}
	if got.events != nil {
		t.Error("the run stream was opened for a refused macro")
	}

	m.cfg.AllowWrites = true
	next, _ = m.runMacro(mac, m.all)
	if status := next.(tuiModel).status; strings.Contains(status, "not read-only") {
		t.Errorf("AllowWrites did not lift the guard: %q", status)
	}
}

func TestAMacroNeedsSomethingToRunOn(t *testing.T) {
	m := macroModel(t)
	mac, _ := m.cmds.Macro("ha check")

	next, cmd := m.runMacro(mac, nil)
	got := next.(tuiModel)
	if cmd != nil || got.phase == phaseRunning {
		t.Error("the macro ran with nothing selected")
	}
	if !strings.Contains(got.status, "nothing selected") {
		t.Errorf("status = %q, want it to say what is missing", got.status)
	}
}

func TestACrossModelMacroAsksOnceAndThenNeverAgain(t *testing.T) {
	m := macroModel(t)
	mac, _ := m.cmds.Macro("ha check")
	on := rowsOfModel(m, "ion 3200")

	next, cmd := m.runMacro(mac, on)
	m = next.(tuiModel)
	if m.askMacro != "ha check" {
		t.Fatalf("askMacro = %q, want the macro held on the question", m.askMacro)
	}
	if cmd != nil {
		t.Error("the macro ran before the question was answered")
	}
	prompt := plain(m.macroWarnPrompt())
	for _, want := range []string{"other hardware", "ion 1200", "ion 3200", "run it anyway"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the question is missing %q:\n%s", want, prompt)
		}
	}

	next, _ = m.onMacroWarnKey("n")
	declined := next.(tuiModel)
	if declined.askMacro != "" || declined.cmds.Acked(warnMacroModel) {
		t.Errorf("declining acknowledged the warning: askMacro=%q acked=%v",
			declined.askMacro, declined.cmds.Acked(warnMacroModel))
	}

	next, _ = declined.runMacro(mac, on)
	m = next.(tuiModel)
	next, cmd = m.onMacroWarnKey("y")
	m = next.(tuiModel)
	if !m.cmds.Acked(warnMacroModel) {
		t.Error("yes did not acknowledge the warning")
	}
	if cmd == nil {
		t.Fatal("the acknowledgement queued no write")
	}
	runSave(t, cmd)
	saved, err := m.store.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Acked(warnMacroModel) {
		t.Error("the acknowledgement was not written through, so it would ask again next run")
	}

	next, _ = m.runMacro(mac, on)
	again := next.(tuiModel)
	if again.askMacro != "" {
		t.Error("the warning was asked a second time after being acknowledged")
	}
	if !strings.Contains(plain(again.viewMacros()), "ion 1200") {
		t.Errorf("the row does not say what the macro was built on:\n%s", plain(again.viewMacros()))
	}
}

func TestAMacroWithNoModelDoesNotWarn(t *testing.T) {
	m := macroModel(t)
	mac := config.Macro{Name: "anywhere", Commands: []string{"dump device status"}}
	if err := m.cmds.SetMacro(mac); err != nil {
		t.Fatal(err)
	}
	next, _ := m.runMacro(mac, m.all)
	if got := next.(tuiModel); got.askMacro != "" {
		t.Errorf("askMacro = %q, want no question for a macro with no model", got.askMacro)
	}
}

func rowsOfModel(m tuiModel, model string) []elementRow {
	var out []elementRow
	for _, r := range m.all {
		if r.Model == model {
			out = append(out, r)
		}
	}
	return out
}

func TestPickingAMacroArmsTheElementList(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1
	mac, _ := m.macroAt(1)

	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	m = next.(tuiModel)
	if m.phase != phasePicking {
		t.Fatalf("phase = %v, want the element list", m.phase)
	}
	if m.mode != modeMacro || m.armedMacro != mac.Name {
		t.Fatalf("mode = %v, armed = %q, want macro mode holding %q", m.mode, m.armedMacro, mac.Name)
	}
	got := plain(m.viewPicking())
	for _, want := range []string{"Macro Mode", mac.Name, "enter run " + mac.Name} {
		if !strings.Contains(got, want) {
			t.Errorf("the list is missing %q:\n%s", want, got)
		}
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModAlt}))
	after := next.(tuiModel)
	if after.mode == modeMacro || after.armedMacro != "" {
		t.Errorf("alt+m left mode %v armed with %q", after.mode, after.armedMacro)
	}
}

func TestEnterInMacroModeRunsTheArmedMacro(t *testing.T) {
	m := macroModel(t)
	mac := config.Macro{Name: "risky", Commands: []string{"config banner hello"}}
	if err := m.cmds.SetMacro(mac); err != nil {
		t.Fatal(err)
	}
	m.mode = modeMacro
	m.armedMacro = "risky"
	m.phase = phasePicking
	m.rebuildRows()

	next, _ := m.onPickEnter()
	got := next.(tuiModel)
	if got.phase == phaseRunning {
		t.Fatal("a macro with a write in it ran from the element list")
	}
	if !strings.Contains(got.status, "not read-only") {
		t.Errorf("status = %q, want the macro refused by name", got.status)
	}

	m.cmds.RemoveMacro("risky")
	next, _ = m.onPickEnter()
	gone := next.(tuiModel)
	if gone.mode == modeMacro || !strings.Contains(gone.status, "gone") {
		t.Errorf("mode = %v, status = %q, want macro mode dropped", gone.mode, gone.status)
	}
}

func TestTheMacroFormGrowsALineAtATime(t *testing.T) {
	m := macroModel(t)
	for _, r := range m.all {
		if r.Model == "ion 1200" {
			m.selected[r.key()] = true
		}
	}

	m.macroCursor = 0
	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	m = next.(tuiModel)
	if m.formKind != formMacro {
		t.Fatalf("formKind = %v, want the macro form", m.formKind)
	}
	if len(m.form) != 2 {
		t.Fatalf("form = %d fields, want a name and one command line", len(m.form))
	}
	if got := plain(m.viewForm()); !strings.Contains(got, "+ another command") {
		t.Errorf("the form does not offer another command:\n%s", got)
	}

	m.form[0].input.SetValue("boot check")
	m.form[1].input.SetValue("dump device status")

	m.formCursor = 1
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	m = next.(tuiModel)
	if len(m.form) != 3 || m.formCursor != 2 {
		t.Fatalf("form = %d fields, cursor %d, want a third field and the cursor on it", len(m.form), m.formCursor)
	}
	if got := m.form[2].label; got != "command 2" {
		t.Errorf("label = %q, want the commands numbered in run order", got)
	}
	m.form[2].input.SetValue("dump interface status all")

	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	m = next.(tuiModel)
	if len(m.form) != 4 {
		t.Fatalf("form = %d fields, want the third command line", len(m.form))
	}
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	m = next.(tuiModel)
	if len(m.form) != 4 {
		t.Errorf("form = %d fields after tab on an empty line, want it unchanged", len(m.form))
	}

	next, cmd := m.submitForm()
	m = next.(tuiModel)
	mac, ok := m.cmds.Macro("boot check")
	if !ok {
		t.Fatal("the macro was not saved")
	}
	want := []string{"dump device status", "dump interface status all"}
	if strings.Join(mac.Commands, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v in order, with the empty line dropped", mac.Commands, want)
	}
	if mac.Model != "ion 1200" {
		t.Errorf("model = %q, want the selection's", mac.Model)
	}
	runSave(t, cmd)
}

func TestBackspaceOnAnEmptyLineRemovesIt(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1
	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"}), "e")
	m = next.(tuiModel)
	if len(m.form) != 4 {
		t.Fatalf("form = %d fields, want the macro's two commands plus an empty line", len(m.form))
	}

	m.formCursor = 3
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), "backspace")
	m = next.(tuiModel)
	if len(m.form) != 3 {
		t.Fatalf("form = %d fields, want the empty line removed", len(m.form))
	}
	if m.formCursor != 2 {
		t.Errorf("cursor = %d, want the line above the one removed", m.formCursor)
	}

	m.formCursor = 1
	m.form[1].input.SetValue("")
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), "backspace")
	m = next.(tuiModel)
	if len(m.form) != 2 {
		t.Fatalf("form = %d fields, want the emptied line removed", len(m.form))
	}
	if got := m.formValue(1); got != "dump vpn count" {
		t.Errorf("command 1 = %q, want what was below the removed line", got)
	}
	if got := m.form[1].label; got != "command 1" {
		t.Errorf("label = %q, want the lines renumbered", got)
	}

	m.formCursor = 1
	m.form[1].input.SetValue("")
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), "backspace")
	m = next.(tuiModel)
	if len(m.form) != 2 {
		t.Errorf("form = %d fields, want the last command line kept", len(m.form))
	}

	m.formCursor = 0
	m.form[0].input.SetValue("")
	next, _ = m.onFormKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), "backspace")
	if got := len(next.(tuiModel).form); got != 2 {
		t.Errorf("form = %d fields after backspace on the name, want it untouched", got)
	}
}

func TestEditingAMacroReopensItsCommands(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1

	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"}), "e")
	m = next.(tuiModel)
	if m.formKind != formMacro {
		t.Fatalf("formKind = %v, want the macro form", m.formKind)
	}
	if len(m.form) != 4 {
		t.Fatalf("form = %d fields, want the macro's commands plus an empty line", len(m.form))
	}
	if got := m.formValue(1); got != "dump interface status all" {
		t.Errorf("first command = %q, want the saved one", got)
	}

	old := m.formMacroName
	m.form[0].input.SetValue(old + " v2")
	next, _ = m.submitForm()
	m = next.(tuiModel)
	if _, ok := m.cmds.Macro(old); ok {
		t.Errorf("%q survived the rename", old)
	}
	if mac, ok := m.cmds.Macro(old + " v2"); !ok || len(mac.Commands) != 2 {
		t.Errorf("renamed macro = %+v, %v, want its commands kept", mac, ok)
	}

	m.form = []formField{newField("name", "", false), newMacroCommand(0)}
	m.formKind = formMacro
	next, _ = m.submitForm()
	if !strings.Contains(next.(tuiModel).status, "needs a name") {
		t.Errorf("status = %q, want the empty form refused", next.(tuiModel).status)
	}
}

func TestAMixedSelectionRecordsNoModel(t *testing.T) {
	m := macroModel(t)
	for _, r := range m.all {
		m.selected[r.key()] = true
	}
	if got := m.selectionModel(); got != "" {
		t.Errorf("selectionModel() = %q, want empty for a mixed selection", got)
	}
}

func TestTheMenuDoesNotOpenTheListInMacroMode(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1
	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	m = next.(tuiModel)
	if m.mode != modeMacro {
		t.Fatalf("mode = %v, want macro mode after picking one", m.mode)
	}

	m.phase = phaseMenu
	m.menuCursor = 0
	next, _ = m.onMenuKey("enter")
	got := next.(tuiModel)
	if got.mode == modeMacro || got.armedMacro != "" {
		t.Errorf("mode = %v armed with %q, want the macro put down", got.mode, got.armedMacro)
	}
	if got.mode != modeShell {
		t.Errorf("mode = %v, want the saved default", got.mode)
	}
}

func TestTheMacroCursorHighlightsTheWholeLine(t *testing.T) {
	m := macroModel(t)
	m.width = 120

	m.macroCursor = 1
	out := m.viewMacros()
	rows := m.macroRows()
	if len(rows) < 2 {
		t.Fatalf("%d macros, want at least two to tell the lines apart", len(rows))
	}

	var want string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(plain(line), rows[0].Name) {
			want = line
			break
		}
	}
	if want == "" {
		t.Fatalf("no line for %s:\n%s", rows[0].Name, out)
	}
	if !strings.Contains(want, "\x1b[1;35m") {
		t.Errorf("the cursor line is not highlighted:\n%q", want)
	}
	if n := strings.Count(want, "\x1b[0m") + strings.Count(want, "\x1b[m"); n > 1 {
		t.Errorf("the highlight is broken into %d runs:\n%q", n, want)
	}

	m.macroCursor = 0
	out = m.viewMacros()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(plain(line), "+ New Macro") {
			if !strings.Contains(line, "\x1b[1;35m") {
				t.Errorf("the new-macro row is not highlighted:\n%q", line)
			}
		}
	}
}

func TestLeavingTheElementListGoesBackToTheMacroPage(t *testing.T) {
	m := macroModel(t)
	m.all = []elementRow{{Element: api.Element{ID: "1", Name: "s0100-rtr-01"}, TSG: "1"}}
	m.matches = []int{0}

	next, _ := m.armMacro(m.cmds.Macros[0])
	m = next.(tuiModel)
	if m.phase != phasePicking || m.mode != modeMacro {
		t.Fatalf("phase = %v mode = %v, want the element list in macro mode", m.phase, m.mode)
	}

	for _, key := range []string{"q", "esc", "alt+w"} {
		back, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'q', Text: key}), key)
		if got := back.(tuiModel).phase; got != phaseMacros {
			t.Errorf("%s from a macro run went to phase %v, want the macro page", key, got)
		}
	}

	m.mode, m.armedMacro = modeShell, ""
	back, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}), "q")
	if got := back.(tuiModel).phase; got != phaseMenu {
		t.Errorf("q from the plain list went to phase %v, want the menu", got)
	}
	m.mode, m.armedMacro = modeMacro, m.cmds.Macros[0].Name
	m.phase, m.width, m.height = phasePicking, 140, 24
	m.rebuildRows()
	if got := plain(m.viewPicking()); !strings.Contains(got, "alt+w macros") {
		t.Errorf("the hint does not say where back goes:\n%s", got)
	}
}

func TestDeletingAMacroCanBeUndone(t *testing.T) {
	m := macroModel(t)
	before := len(m.macroRows())
	if before < 2 {
		t.Fatalf("need two macros, got %d", before)
	}

	m.macroCursor = 1
	mac, ok := m.macroAt(m.macroCursor)
	if !ok {
		t.Fatal("no macro under the cursor")
	}

	del := func() tea.Cmd {
		next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
		m = next.(tuiModel)
		next, cmd := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
		m = next.(tuiModel)
		return cmd
	}
	runSave(t, del())
	if _, still := m.cmds.Macro(mac.Name); still {
		t.Fatal("the macro survived two presses")
	}

	next, cmd := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}), "u")
	m = next.(tuiModel)
	back, ok := m.cmds.Macro(mac.Name)
	if !ok {
		t.Fatalf("u did not put it back: %s", m.status)
	}
	if !slices.Equal(back.Commands, mac.Commands) {
		t.Errorf("it came back as %v, want %v", back.Commands, mac.Commands)
	}
	if cmd == nil {
		t.Error("the undo queued no write, so it is back only until a restart")
	}
	runSave(t, cmd)
	saved, err := m.store.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Macro(mac.Name); !ok {
		t.Error("the undo was not written")
	}

	next, _ = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}), "u")
	if got := next.(tuiModel).status; !strings.Contains(got, "nothing to undo") {
		t.Errorf("status = %q with nothing left to undo", got)
	}
}

func TestTheMacroDeleteArmIsClearedByAnyOtherKey(t *testing.T) {
	m := macroModel(t)
	m.macroCursor = 1
	mac, _ := m.macroAt(m.macroCursor)

	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
	m = next.(tuiModel)
	if m.macroDel == "" {
		t.Fatal("x did not arm")
	}

	next, _ = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}), "up")
	m = next.(tuiModel)
	if m.macroDel != "" {
		t.Error("the arm survived a cursor move")
	}
	m.macroCursor = 1
	next, _ = m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}), "x")
	m = next.(tuiModel)
	if _, still := m.cmds.Macro(mac.Name); !still {
		t.Error("a disarmed x deleted on one press")
	}
}
