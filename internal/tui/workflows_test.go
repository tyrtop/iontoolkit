package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tyrtop.com/iontk/internal/app"

	"tyrtop.com/iontk/internal/sase/write"
)

const describeWorkflow = `
title: Describe port 1
description: sets interface 1's description on one element
inputs:
  - name: element
    prompt: Element ID
    required: true
  - name: site
    prompt: Site ID
    required: true
  - name: text
    default: uplink to core
`

const describeMain = `
return function(ctx)
  ctx.run('write what=interface elements=' .. ctx.input.element .. ' match=1 description="' .. ctx.input.text .. '"')
  ctx.status("port 1 → " .. ctx.input.text)
end
`

func workflowTUI(t *testing.T) (tuiModel, string, string) {
	t.Helper()
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	dir := filepath.Join(t.TempDir(), "workflows", "describe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(describeWorkflow), 0o600)
	os.WriteFile(filepath.Join(dir, "main.lua"), []byte(describeMain), 0o600)
	app.WorkflowsFolder = filepath.Dir(dir)
	app.WritesFile = filepath.Join(t.TempDir(), "writes.yaml")
	verified := strings.Replace(write.BuiltinYAML(), "    method: PUT\n", "    method: PUT\n    verified: true\n", 1)
	os.WriteFile(app.WritesFile, []byte(verified), 0o600)
	t.Cleanup(func() {
		app.WorkflowsFolder, app.WritesFile = "", ""
		_ = os.Remove(filepath.Join(m.store().Root, write.LogName))
	})
	app.ReloadCommands(m.runtime())
	if w := m.runtime().Warnings(); len(w) > 0 {
		t.Fatal(w)
	}

	el := set.Tenants[0].Elements[0]
	return m, el.ID, siteOf(t, m, el.ID)
}

func siteOf(t *testing.T, m tuiModel, eid string) string {
	t.Helper()
	c := m.tenantClient(m.tenant().TSGID)
	el, err := c.LookupElement(context.Background(), eid)
	if err != nil {
		t.Fatal(err)
	}
	return el.SiteID
}

func finishWorkflow(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	for msg := range m.wf.events {
		next, _ := m.Update(msg)
		m = next.(tuiModel)
	}
	if m.wf.step != wfDone {
		t.Fatalf("the run did not finish, step %d", m.wf.step)
	}
	return m
}

func openDescribe(t *testing.T, m tuiModel, eid, site string) tuiModel {
	t.Helper()
	m.phase = phaseMenu
	next, _ := m.openWorkflows()
	m = next.(tuiModel)
	if got := plain(m.viewWorkflows()); !strings.Contains(got, "> Describe port 1") {
		t.Fatalf("the list does not offer the workflow:\n%s", got)
	}
	m, _ = typeKey(m, "enter")
	if m.wf.step != wfForm {
		t.Fatalf("enter did not open the inputs, step %d", m.wf.step)
	}
	m.wf.inputs[0].SetValue(eid)
	m.wf.inputs[1].SetValue(site)
	return m
}

func TestWorkflowsIsOnTheMainMenu(t *testing.T) {
	m, _, _ := workflowTUI(t)
	found := false
	for _, e := range m.menuEntries() {
		found = found || (e.label == "Workflows" && e.action == actWorkflows)
	}
	if !found {
		t.Errorf("menu = %+v", m.menuEntries())
	}
}

func TestAWorkflowDryRunsFirstAndDoesItOnD(t *testing.T) {
	m, eid, site := workflowTUI(t)
	m = openDescribe(t, m, eid, site)

	m, _ = typeKey(m, "enter")
	if m.wf.step != wfRunning || m.wf.doit {
		t.Fatalf("enter did not start a dry run: step %d doit %v err %q", m.wf.step, m.wf.doit, m.wf.err)
	}
	m = finishWorkflow(t, m)
	page := plain(m.viewWorkflows())
	for _, want := range []string{"dry run: 1 change(s)", "→ uplink to core", "port 1 → uplink to core", "d do it"} {
		if !strings.Contains(page, want) {
			t.Errorf("result lacks %q:\n%s", want, page)
		}
	}
	if _, err := os.Stat(filepath.Join(m.store().Root, write.LogName)); !os.IsNotExist(err) {
		t.Error("a dry run wrote to writes.log")
	}

	m, _ = typeKey(m, "d")
	if m.wf.step != wfRunning || !m.wf.doit {
		t.Fatalf("d did not do it: %q", m.wf.err)
	}
	m = finishWorkflow(t, m)
	if m.wf.err != "" || !strings.Contains(plain(m.viewWorkflows()), "1 sent, 0 failed") {
		t.Errorf("after doing it:\n%s", plain(m.viewWorkflows()))
	}

	c := m.tenantClient(m.tenant().TSGID)
	b, err := c.GetJSON(context.Background(), "/sdwan/v4.21/api/sites/"+site+"/elements/"+eid+"/interfaces")
	if err != nil || !strings.Contains(string(b), `"description":"uplink to core"`) {
		t.Errorf("interfaces after doing it = %s, %v", b, err)
	}
}

func TestMissingInputsStopBeforeAnythingRuns(t *testing.T) {
	m, _, _ := workflowTUI(t)
	m = openDescribe(t, m, "", "")
	m, cmd := typeKey(m, "enter")
	if cmd != nil || m.wf.step != wfForm || !strings.Contains(m.wf.err, "element is required") {
		t.Errorf("step %d, err %q", m.wf.step, m.wf.err)
	}
}
