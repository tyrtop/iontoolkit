package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/workflow"
)

type portal struct {
	mu   sync.Mutex
	sent []string
}

func (p *portal) GetJSON(_ context.Context, path string) ([]byte, error) {
	switch path {
	case "/sdwan/v4.21/api/sites/s1/elements/e1/interfaces":
		return []byte(`{"items":[{"id":"i1","name":"1","description":"old"},{"id":"i2","name":"2"}]}`), nil
	case "/sdwan/v4.9/api/sites":
		return []byte(`{"items":[{"id":"s1","name":"S0272"}]}`), nil
	}
	if path == "/sdwan/v4.9/api/sites/777" {
		return []byte(`{"id":"777","name":"S0272-REPL","admin_state":"disabled"}`), nil
	}
	if strings.Contains(path, "/sites/777/") {
		return []byte(`{"items":[]}`), nil
	}
	return nil, errors.New("404 " + path)
}

func (p *portal) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(string(body), "S9999") {
		return nil, errors.New("the portal said no")
	}
	p.sent = append(p.sent, method+" "+path)
	return []byte(`{"id":"777"}`), nil
}

func (p *portal) LookupElement(_ context.Context, eid string) (api.Element, error) {
	if eid == "e1" {
		return api.Element{ID: "e1", Name: "S0224-RTR-01", SiteID: "s1"}, nil
	}
	return api.Element{}, errors.New("404 element " + eid)
}

func registryRuntime(t *testing.T, workflows map[string]map[string]string) (*runtime.Runtime, *portal, *config.Store) {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()

	wfRoot := filepath.Join(root, "wf")
	for name, files := range workflows {
		dir := filepath.Join(wfRoot, name)
		os.MkdirAll(dir, 0o700)
		for f, body := range files {
			os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600)
		}
	}
	WorkflowsFolder = wfRoot
	t.Cleanup(func() { WorkflowsFolder, WritesFile = "", "" })

	rt := NewRuntime(st, cfg)
	p := &portal{}
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	return rt, p, st
}

const newSite = `
title: New site
inputs:
  - name: site
    prompt: Source site
    required: true
`

const newSiteMain = `
return function(ctx)
  local name = ctx.input.site .. "-REPL"
  ctx.run("site.push", {model = {site = {name = name, admin_state = "disabled"}}})
  ctx.status("planned " .. name)
end
`

func exec(rt *runtime.Runtime, name string, args map[string]string, rest []string, doit bool) ([]string, any, error) {
	var lines []string
	c := &runtime.Call{Args: args, Rest: rest, DoIt: doit, Out: func(s string) { lines = append(lines, s) }}
	err := rt.Exec(c, name)
	return lines, c.Result, err
}

func TestAWorkflowIsACommandWithItsInputsAsArguments(t *testing.T) {
	rt, p, _ := registryRuntime(t, map[string]map[string]string{
		"new-site": {workflow.ManifestName: newSite, "main.lua": newSiteMain},
		"broken":   {workflow.ManifestName: "title: Broken\n"},
	})
	cmd, ok := rt.Command("workflow.new-site")
	if !ok || cmd.Help != "New site" || len(cmd.Args) != 2 || !cmd.Args[0].Required || cmd.Args[1].Name != EachInput || !cmd.Writes {
		t.Fatalf("command = %+v", cmd)
	}
	if w := strings.Join(rt.Warnings(), ";"); !strings.Contains(w, "broken: no main.lua") {
		t.Errorf("warnings = %q", w)
	}

	lines, res, err := exec(rt, "workflow.new-site", map[string]string{"site": "S0272"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "planned S0272-REPL") || !strings.Contains(out, "1 write(s) planned") || len(p.sent) != 0 {
		t.Errorf("dry run said:\n%s\nsent %v", out, p.sent)
	}
	if r, ok := res.(workflow.Result); !ok || len(r.Writes) != 1 {
		t.Errorf("result = %#v", res)
	}

	if _, _, err := exec(rt, "workflow.new-site", nil, nil, false); err == nil || !strings.Contains(err.Error(), "site is required") {
		t.Errorf("missing input: %v", err)
	}
}

func TestWriteIsOneCommandForEveryFrontEnd(t *testing.T) {
	rt, p, st := registryRuntime(t, nil)
	args := map[string]string{"what": "interface", "elements": "e1,e9", "match": "1"}

	lines, res, err := exec(rt, "write", args, []string{"description=uplink"}, false)
	out := strings.Join(lines, "\n")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 elements could not be planned") {
		t.Errorf("dry run error = %v", err)
	}
	if !strings.Contains(out, "description    old → uplink") || !strings.Contains(out, "dry run: 1 change(s)") || len(p.sent) != 0 {
		t.Errorf("dry run said:\n%s", out)
	}
	if run, ok := res.(*WriteRun); !ok || len(run.Plans) != 2 {
		t.Errorf("result = %#v", res)
	}

	args["elements"] = "e1"
	if _, _, err := exec(rt, "write", args, []string{"description=uplink"}, true); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Errorf("doit on an unverified spec = %v", err)
	}

	verified := strings.Replace(write.BuiltinYAML(), "    method: PUT\n", "    method: PUT\n    verified: true\n", 1)
	WritesFile = filepath.Join(st.Root, "writes.yaml")
	os.WriteFile(WritesFile, []byte(verified), 0o600)
	lines, _, err = exec(rt, "write", args, []string{"description=uplink"}, true)
	if err != nil || len(p.sent) != 1 || p.sent[0] != "PUT /sdwan/v4.21/api/sites/s1/elements/e1/interfaces/i1" {
		t.Errorf("doit: %v, sent %v\n%s", err, p.sent, strings.Join(lines, "\n"))
	}
	if b, _ := os.ReadFile(filepath.Join(st.Root, write.LogName)); !strings.Contains(string(b), "Tractor Supply") {
		t.Errorf("writes.log = %s", b)
	}

	if _, _, err := exec(rt, "write", args, []string{"name=9"}, false); err == nil || !strings.Contains(err.Error(), "no editable field") {
		t.Errorf("bad field = %v", err)
	}
}

func TestWithNoPortalACommandSaysSo(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	rt := NewRuntime(st, cfg)
	_, _, err := exec(rt, "write", map[string]string{"what": "interface", "elements": "e1", "match": "1"}, []string{"description=x"}, false)
	if err == nil || !strings.Contains(err.Error(), "no tenant") {
		t.Errorf("write with no portal = %v", err)
	}
}

func TestAWorkflowRunsOncePerEntryInAFileOfInputs(t *testing.T) {
	rt, p, _ := registryRuntime(t, map[string]map[string]string{
		"new-site": {workflow.ManifestName: newSite + "  - name: suffix\n    default: -REPL\n", "main.lua": newSiteMain},
	})
	runs := filepath.Join(t.TempDir(), "sites.csv")
	os.WriteFile(runs, []byte("site,suffix\nS0272,\nS9999,-X\nS0272,-NEW\n"), 0o600)

	lines, _, _ := exec(rt, "workflow.new-site", map[string]string{EachInput: runs, "suffix": "-FLAG"}, nil, false)
	out := strings.Join(lines, "\n")
	for _, want := range []string{"3 run(s), up to 10 at a time", "S0272 | site=S0272", "S9999 | site=S9999 suffix=-X", "S0272#2 | "} {
		if !strings.Contains(out, want) {
			t.Errorf("the runs never said %q:\n%s", want, out)
		}
	}
	if _, _, err := exec(rt, "workflow.new-site", map[string]string{EachInput: runs}, nil, true); err == nil ||
		!strings.Contains(err.Error(), "both have site S0272") || len(p.sent) != 0 {
		t.Errorf("a site twice with --doit: err %v, sent %v", err, p.sent)
	}

	os.WriteFile(runs, []byte("suffix\n-A\n-B\n"), 0o600)
	refused, _, err := exec(rt, "workflow.new-site", map[string]string{EachInput: runs, "site": "S0272"}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "both have site S0272") || len(p.sent) != 0 {
		t.Errorf("one site from the command line for two runs with --doit: err %v, sent %v", err, p.sent)
	}
	if strings.Contains(strings.Join(refused, "\n"), "run(s)") {
		t.Errorf("runs were announced before the refusal: %v", refused)
	}

	os.WriteFile(runs, []byte("- site: \"  \"\n  suffix: -A\n- suffix: -B\n"), 0o600)
	yml := runs[:len(runs)-len(".csv")] + ".yaml"
	os.Rename(runs, yml)
	if _, _, err := exec(rt, "workflow.new-site", map[string]string{EachInput: yml, "site": "S0272"}, nil, true); err == nil ||
		!strings.Contains(err.Error(), "both have site S0272") || len(p.sent) != 0 {
		t.Errorf("a blank site in the file over one on the command line: err %v, sent %v", err, p.sent)
	}

	os.WriteFile(runs, []byte("site,suffix\nS0272,\nS9999,-X\n"), 0o600)
	_, _, err = exec(rt, "workflow.new-site", map[string]string{EachInput: runs, "suffix": "-FLAG"}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 runs failed: S9999") {
		t.Errorf("err = %v", err)
	}
	if len(p.sent) != 1 {
		t.Errorf("sent %v, want the run that could", p.sent)
	}

	if _, _, err := exec(rt, "workflow.new-site", map[string]string{EachInput: filepath.Join(t.TempDir(), "none.yaml")}, nil, false); err == nil {
		t.Error("a missing file of inputs was taken")
	}
}

func TestRunsFromAFileOfInputsGoAtOnce(t *testing.T) {
	rt, _, _ := registryRuntime(t, map[string]map[string]string{
		"slow": {workflow.ManifestName: newSite, "main.lua": `
return function(ctx)
  ctx.budget("5s"):tick("300ms")
  ctx.status("done " .. ctx.input.site)
end
`},
	})
	runs := filepath.Join(t.TempDir(), "sites.yaml")
	os.WriteFile(runs, []byte("- site: A\n- site: B\n- site: C\n"), 0o600)

	start := time.Now()
	lines, _, err := exec(rt, "workflow.slow", map[string]string{EachInput: runs}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 800*time.Millisecond {
		t.Errorf("three 300ms runs took %s: they ran one after another", took)
	}
	out := strings.Join(lines, "\n")
	for _, want := range []string{"A | done A", "B | done B", "C | done C", "3 run(s) done"} {
		if !strings.Contains(out, want) {
			t.Errorf("the runs never said %q:\n%s", want, out)
		}
	}
}

func TestABlankValueInAFileOfInputsLeavesTheCommandLineOne(t *testing.T) {
	rt, _, _ := registryRuntime(t, map[string]map[string]string{
		"echo": {workflow.ManifestName: newSite + "  - name: suffix\n    default: -REPL\n",
			"main.lua": "return function(ctx) ctx.status(ctx.input.site .. ctx.input.suffix) end"},
	})
	runs := filepath.Join(t.TempDir(), "sites.yaml")
	os.WriteFile(runs, []byte("- site: S1\n  suffix: \"  \"\n"), 0o600)
	lines, _, err := exec(rt, "workflow.echo", map[string]string{EachInput: runs, "suffix": "-FLAG"}, nil, false)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "S1 | S1-FLAG") {
		t.Errorf("err %v:\n%s", err, strings.Join(lines, "\n"))
	}
}
