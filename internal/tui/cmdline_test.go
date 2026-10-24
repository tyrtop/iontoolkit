package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func enter(m tuiModel) tuiModel {
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	return next.(tuiModel)
}

func TestColonRunsARuntimeCommandOnTheList(t *testing.T) {
	m := historyModel(t)
	m.phase = phasePicking
	m = typeString(t, m, ":")
	if !m.cmdlineOn {
		t.Fatal(": did not open the command line")
	}
	m = enter(typeString(t, m, "list.filter query=3200"))
	if m.cmdlineOn {
		t.Error("the command line stayed open after enter")
	}
	if got := m.filter.Value(); got != "3200" {
		t.Errorf("filter box = %q, want the query the command set", got)
	}
	if n := len(m.matched()); n != 1 {
		t.Errorf("%d rows match, want the 3200 alone", n)
	}
	if plain := plain(m.View().Content); !strings.Contains(plain, "S0100-RTR-02") || strings.Contains(plain, "S0100-RTR-01") {
		t.Errorf("list not redrawn from the runtime:\n%s", plain)
	}
}

func TestTabCompletesACommandName(t *testing.T) {
	m := historyModel(t)
	m.phase = phasePicking
	m = typeString(t, m, ":tenant.li")
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = next.(tuiModel)
	if got := m.cmdline.Value(); got != "tenant.list " {
		t.Errorf("completed to %q", got)
	}
	if plain := plain(m.View().Content); !strings.Contains(plain, "the saved tenants") {
		t.Errorf("no hint for the command:\n%s", plain)
	}
}

func TestALongAnswerOpensTheCommandScreenAndEscComesBack(t *testing.T) {
	m := historyModel(t)
	m.phase = phasePicking
	m = enter(typeString(t, m, ":commands"))
	if !strings.Contains(m.status, "no command is called") {
		t.Errorf("unknown command said %q", m.status)
	}
	m = enter(typeString(t, m, ":options"))
	if m.phase != phaseWorkflows || m.wf.step != wfDone || len(m.wf.lines) < 5 {
		t.Fatalf("phase %v step %v, %d lines", m.phase, m.wf.step, len(m.wf.lines))
	}
	if plain := plain(m.View().Content); !strings.Contains(plain, "page_step") {
		t.Errorf("options not shown:\n%s", plain)
	}
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := next.(tuiModel).phase; got != phasePicking {
		t.Errorf("esc went to %v, want the list it was run over", got)
	}
}

func TestACommandWantingArgumentsOpensItsForm(t *testing.T) {
	m := historyModel(t)
	m.phase = phasePicking
	m = enter(typeString(t, m, ":fav.set slot=4"))
	if m.phase != phaseWorkflows || m.wf.step != wfForm {
		t.Fatalf("phase %v step %v, want the form", m.phase, m.wf.step)
	}
	if got := m.wf.inputs[0].Value(); got != "4" {
		t.Errorf("slot field = %q, want what was typed", got)
	}
}

func TestAKeyCanBeBoundToARuntimeCommand(t *testing.T) {
	m := historyModel(t)
	km := defaultKeymap()
	km.runs = m.runsCommand
	if err := km.readBytes([]byte("keys:\n  alt+9: \"list.filter query=3200\"\n"), scrElements, "keys.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := km.check(); err != nil {
		t.Fatal(err)
	}
	m.keys, m.phase = km, phasePicking
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '9', Mod: tea.ModAlt}))
	if got := len(next.(tuiModel).matched()); got != 1 {
		t.Errorf("alt+9 left %d rows matching", got)
	}
}

func TestAMenuEntryCanBeARuntimeCommand(t *testing.T) {
	m := historyModel(t)
	entries, err := parseMenu([]byte("kind: menu\nentries:\n  - elements\n  - {name: \"tenant.list\", label: Tenants}\n  - quit\n"), m.runsCommand)
	if err != nil {
		t.Fatal(err)
	}
	if entries[1].label != "Tenants" || entries[1].action != actCommand {
		t.Fatalf("entry = %+v", entries[1])
	}
	if _, err := parseMenu([]byte("kind: menu\nentries:\n  - nothing.here\n  - quit\n"), m.runsCommand); err == nil {
		t.Error("a menu naming no command was taken")
	}
	m.menuSpec, m.phase, m.menuCursor = entries, phaseMenu, 1
	m = enter(m)
	if !strings.HasPrefix(m.status, "> t ") {
		t.Errorf("status = %q, want the one tenant listed", m.status)
	}
}
