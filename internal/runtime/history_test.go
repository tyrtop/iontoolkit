package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func TestFavouritesAreSavedAndSurviveANewRuntime(t *testing.T) {
	rt, st := withConfig(t)
	if _, err := say(t, rt, "fav.set", map[string]string{"slot": "3", "command": "dump device status", "model": "ion 1200"}); err != nil {
		t.Fatal(err)
	}
	if _, err := say(t, rt, "fav.set", map[string]string{"slot": "10", "command": "x"}); err == nil {
		t.Error("slot 10 was taken")
	}

	again := loaded(t)
	cfg, _ := st.Config()
	again.UseConfig(st, cfg)
	said, err := say(t, again, "fav.list", nil)
	if err != nil || len(said) != 1 || !strings.Contains(said[0], "3  dump device status") || !strings.Contains(said[0], "ion 1200") {
		t.Fatalf("fav.list = %q, %v", said, err)
	}
	if _, err := say(t, again, "fav.clear", map[string]string{"slot": "3"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Favourite(3); ok {
		t.Error("slot 3 still full after fav.clear")
	}
}

func TestHistoryRanksTheModelInUseFirstAndNarrowsFuzzily(t *testing.T) {
	rt, _ := withConfig(t)
	rt.RecordUse("ion 1200", "dump device status", "dump device status", "ping 10.0.0.1")
	for i := 0; i < 5; i++ {
		rt.RecordUse("ion 3200", "dump interface status 8")
	}
	got := rt.Ranked("ion 1200", "")
	if len(got) != 3 || got[0].Command != "dump device status" || got[0].Uses != 2 || got[2].Model != "ion 3200" {
		t.Errorf("ranked = %+v", got)
	}
	if got := rt.Ranked("", "png"); len(got) != 1 || got[0].Command != "ping 10.0.0.1" {
		t.Errorf("narrowed = %+v", got)
	}
	if _, err := say(t, rt, "history.forget", map[string]string{"command": "ping 10.0.0.1", "model": "ion 1200"}); err != nil {
		t.Fatal(err)
	}
	if n := len(rt.History()); n != 2 {
		t.Errorf("%d left after forgetting one of three", n)
	}
}

func TestAMacroRemovedComesBackWithUndo(t *testing.T) {
	rt, _ := withConfig(t)
	if _, err := say(t, rt, "macro.set", map[string]string{"name": "wan check", "commands": "dump device status, dump interface status 1"}); err != nil {
		t.Fatal(err)
	}
	if m, ok := rt.Macro("WAN CHECK"); !ok || len(m.Commands) != 2 {
		t.Fatalf("macro = %+v %v", m, ok)
	}
	if _, err := say(t, rt, "macro.remove", map[string]string{"name": "wan check"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Macro("wan check"); ok {
		t.Fatal("still there after macro.remove")
	}
	said, err := say(t, rt, "macro.undo", nil)
	if err != nil || len(said) != 1 || said[0] != "wan check is back" {
		t.Errorf("undo = %q, %v", said, err)
	}
	if _, err := say(t, rt, "macro.undo", nil); err == nil {
		t.Error("a second undo found something to put back")
	}
}

func TestAMacroRunsOnTheSelectionAndAsksOnceAboutOtherHardware(t *testing.T) {
	rt := deviceRuntime(t)
	rows := rt.Elements()
	for i := range rows {
		rows[i].Model = "ion 1200"
	}
	rt.SetElements(rows)
	run(t, rt, "list.select-matches")
	if _, err := say(t, rt, "macro.set", map[string]string{"name": "status", "commands": "dump device status", "model": "ion 3200"}); err != nil {
		t.Fatal(err)
	}

	_, err := say(t, rt, "macro.run", map[string]string{"name": "status"})
	if err == nil || !strings.Contains(err.Error(), "anyway=yes") {
		t.Fatalf("err = %v, want the hardware warning", err)
	}
	said, err := say(t, rt, "macro.run", map[string]string{"name": "status", "anyway": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(said, "\n"); !strings.Contains(text, "FAKE0001") || !strings.Contains(text, "2 of 2 elements done") {
		t.Errorf("run printed:\n%s", text)
	}
	if !rt.Acked(WarnMacroModel) {
		t.Error("anyway=yes was not remembered")
	}
	if _, err := say(t, rt, "macro.run", map[string]string{"name": "status"}); err != nil {
		t.Errorf("asked again after it was answered: %v", err)
	}

	rt.SetMacro(configMacro("risky", "reboot"))
	if _, err := say(t, rt, "macro.run", map[string]string{"name": "risky"}); err == nil || !strings.Contains(err.Error(), "not read-only") {
		t.Errorf("a write macro ran without writes allowed: %v", err)
	}
}

func TestAFavouriteGoesOnTheSessionLineUnsent(t *testing.T) {
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	id := rt.Elements()[0].ID
	if _, err := rt.OpenSession(context.Background(), id, "acme-rtr-01", "111"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Sessions().CloseAll)
	rt.SetFavourite(1, "dump device status", "")
	if _, _, err := rt.UseFavourite(1); err != nil && !errors.Is(err, ErrLoggingIn) {
		t.Fatal(err)
	}
	if _, err := say(t, rt, "fav.use", map[string]string{"slot": "1"}); err != nil {
		t.Fatal(err)
	}
	if line := rt.Sessions().Active().Line(); !strings.Contains(line, "dump device status") {
		t.Errorf("line = %q", line)
	}
	rt.SetFavourite(2, "reboot", "")
	if _, _, err := rt.UseFavourite(2); err == nil {
		t.Error("a write went on the line without writes allowed")
	}
}

func configMacro(name string, cmds ...string) config.Macro {
	return config.Macro{Name: name, Commands: cmds}
}

func TestAMacroSavedWithNoModelTakesTheSelectionsAndARenameKeepsOne(t *testing.T) {
	rt := deviceRuntime(t)
	rows := rt.Elements()
	for i := range rows {
		rows[i].Model = "ion 1200"
	}
	rt.SetElements(rows)
	run(t, rt, "list.select-matches")
	if _, err := say(t, rt, "macro.set", map[string]string{"name": "status", "commands": "dump device status"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := rt.Macro("status"); m.Model != "ion 1200" {
		t.Errorf("model = %q, want the selection's", m.Model)
	}
	if _, err := say(t, rt, "macro.set", map[string]string{"name": "status v2", "commands": "dump device status", "was": "status"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Macro("status"); ok {
		t.Error("the old name survived the rename")
	}
	if _, ok := rt.Macro("status v2"); !ok {
		t.Error("the renamed macro is missing")
	}
	if _, err := say(t, rt, "macro.set", map[string]string{"name": "", "commands": "x", "was": "status v2"}); err == nil {
		t.Error("a rename to no name was taken")
	}
	if _, ok := rt.Macro("status v2"); !ok {
		t.Error("a refused rename lost the macro")
	}
}

func TestAMacroCommandGivenOnItsOwnKeepsItsComma(t *testing.T) {
	rt, _ := withConfig(t)
	c := &Call{Args: map[string]string{"name": "pair"}, Rest: []string{"dump interface status 1,2", "dump device status"}}
	if err := rt.Exec(c, "macro.set"); err != nil {
		t.Fatal(err)
	}
	if m, _ := rt.Macro("pair"); len(m.Commands) != 2 || m.Commands[0] != "dump interface status 1,2" {
		t.Errorf("commands = %q, want two, the first with its comma", m.Commands)
	}
}
