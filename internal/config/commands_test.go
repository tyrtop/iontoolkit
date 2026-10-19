package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func at(min int) time.Time { return epoch.Add(time.Duration(min) * time.Minute) }

// The same command on the same model is one entry with a count; on another
// model it is a different command, since the ports and subsystems differ.
func TestRecordCountsUsesPerModel(t *testing.T) {
	var c CommandStore
	c.Record("dump device status", "ion 1200", at(0))
	c.Record("dump device status", "ion 1200", at(1))
	c.Record("dump device status", "ion 3200", at(2))
	// Typed with trailing space, which is what a device line looks like after
	// the palette inserts a completion.
	c.Record("  dump device status  ", "ion 1200", at(3))
	c.Record("", "ion 1200", at(4))

	if len(c.History) != 2 {
		t.Fatalf("history = %+v, want one entry per model", c.History)
	}
	got, ok := find(c.History, "dump device status", "ion 1200")
	if !ok {
		t.Fatal("the 1200's entry is missing")
	}
	if got.Uses != 3 {
		t.Errorf("uses = %d, want 3 — the padded one is the same command", got.Uses)
	}
	if !got.LastUsed.Equal(at(3)) {
		t.Errorf("last used = %v, want the most recent run at %v", got.LastUsed, at(3))
	}
}

func TestRankedPutsThisModelsCommandsFirst(t *testing.T) {
	var c CommandStore
	// Run far more often, but on another model.
	for i := 0; i < 5; i++ {
		c.Record("dump interface status 8", "ion 3200", at(i))
	}
	c.Record("dump device status", "ion 1200", at(10))
	c.Record("dump device status", "ion 1200", at(11))
	c.Record("ping 10.0.0.1", "ion 1200", at(12))

	want := []string{
		"dump device status",      // this model, two uses
		"ping 10.0.0.1",           // this model, one
		"dump interface status 8", // another model, however often it was run
	}
	var got []string
	for _, u := range c.Ranked("ion 1200") {
		got = append(got, u.Command)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Ranked(ion 1200) = %v, want %v", got, want)
	}

	// Ranking does not reorder what is stored, so the file does not churn on
	// a read.
	if c.History[0].Command != "dump interface status 8" {
		t.Errorf("Ranked rewrote the history order: %+v", c.History)
	}
}

// A history that grows for the life of the install would be a file nobody can
// read and a palette nobody can use.
func TestHistoryIsBounded(t *testing.T) {
	var c CommandStore
	for i := 0; i < HistoryMax+10; i++ {
		c.Record("ping 10.0.0."+strconv.Itoa(i), "ion 1200", at(i))
	}
	if len(c.History) > HistoryMax {
		t.Fatalf("history = %d entries, want at most %d", len(c.History), HistoryMax)
	}
	// The oldest went, not the newest: an entry recorded a moment ago is the
	// one most likely to be wanted again.
	if _, ok := find(c.History, "ping 10.0.0.0", "ion 1200"); ok {
		t.Error("the oldest entry survived the trim")
	}
	if _, ok := find(c.History, "ping 10.0.0."+strconv.Itoa(HistoryMax+9), "ion 1200"); !ok {
		t.Error("the newest entry was trimmed")
	}
}

func TestForgetDropsOneEntry(t *testing.T) {
	var c CommandStore
	c.Record("dump device status", "ion 1200", at(0))
	c.Record("dump device status", "ion 3200", at(1))

	if !c.Forget("dump device status", "ion 1200") {
		t.Fatal("Forget() = false, want the entry removed")
	}
	if _, ok := find(c.History, "dump device status", "ion 1200"); ok {
		t.Error("the entry survived")
	}
	// The other model's entry is a different command and stays.
	if _, ok := find(c.History, "dump device status", "ion 3200"); !ok {
		t.Error("forgetting one model's entry took the other's")
	}
	if c.Forget("never typed", "ion 1200") {
		t.Error("Forget() = true for something that was never recorded")
	}
}

func TestFavouriteSlots(t *testing.T) {
	var c CommandStore
	if err := c.SetFavourite(1, "dump device status", "ion 1200"); err != nil {
		t.Fatalf("SetFavourite() error = %v", err)
	}
	// Setting an occupied slot replaces it rather than stacking.
	if err := c.SetFavourite(1, "dump vpn count", "ion 3200"); err != nil {
		t.Fatalf("SetFavourite() error = %v", err)
	}
	if err := c.SetFavourite(9, "uptime", ""); err != nil {
		t.Fatalf("SetFavourite() error = %v", err)
	}

	f, ok := c.Favourite(1)
	if !ok || f.Command != "dump vpn count" || f.Model != "ion 3200" {
		t.Errorf("slot 1 = %+v, want the command that replaced it", f)
	}
	if list := c.FavouriteList(); len(list) != 2 || list[0].Slot != 1 || list[1].Slot != 9 {
		t.Errorf("FavouriteList() = %+v, want slot order", list)
	}
	if _, ok := c.Favourite(4); ok {
		t.Error("an empty slot answered")
	}
	if !c.ClearFavourite(1) {
		t.Error("ClearFavourite() = false, want the slot emptied")
	}
	if _, ok := c.Favourite(1); ok {
		t.Error("the cleared slot still answers")
	}

	for _, tc := range []struct {
		name string
		slot int
		cmd  string
		want string
	}{
		{"slot zero", 0, "uptime", "outside 1-9"},
		{"past the last slot", 10, "uptime", "outside 1-9"},
		{"nothing to save", 2, "   ", "nothing to save"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.SetFavourite(tc.slot, tc.cmd, "")
			if err == nil {
				t.Fatalf("SetFavourite(%d, %q) error = nil, want %q", tc.slot, tc.cmd, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestMacrosAreKeptByName(t *testing.T) {
	var c CommandStore
	m := Macro{Name: "wan check", Commands: []string{"dump interface status all"}, Model: "ion 1200"}
	if err := c.SetMacro(m); err != nil {
		t.Fatalf("SetMacro() error = %v", err)
	}
	// Same name, different case: one macro, replaced.
	m.Name = "WAN Check"
	m.Commands = append(m.Commands, "dump vpn count")
	if err := c.SetMacro(m); err != nil {
		t.Fatalf("SetMacro() error = %v", err)
	}
	if len(c.Macros) != 1 {
		t.Fatalf("macros = %+v, want one", c.Macros)
	}
	got, ok := c.Macro("wan check")
	if !ok || len(got.Commands) != 2 {
		t.Errorf("Macro() = %+v, %v, want the replacement", got, ok)
	}

	for _, tc := range []struct {
		name  string
		macro Macro
		want  string
	}{
		{"no name", Macro{Commands: []string{"uptime"}}, "needs a name"},
		{"no commands", Macro{Name: "empty"}, "no commands"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.SetMacro(tc.macro); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("SetMacro(%+v) error = %v, want %q", tc.macro, err, tc.want)
			}
		})
	}

	if !c.RemoveMacro("WAN CHECK") {
		t.Error("RemoveMacro() = false, want it gone")
	}
	if _, ok := c.Macro("wan check"); ok {
		t.Error("the macro survived")
	}
}

// Keyed by ID rather than a field per warning, because the cross-model one is
// the first of several.
func TestAcknowledgedWarningsAreKeptByID(t *testing.T) {
	var c CommandStore
	if c.Acked("favourite/model") {
		t.Error("an unanswered warning reads as acknowledged")
	}
	c.Ack("favourite/model")
	c.Ack("favourite/model")
	if !c.Acked("favourite/model") {
		t.Error("the warning was not recorded")
	}
	if len(c.Acknowledged) != 1 {
		t.Errorf("acknowledged = %v, want one entry", c.Acknowledged)
	}
	if c.Acked("macro/writes") {
		t.Error("acknowledging one warning answered another")
	}
}

func TestCommandsRoundTripThroughTheStore(t *testing.T) {
	root := t.TempDir()
	st := OpenAt(root, NewMemorySecrets())

	// A first run has no file, which is not an error.
	c, err := st.Commands()
	if err != nil {
		t.Fatalf("Commands() on a fresh root error = %v", err)
	}
	if len(c.History) != 0 {
		t.Errorf("history = %+v, want empty", c.History)
	}

	c.Record("dump device status", "ion 1200", at(0))
	if err := c.SetFavourite(3, "dump vpn count", "ion 1200"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMacro(Macro{Name: "checks", Commands: []string{"uptime"}}); err != nil {
		t.Fatal(err)
	}
	c.Ack("favourite/model")
	if err := st.SaveCommands(c); err != nil {
		t.Fatalf("SaveCommands() error = %v", err)
	}

	back, err := st.Commands()
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if len(back.History) != 1 || back.History[0].Uses != 1 {
		t.Errorf("history = %+v, want the recorded use", back.History)
	}
	if f, ok := back.Favourite(3); !ok || f.Command != "dump vpn count" {
		t.Errorf("slot 3 = %+v, %v, want what was saved", f, ok)
	}
	if _, ok := back.Macro("checks"); !ok {
		t.Error("the macro did not survive the round trip")
	}
	if !back.Acked("favourite/model") {
		t.Error("the acknowledgement did not survive the round trip")
	}

	// A corrupt file costs the history, not the session someone is opening.
	if err := os.WriteFile(st.commandsPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken, err := st.Commands()
	if err == nil {
		t.Error("Commands() error = nil on a corrupt file")
	}
	if broken == nil {
		t.Fatal("Commands() = nil on a corrupt file, want an empty store")
	}
	if err := broken.SetFavourite(1, "uptime", ""); err != nil {
		t.Errorf("the store handed back after a parse failure is unusable: %v", err)
	}
}

func find(hist []CommandUse, command, model string) (CommandUse, bool) {
	for _, u := range hist {
		if u.Command == command && u.Model == model {
			return u, true
		}
	}
	return CommandUse{}, false
}
