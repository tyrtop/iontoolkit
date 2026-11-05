package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/runtime"
)

func TestAltYOnAFinishedWorkflowSavesTheRunAsJSON(t *testing.T) {
	m, eid, site := workflowTUI(t)
	m = openDescribe(t, m, eid, site)
	m, _ = typeKey(m, "enter")
	m = finishWorkflow(t, m)
	if page := plain(m.viewWorkflows()); !strings.Contains(page, exportKeyHint+" run as json") {
		t.Errorf("the finished page does not offer the export:\n%s", page)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Mod: tea.ModAlt}))
	m = next.(tuiModel)
	where, ok := strings.CutPrefix(m.status, "saved the run to ")
	if !ok {
		t.Fatalf("alt+y said %q, want where the run went", m.status)
	}
	b, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	var runs []runtime.Run
	if err := json.Unmarshal(b, &runs); err != nil || len(runs) != 1 || runs[0].Workflow != "describe" || runs[0].Inputs["element"] != eid {
		t.Errorf("the export holds %s, err %v", b, err)
	}
}
