package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/view"
)

func keysModel(t *testing.T, keybinds string, views map[string]string) tuiModel {
	t.Helper()
	root := t.TempDir()
	if keybinds != "" {
		path := filepath.Join(root, KeybindsFileName)
		if err := os.WriteFile(path, []byte(keybinds), 0o600); err != nil {
			t.Fatal(err)
		}
		app.KeybindsFile = path
		t.Cleanup(func() { app.KeybindsFile = "" })
	}
	if len(views) > 0 {
		dir := filepath.Join(root, view.DirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, text := range views {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		app.ViewsFolder = dir
		t.Cleanup(func() { app.ViewsFolder = "" })
	}
	return listModel(t, 3)
}

func pressOnList(m tuiModel, key string) tuiModel {
	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'x'}), key)
	return next.(tuiModel)
}

func TestTheKeysIontkShipsAreBoundThroughTheTable(t *testing.T) {
	m := keysModel(t, "", nil)
	if got := firstKey(m.binds(), scrElements, "filter"); got != "/" {
		t.Errorf("filter is on %q, want /", got)
	}
	if m2 := pressOnList(m, "/"); !m2.filterOn {
		t.Error("/ did not open the filter through the keymap")
	}
}

func TestAFileRebindsAKeyAndCanHandOneBack(t *testing.T) {
	m := keysModel(t, "screens:\n  elements:\n    f: filter\n    \"/\": none\n", nil)
	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the file was refused: %v", err)
	}

	if m2 := pressOnList(m, "f"); !m2.filterOn {
		t.Error("f does not open the filter the file put it on")
	}
	if m2 := pressOnList(m, "/"); m2.filterOn {
		t.Error("/ still opens the filter after the file took it back")
	}
	if got := m.legend(scrElements, "filter", "filter"); got != "f filter" {
		t.Errorf("the footer says %q, want it to name the key the file chose", got)
	}
}

func TestAScreensOwnFileBindsForThatScreenAlone(t *testing.T) {
	m := keysModel(t, "", map[string]string{
		"elements.yaml": "kind: list\ncolumns:\n  - name\nkeys:\n  alt+q: refresh\n",
	})
	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the file was refused: %v", err)
	}
	if got, ok := m.binds().action(scrElements, "alt+q"); !ok || got != "refresh" {
		t.Errorf("alt+q on the list = %q, %v; want the refresh the file bound", got, ok)
	}
	if _, ok := m.binds().action(scrMenu, "alt+q"); ok {
		t.Error("the list's own binding reached the menu")
	}
}

func TestAnActionWrittenInAFileRuns(t *testing.T) {
	m := keysModel(t, `screens:
  elements:
    alt+d: offline-only
actions:
  offline-only:
    - set:
        filter: offline
    - status: offline elements only
`, nil)
	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the file was refused: %v", err)
	}

	got := pressOnList(m, "alt+d")
	if got.filter.Value() != "offline" {
		t.Errorf("the filter holds %q, want what the action set", got.filter.Value())
	}
	if got.status != "offline elements only" {
		t.Errorf("status = %q, want the line the action wrote", got.status)
	}
}

func TestAnActionRunsAnotherAndStopsAtItself(t *testing.T) {
	m := keysModel(t, `screens:
  elements:
    alt+d: both
actions:
  both:
    - do: clear-selection
    - status: done
  loops:
    - do: loops
`, nil)
	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the file was refused: %v", err)
	}
	selectOnly(&m, "1/1")

	got := pressOnList(m, "alt+d")
	if got.selectedCount() != 0 {
		t.Error("the sequence did not run the built-in it named")
	}
	if got.status != "done" {
		t.Errorf("status = %q, want the step after it", got.status)
	}

	next, _, _ := got.runNamed(scrElements, "loops", 0)
	if st := next.(tuiModel).status; !strings.Contains(st, "runs itself") {
		t.Errorf("status = %q, want the loop stopped and named", st)
	}
}

func TestAFileThatCouldNotBindIsRefusedWithTheReason(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"an action nothing runs", "keys:\n  alt+d: reboot-the-lot\n", "reboot-the-lot"},
		{"a screen there is none of", "screens:\n  shell:\n    f: filter\n", `"shell" is not a screen`},
		{"a verb iontk has not got", "actions:\n  x:\n    - post: /sdwan/v2.0/api/x\n", "post is not a verb"},
		{"a place there is none of", "actions:\n  x:\n    - goto: the-moon\n", "the-moon"},
		{"a setting there is none of", "actions:\n  x:\n    - set:\n        colour: red\n", `sets "colour"`},
		{"a step that says nothing", "actions:\n  x:\n    - {}\n", "says nothing"},
		{"an action with no steps", "actions:\n  x: []\n", "does nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keysModel(t, tt.yaml, nil)
			if m.keysErr == "" {
				t.Fatalf("%q loaded", tt.yaml)
			}
			if !strings.Contains(m.keysErr, tt.want) {
				t.Errorf("keysErr = %q, want it to name %q", m.keysErr, tt.want)
			}
			if m.keys.bound != nil {
				t.Error("the refused file was taken anyway")
			}
			if m2 := pressOnList(m, "/"); !m2.filterOn {
				t.Error("the keys iontk ships stopped working when a file was refused")
			}
		})
	}
}

func TestTheKeysSectionSaysWhatIsBoundAndAltEOpensIt(t *testing.T) {
	m := keysModel(t, "screens:\n  elements:\n    f: filter\n", nil)
	m.phase, m.setSection, m.width = phaseSettings, secKeys, 100

	out := plain(strings.Join(m.keysContent(), "\n"))
	for _, want := range []string{"Element List", "f", "filter", "Everywhere", "edit-yaml", KeybindsFileName} {
		if !strings.Contains(out, want) {
			t.Errorf("the Keys section does not mention %q:\n%s", want, out)
		}
	}

	mm := openEditor(t, m)
	if got := mm.edit.target.what; got != KeybindsFileName {
		t.Errorf("the editor opened on %q, want %q", got, KeybindsFileName)
	}
}

func TestTheShippedKeybindsExampleLoads(t *testing.T) {
	km := defaultKeymap()
	b, err := os.ReadFile("../../examples/keybinds.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := km.readBytes(b, scrAny, "keybinds.yaml"); err != nil {
		t.Fatalf("examples/keybinds.yaml: %v", err)
	}
	if err := km.check(); err != nil {
		t.Fatalf("examples/keybinds.yaml: %v", err)
	}
	if got := firstKey(km, scrElements, "filter"); got != "f" {
		t.Errorf("the example binds filter to %q, want f", got)
	}
}

func scriptModel(t *testing.T, keybinds string, files map[string]string) tuiModel {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "actions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.ActionsFolder = dir
	t.Cleanup(func() { app.ActionsFolder = "" })
	return keysModel(t, keybinds, nil)
}

func TestALuaActionIsBoundLikeAnyOtherAndItsEffectsLand(t *testing.T) {
	m := scriptModel(t, "screens:\n  elements:\n    alt+o: narrow\n", map[string]string{
		"narrow.lua": `
return function(ctx)
  if ctx.state.selection > 0 then
    ctx.fail("clear the selection first")
  end
  ctx.set("filter", "offline")
  ctx.status("offline only, on " .. ctx.state.tenant)
end
`})
	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the script was refused: %v", err)
	}
	if !m.hasAction("narrow") {
		t.Fatalf("actions = %v, want the file's own name", m.actionNames())
	}

	next, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModAlt}), "alt+o")
	m = next.(tuiModel)
	if m.scriptOn != "narrow" {
		t.Fatalf("scriptOn = %q, want the script marked as running", m.scriptOn)
	}
	if cmd == nil {
		t.Fatal("the keypress started nothing")
	}

	msg, ok := cmd().(scriptDoneMsg)
	if !ok {
		t.Fatalf("message = %T, want the script's own", msg)
	}
	if msg.err != "" {
		t.Fatalf("the script failed: %s", msg.err)
	}
	done, _ := m.onScriptDone(msg)
	got := done.(tuiModel)

	if got.filter.Value() != "offline" {
		t.Errorf("the filter holds %q, want what the script set", got.filter.Value())
	}
	if !strings.Contains(got.status, "offline only, on") {
		t.Errorf("status = %q, want the line the script wrote", got.status)
	}
	if got.scriptOn != "" {
		t.Error("the script is still marked as running")
	}
}

func TestASecondPressIsRefusedWhileAScriptRuns(t *testing.T) {
	m := scriptModel(t, "screens:\n  elements:\n    alt+o: slow\n", map[string]string{
		"slow.lua": "return function(ctx) ctx.status(\"done\") end",
	})
	m.scriptOn = "slow"

	next, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModAlt}), "alt+o")
	if cmd != nil {
		t.Error("a second press started the script again")
	}
	if got := next.(tuiModel).status; !strings.Contains(got, "still running") {
		t.Errorf("status = %q, want it to say the script is still going", got)
	}
}

func TestAScriptThatWillNotLoadLeavesTheKeysAlone(t *testing.T) {
	m := scriptModel(t, "", map[string]string{"broken.lua": "return function(ctx"})
	if m.actionsErr() == "" {
		t.Fatal("a broken script loaded without a word")
	}
	if !strings.Contains(m.actionsErr(), "broken.lua") {
		t.Errorf("actionsErr = %q, want the file named", m.actionsErr())
	}
	if m2 := pressOnList(m, "/"); !m2.filterOn {
		t.Error("the keys iontk ships stopped working")
	}
}

func TestAnActionCannotBeWrittenTwice(t *testing.T) {
	m := scriptModel(t, "actions:\n  narrow:\n    - status: from yaml\n", map[string]string{
		"narrow.lua": "return function(ctx) ctx.status(\"from lua\") end",
	})
	if m.keysErr == "" || !strings.Contains(m.keysErr, "twice") {
		t.Errorf("keysErr = %q, want the collision named", m.keysErr)
	}
}

func TestTheShippedLuaExamplesLoad(t *testing.T) {
	set, err := script.Load("../../examples/actions")
	if err != nil {
		t.Fatalf("the shipped actions do not load: %v", err)
	}
	for _, want := range []string{"offline-report", "wait-for-the-element"} {
		if !set.Has(want) {
			t.Errorf("Names() = %v, want %q among them", set.Names(), want)
		}
	}
}

func TestEnterOnTheKeysRowEditsTheKeybinds(t *testing.T) {
	m := keysModel(t, "screens:\n  elements:\n    f: filter\n", nil)
	m.phase, m.setSection, m.setFocus = phaseSettings, secKeys, focusContent

	if got := plain(m.sectionContent()[0]); !strings.Contains(got, "Keybinds") {
		t.Errorf("the first row is %q, want the one enter acts on", got)
	}
	if got := m.sectionHint(); !strings.Contains(got, "enter edit") {
		t.Errorf("hint = %q, want it to offer the edit", got)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := next.(tuiModel)
	if mm.phase != phaseEdit {
		t.Fatalf("enter left phase %v, want the editor", mm.phase)
	}
	if got := mm.edit.target.what; got != KeybindsFileName {
		t.Errorf("the editor opened on %q, want %q", got, KeybindsFileName)
	}
	if !strings.Contains(mm.edit.area.Value(), "f: filter") {
		t.Errorf("the pane holds %q, want the file as it stands", mm.edit.area.Value())
	}
}
