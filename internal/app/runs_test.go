package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/workflow"
)

const measureWorkflow = `
title: Measure
inputs:
  - name: router
    required: true
  - name: interface
    default: "3"
`

const measureMain = `
return function(ctx)
  if ctx.input.router == "S0000-RTR-01" then ctx.fail("no router called S0000-RTR-01") end
  ctx.status("measured " .. ctx.input.router)
  return { router = ctx.input.router, mbps = 192.69 }
end
`

func measureRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg := &config.Config{Tenants: []config.Tenant{{Name: "Acme Co", TSGID: "t1", Auth: config.AuthBrowser, Added: true}}}
	dir := filepath.Join(root, "wf", "measure")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, workflow.ManifestName), []byte(measureWorkflow), 0o600)
	os.WriteFile(filepath.Join(dir, "main.lua"), []byte(measureMain), 0o600)
	WorkflowsFolder = filepath.Dir(dir)
	t.Cleanup(func() { WorkflowsFolder = "" })
	rt := NewRuntime(st, cfg)
	if w := rt.Warnings(); len(w) > 0 {
		t.Fatalf("the workflow did not load: %v", w)
	}
	return rt
}

func TestAWorkflowRunIsKeptWithWhatItReturned(t *testing.T) {
	rt := measureRuntime(t)
	if _, _, err := exec(rt, WorkflowPrefix+"measure", map[string]string{"router": "S5037-RTR-01", "tenant": "Acme Co"}, nil, false); err != nil {
		t.Fatal(err)
	}
	runs := rt.Runs()
	if len(runs) != 1 {
		t.Fatalf("kept %d runs, want 1", len(runs))
	}
	r := runs[0]
	data, _ := json.Marshal(r.Data)
	if r.Workflow != "measure" || r.Tenant != "Acme Co" || r.TSG != "t1" || r.Inputs["interface"] != "3" || r.Error != "" ||
		strings.Join(r.Status, "|") != "measured S5037-RTR-01" || string(data) != `{"mbps":192.69,"router":"S5037-RTR-01"}` {
		t.Errorf("kept %+v with data %q", r, data)
	}

	_, _, err := exec(rt, WorkflowPrefix+"measure", map[string]string{"router": "S0000-RTR-01", "tenant": "Acme Co"}, nil, false)
	runs = rt.Runs()
	if err == nil || len(runs) != 1 || !strings.Contains(runs[0].Error, "no router called S0000-RTR-01") || runs[0].Data != nil {
		t.Errorf("a failed run kept %+v, err %v; want the failure alone, with its reason", runs, err)
	}
}

func TestEveryRunFromAFileOfInputsIsKeptInFileOrder(t *testing.T) {
	rt := measureRuntime(t)
	file := filepath.Join(t.TempDir(), "routers.csv")
	os.WriteFile(file, []byte("router,tenant\nS0001-RTR-01,Acme Co\nS0000-RTR-01,Acme Co\nS0002-RTR-01,Acme Co\n"), 0o600)
	exec(rt, WorkflowPrefix+"measure", map[string]string{EachInput: file}, nil, false)

	runs := rt.Runs()
	var got []string
	for _, r := range runs {
		got = append(got, r.Inputs["router"])
	}
	if strings.Join(got, ",") != "S0001-RTR-01,S0000-RTR-01,S0002-RTR-01" {
		t.Fatalf("kept the runs as %q, want them in the file's order", got)
	}
	if runs[0].Error != "" || !strings.Contains(runs[1].Error, "no router called S0000-RTR-01") || runs[2].Data == nil {
		t.Errorf("kept %+v, want the middle run's failure beside the other two's results", runs)
	}
}
