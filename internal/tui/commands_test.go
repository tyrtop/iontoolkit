package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

// A model whose elements carry hardware models, since that is what history is
// keyed on.
func historyModel(t *testing.T) tuiModel {
	t.Helper()
	m := pickModel(t)
	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", Model: "ion 1200", Connected: true},
		{ID: "2", Name: "S0100-RTR-02", Model: "ion 3200", Connected: true},
	})
	return m
}

// What a shell sends is counted as it is sent, which is what fills /fc
// without anyone having to save anything.
func TestEnterInAShellCountsWhatWasTyped(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions.CloseAll)

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	m = typeString(t, m, "dump device status")
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)

	got := m.cmds.Ranked("ion 1200")
	if len(got) != 1 {
		t.Fatalf("history = %+v, want the line that was sent", got)
	}
	if got[0].Command != "dump device status" {
		t.Errorf("command = %q, want what was typed", got[0].Command)
	}
	// The model it ran on, not just the command: the same text means
	// different things on different hardware.
	if got[0].Model != "ion 1200" {
		t.Errorf("model = %q, want the element's", got[0].Model)
	}

	// And the keypress hands back the save, so the next run has it.
	if cmd == nil {
		t.Fatal("enter queued no save")
	}
	runSave(t, cmd)
	saved, err := m.store.Commands()
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if len(saved.History) != 1 || saved.History[0].Uses != 1 {
		t.Errorf("saved history = %+v, want the one use", saved.History)
	}
}

// Enter on an empty line is the device's own prompt being nudged, not a
// command.
func TestEnterOnAnEmptyLineCountsNothing(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions.CloseAll)

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.(tuiModel).cmds.History; len(got) != 0 {
		t.Errorf("history = %+v, want nothing recorded", got)
	}
	if cmd != nil {
		t.Error("an empty line queued a save")
	}
}

// A run of ten 1200s doing the same thing is one fact about the 1200, not
// ten, so a batch counts per model rather than per element.
func TestABatchRunCountsOncePerModel(t *testing.T) {
	m := historyModel(t)
	rows := []elementRow{
		{Element: api.Element{ID: "1", Model: "ion 1200"}},
		{Element: api.Element{ID: "3", Model: "ion 1200"}},
		{Element: api.Element{ID: "2", Model: "ion 3200"}},
	}
	cmd := m.recordRun([]string{"uptime", "dump device status"}, rows)
	if cmd == nil {
		t.Fatal("recordRun queued no save")
	}

	if len(m.cmds.History) != 4 {
		t.Fatalf("history = %+v, want two commands on each of two models", m.cmds.History)
	}
	for _, model := range []string{"ion 1200", "ion 3200"} {
		for _, want := range []string{"uptime", "dump device status"} {
			found := false
			for _, u := range m.cmds.History {
				if u.Command == want && u.Model == model {
					found = true
					if u.Uses != 1 {
						t.Errorf("%q on %s has %d uses, want 1 for the whole run", want, model, u.Uses)
					}
				}
			}
			if !found {
				t.Errorf("%q on %s was not counted", want, model)
			}
		}
	}
}

// /fc is the history: this model's commands first, most-used first inside
// that, and a command from another model named as such rather than hidden.
func TestFrequentCommandsAreRankedForThisModel(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions.CloseAll)

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	m.cmds = &config.CommandStore{}
	m.cmds.Record("dump device status", "ion 1200", now)
	m.cmds.Record("dump device status", "ion 1200", now)
	m.cmds.Record("ping 10.0.0.1", "ion 1200", now)
	for i := 0; i < 5; i++ {
		m.cmds.Record("dump interface status 8", "ion 3200", now)
	}

	next, _ := m.openSlash()
	m = typeString(t, next.(tuiModel), "fc")
	next, _ = m.activateSlash()
	m = next.(tuiModel)
	if m.slash != slashFreq {
		t.Fatalf("mode = %d, want the frequent list", m.slash)
	}

	if len(m.slashItems) != 3 {
		t.Fatalf("items = %v, want every entry", m.slashItems)
	}
	if !strings.HasPrefix(m.slashItems[0], "dump device status") {
		t.Errorf("first row = %q, want this model's most-used", m.slashItems[0])
	}
	if !strings.Contains(m.slashItems[0], "×2") {
		t.Errorf("first row = %q, want the count on it", m.slashItems[0])
	}
	// No tag on a command captured on the model in front of the user: it
	// would be on every row and say nothing.
	if strings.Contains(m.slashItems[0], "ion 1200") {
		t.Errorf("first row = %q, want no model tag for this model", m.slashItems[0])
	}
	// Run five times, but on other hardware, so it comes last and says so.
	last := m.slashItems[2]
	if !strings.HasPrefix(last, "dump interface status 8") || !strings.Contains(last, "ion 3200") {
		t.Errorf("last row = %q, want the other model's command, named", last)
	}

	// The query decides which rows show; the ranking still decides the order.
	m.slashInput.SetValue("dump")
	m = m.refilterSlash()
	if len(m.slashItems) != 2 || !strings.HasPrefix(m.slashItems[0], "dump device status") {
		t.Errorf("filtered items = %v, want the two dumps, this model's first", m.slashItems)
	}

	// Enter inserts without sending, like the reference palette.
	next, _ = m.activateSlash()
	m = next.(tuiModel)
	if m.slash != slashOff {
		t.Errorf("slash mode = %d, want closed", m.slash)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(conn.sent(), "dump device status") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the command never reached the session; sent=%q", conn.sent())
}

// A history nobody can prune fills with typos and stops being a shortlist.
func TestCtrlDForgetsAHistoryEntry(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions.CloseAll)

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	m.cmds = &config.CommandStore{}
	m.cmds.Record("dmup device status", "ion 1200", time.Now())
	m.cmds.Record("dump device status", "ion 1200", time.Now())
	if err := m.store.SaveCommands(m.cmds); err != nil {
		t.Fatal(err)
	}

	m = m.enterVerb("fc", "dmup")
	if len(m.slashItems) != 1 {
		t.Fatalf("items = %v, want the typo alone", m.slashItems)
	}

	next, cmd := m.onSlashKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}), "ctrl+d")
	m = next.(tuiModel)
	if len(m.cmds.History) != 1 || m.cmds.History[0].Command != "dump device status" {
		t.Errorf("history = %+v, want only the typo gone", m.cmds.History)
	}
	if !strings.Contains(m.status, "forgot") {
		t.Errorf("status = %q, want it to say what went", m.status)
	}
	// The list it is showing goes with it, or the next enter inserts a
	// command that no longer exists.
	if len(m.slashItems) != 0 {
		t.Errorf("items = %v, want the row gone too", m.slashItems)
	}

	if cmd == nil {
		t.Fatal("ctrl+d queued no save")
	}
	runSave(t, cmd)
	saved, err := m.store.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 {
		t.Errorf("saved history = %+v, want the deletion written through", saved.History)
	}
}

// The verbs are resolved by prefix before anything fuzzy, so one letter still
// picks one: "c" is a subsequence of both "commands" and "fc".
func TestOneLetterStillPicksAVerb(t *testing.T) {
	tests := map[string]string{
		"c": "commands",
		"f": "fc",
		"l": "login",
		"h": "help",
		"d": "debug",
	}
	for typed, want := range tests {
		t.Run(typed, func(t *testing.T) {
			got, ok := resolveVerb(typed)
			if !ok || got != want {
				t.Errorf("resolveVerb(%q) = %q, %v, want %q", typed, got, ok, want)
			}
		})
	}
}

func runSave(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if saved, ok := c().(commandsSavedMsg); ok && saved.err != "" {
				t.Fatalf("save failed: %s", saved.err)
			}
		}
		return
	}
	if saved, ok := msg.(commandsSavedMsg); ok && saved.err != "" {
		t.Fatalf("save failed: %s", saved.err)
	}
}
