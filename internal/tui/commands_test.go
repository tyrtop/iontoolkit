package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

func historyModel(t *testing.T) tuiModel {
	t.Helper()
	m := pickModel(t)
	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", Model: "ion 1200", Connected: true},
		{ID: "2", Name: "S0100-RTR-02", Model: "ion 3200", Connected: true},
	})
	return m
}

func TestEnterInAShellCountsWhatWasTyped(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions().CloseAll)

	conn := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	m = typeString(t, m, "dump device status")
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(tuiModel)

	got := m.runtime().Ranked("ion 1200", "")
	if len(got) != 1 {
		t.Fatalf("history = %+v, want the line that was sent", got)
	}
	if got[0].Command != "dump device status" {
		t.Errorf("command = %q, want what was typed", got[0].Command)
	}
	if got[0].Model != "ion 1200" {
		t.Errorf("model = %q, want the element's", got[0].Model)
	}

	if cmd == nil {
		t.Fatal("enter queued no save")
	}
	runSave(t, cmd)
	saved, err := m.store().Commands()
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if len(saved.History) != 1 || saved.History[0].Uses != 1 {
		t.Errorf("saved history = %+v, want the one use", saved.History)
	}
}

func TestEnterOnAnEmptyLineCountsNothing(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions().CloseAll)

	conn := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.(tuiModel).rt.History(); len(got) != 0 {
		t.Errorf("history = %+v, want nothing recorded", got)
	}
	if cmd != nil {
		t.Error("an empty line queued a save")
	}
}

func TestFrequentCommandsAreRankedForThisModel(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions().CloseAll)

	conn := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	m.runtime().RecordUse("ion 1200", "dump device status")
	m.runtime().RecordUse("ion 1200", "dump device status")
	m.runtime().RecordUse("ion 1200", "ping 10.0.0.1")
	for i := 0; i < 5; i++ {
		m.runtime().RecordUse("ion 3200", "dump interface status 8")
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
	if strings.Contains(m.slashItems[0], "ion 1200") {
		t.Errorf("first row = %q, want no model tag for this model", m.slashItems[0])
	}
	last := m.slashItems[2]
	if !strings.HasPrefix(last, "dump interface status 8") || !strings.Contains(last, "ion 3200") {
		t.Errorf("last row = %q, want the other model's command, named", last)
	}

	m.slashInput.SetValue("dump")
	m = m.refilterSlash()
	if len(m.slashItems) != 2 || !strings.HasPrefix(m.slashItems[0], "dump device status") {
		t.Errorf("filtered items = %v, want the two dumps, this model's first", m.slashItems)
	}

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

func TestCtrlDForgetsAHistoryEntry(t *testing.T) {
	m := historyModel(t)
	m.phase = phaseSession
	t.Cleanup(m.sessions().CloseAll)

	conn := newRecConn()
	if _, err := m.sessions().Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	m.runtime().RecordUse("ion 1200", "dmup device status")
	m.runtime().RecordUse("ion 1200", "dump device status")
	if err := m.runtime().SaveCommands(); err != nil {
		t.Fatal(err)
	}

	m = m.enterVerb("fc", "dmup")
	if len(m.slashItems) != 1 {
		t.Fatalf("items = %v, want the typo alone", m.slashItems)
	}

	next, cmd := m.onSlashKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}), "ctrl+d")
	m = next.(tuiModel)
	if len(m.runtime().History()) != 1 || m.runtime().History()[0].Command != "dump device status" {
		t.Errorf("history = %+v, want only the typo gone", m.runtime().History())
	}
	if !strings.Contains(m.status, "forgot") {
		t.Errorf("status = %q, want it to say what went", m.status)
	}
	if len(m.slashItems) != 0 {
		t.Errorf("items = %v, want the row gone too", m.slashItems)
	}

	if cmd == nil {
		t.Fatal("ctrl+d queued no save")
	}
	runSave(t, cmd)
	saved, err := m.store().Commands()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 {
		t.Errorf("saved history = %+v, want the deletion written through", saved.History)
	}
}

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
	if cmd == nil {
		return
	}
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
