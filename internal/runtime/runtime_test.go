package runtime

import (
	"strings"
	"sync"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/search"
)

func fleet() ([]Element, search.SiteMap) {
	el := func(id, name, site, model string) Element {
		return Element{Element: api.Element{ID: id, Name: name, SiteID: site, Model: model, Connected: true}, TSG: "t1", Tenant: "Tractor Supply"}
	}
	return []Element{
			el("1", "S0224-RTR-01", "s224", "ion 1200-s"),
			el("2", "S0224-RTR-02", "s224", "ion 1200-s"),
			el("3", "S0272-RTR-01", "s272", "ion 2000"),
			el("4", "S0272-RTR-02", "s272", "ion 2000"),
			el("5", "S0282-RTR-01", "s282", "ion 2000"),
		}, search.SiteMap{
			"t1/s224": "S0224", "t1/s272": "S0272", "t1/s282": "S0282",
		}
}

func loaded(t *testing.T) *Runtime {
	t.Helper()
	rt := New()
	rows, sites := fleet()
	rt.MergeSites(sites)
	rt.SetElements(rows)
	return rt
}

func names(rows []Element) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return strings.Join(out, " ")
}

func run(t *testing.T, rt *Runtime, name string, rest ...string) {
	t.Helper()
	if err := rt.Run(name, nil, rest...); err != nil {
		t.Fatalf("%s %v: %v", name, rest, err)
	}
}

func TestExecChecksArgumentsBeforeRunning(t *testing.T) {
	rt := New()
	ran := 0
	rt.Register(Command{Name: "x", Args: []Arg{{Name: "site", Required: true}, {Name: "suffix", Default: "-REPL"}},
		Run: func(_ *Runtime, c *Call) error {
			ran++
			if c.Arg("suffix") != "-REPL" || c.DoIt {
				t.Errorf("call = %+v, want the default filled and doit dropped on a command that does not write", c)
			}
			return nil
		}})

	if err := rt.Run("x", nil); err == nil || !strings.Contains(err.Error(), "site is required") {
		t.Errorf("missing arg: %v", err)
	}
	if err := rt.Run("x", map[string]string{"site": "S1", "serial": "z"}); err == nil || !strings.Contains(err.Error(), "serial is not an argument") {
		t.Errorf("unknown arg: %v", err)
	}
	if err := rt.Run("nope", nil); err == nil {
		t.Error("an unknown command ran")
	}
	if ran != 0 {
		t.Fatalf("ran %d times on bad calls", ran)
	}
	if err := rt.Exec(&Call{Args: map[string]string{"site": "S1"}, DoIt: true}, "x"); err != nil || ran != 1 {
		t.Errorf("good call: %v, ran %d", err, ran)
	}
}

func TestEveryListBehaviourIsACommand(t *testing.T) {
	rt := New()
	for _, want := range []string{"list.filter", "list.cursor", "list.toggle", "list.select-matches",
		"list.show-selected", "list.clear", "list.undo-clear", "list.mode"} {
		if _, ok := rt.Command(want); !ok {
			t.Errorf("no %s", want)
		}
	}
}

func TestFilterAndCursor(t *testing.T) {
	rt := loaded(t)
	run(t, rt, "list.filter", "ion", "2000")
	if got := names(rt.List().Rows); got != "S0272-RTR-01 S0272-RTR-02 S0282-RTR-01" {
		t.Errorf("rows = %s", got)
	}
	run(t, rt, "list.cursor", "bottom")
	run(t, rt, "list.cursor", "down")
	if cur, _ := rt.Current(); cur.Name != "S0282-RTR-01" {
		t.Errorf("cursor past the end landed on %s", cur.Name)
	}
	run(t, rt, "list.filter", "s0272")
	if cur, _ := rt.Current(); cur.Name != "S0272-RTR-02" || rt.List().Cursor != 1 {
		t.Errorf("a narrower filter left the cursor on %s (%d)", cur.Name, rt.List().Cursor)
	}
	run(t, rt, "list.cursor", "-5")
	if rt.List().Cursor != 0 {
		t.Errorf("cursor = %d", rt.List().Cursor)
	}
	if err := rt.Run("list.cursor", nil, "sideways"); err == nil {
		t.Error("list.cursor took sideways")
	}
	if err := rt.Run("list.filter", map[string]string{"query": "S0224"}); err != nil || names(rt.List().Rows) != "S0224-RTR-01 S0224-RTR-02" {
		t.Errorf("filter by arg: %v %s", err, names(rt.List().Rows))
	}
}

func TestSelectionClearAndUndo(t *testing.T) {
	rt := loaded(t)
	run(t, rt, "list.toggle")
	run(t, rt, "list.cursor", "+2")
	run(t, rt, "list.toggle")
	if got := names(rt.Targets()); got != "S0224-RTR-01 S0272-RTR-01" {
		t.Errorf("targets = %s", got)
	}

	run(t, rt, "list.show-selected")
	if got := names(rt.List().Rows); got != "S0224-RTR-01 S0272-RTR-01" {
		t.Errorf("show-selected rows = %s", got)
	}
	run(t, rt, "list.toggle")
	if got := names(rt.List().Rows); got != "S0224-RTR-01 S0272-RTR-01" {
		t.Errorf("unselecting while showing the selection moved the rows under the cursor: %s", got)
	}
	run(t, rt, "list.filter")
	if got := names(rt.List().Rows); got != "S0272-RTR-01" {
		t.Errorf("the next filter kept the unselected row: %s", got)
	}

	run(t, rt, "list.clear")
	if len(rt.List().Selected) != 0 || rt.List().ShowSelected || !strings.HasPrefix(rt.Status(), "cleared 1 selected") {
		t.Errorf("after clear: %+v, status %q", rt.List(), rt.Status())
	}
	if cur, _ := rt.Current(); names(rt.Targets()) != cur.Name {
		t.Error("with nothing selected, the target is not the row under the cursor")
	}
	run(t, rt, "list.undo-clear")
	if !rt.List().ShowSelected || names(rt.List().Rows) != "S0272-RTR-01" || rt.Status() != "restored 1 selected" {
		t.Errorf("undo: %+v, status %q", rt.List(), rt.Status())
	}
	run(t, rt, "list.undo-clear")
	if rt.Status() != "nothing to undo" {
		t.Errorf("second undo said %q", rt.Status())
	}
}

func TestSelectMatchesTogglesThemAll(t *testing.T) {
	rt := loaded(t)
	run(t, rt, "list.filter", "S0272")
	run(t, rt, "list.select-matches")
	if len(rt.List().Selected) != 2 {
		t.Errorf("selected %v", rt.List().Selected)
	}
	run(t, rt, "list.select-matches")
	if len(rt.List().Selected) != 0 {
		t.Errorf("a second select-matches left %v", rt.List().Selected)
	}
}

func TestNewElementsKeepTheSelectionThatStillExists(t *testing.T) {
	rt := loaded(t)
	run(t, rt, "list.select-matches")
	rows, _ := fleet()
	rt.SetElements(rows[:2])
	if len(rt.List().Selected) != 2 || rt.List().Total != 2 {
		t.Errorf("after a smaller fleet: %+v", rt.List())
	}
}

func TestModeCyclesOrIsNamed(t *testing.T) {
	rt := loaded(t)
	if rt.List().Mode != ModeShell {
		t.Errorf("starts in %q", rt.List().Mode)
	}
	run(t, rt, "list.mode")
	run(t, rt, "list.mode")
	if rt.List().Mode != ModeData {
		t.Errorf("two cycles = %q", rt.List().Mode)
	}
	run(t, rt, "list.mode")
	if rt.List().Mode != ModeShell {
		t.Errorf("wrapped to %q", rt.List().Mode)
	}
	if err := rt.Run("list.mode", map[string]string{"mode": "batch"}); err != nil || rt.List().Mode != ModeBatch {
		t.Errorf("named: %v %q", err, rt.List().Mode)
	}
	if err := rt.Run("list.mode", nil, "macro"); err == nil {
		t.Error("list.mode took macro")
	}
}

func TestSubscribersHearChanges(t *testing.T) {
	rt := loaded(t)
	ev, stop := rt.Subscribe()
	run(t, rt, "list.clear")
	got := <-ev
	if got.Area != AreaList || got.Status != "nothing selected" {
		t.Errorf("event = %+v", got)
	}
	stop()
	run(t, rt, "list.toggle")
	if _, open := <-ev; open {
		t.Error("an event arrived after stop")
	}
}

func TestReadingWhileCommandsRun(t *testing.T) {
	rt := loaded(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = rt.Run("list.cursor", nil, "down")
				_ = rt.Run("list.toggle", nil)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = rt.List()
				_, _ = rt.Current()
				_ = rt.Targets()
			}
		}()
	}
	wg.Wait()
}

func TestRowsAreOrderedAndSiteNamesMatch(t *testing.T) {
	rt := New()
	rows, _ := fleet()
	rows[0], rows[4] = rows[4], rows[0]
	rt.SetElements(rows)
	if got := names(rt.Elements()); got != "S0224-RTR-01 S0224-RTR-02 S0272-RTR-01 S0272-RTR-02 S0282-RTR-01" {
		t.Errorf("order = %s", got)
	}
	run(t, rt, "list.filter", "nashville")
	if len(rt.List().Rows) != 0 {
		t.Error("a site name matched before any were known")
	}
	rt.MergeSites(map[string]string{"t1/s282": "Nashville Hub"})
	if names(rt.List().Rows) != "S0282-RTR-01" {
		t.Errorf("after the names arrived: %s", names(rt.List().Rows))
	}
	rt.MergeSites(nil)
	if rt.Sites() != nil {
		t.Error("nil did not forget the names")
	}
}

func TestArmingAMacroAndCyclingAway(t *testing.T) {
	rt := loaded(t)
	rt.ArmMacro("dump-status")
	if rt.Mode() != ModeMacro || rt.Armed() != "dump-status" {
		t.Fatalf("armed: %q %q", rt.Mode(), rt.Armed())
	}
	run(t, rt, "list.mode")
	if rt.Mode() != ModeShell || rt.Armed() != "" {
		t.Errorf("cycling from an armed macro = %q %q, want shell and disarmed", rt.Mode(), rt.Armed())
	}
	rt.ArmMacro("x")
	rt.SetMode(ModeBatch)
	if rt.Armed() != "" {
		t.Error("SetMode left the macro armed")
	}
}
