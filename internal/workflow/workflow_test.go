package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/script"
)

func folder(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const newSite = `
title: New site
budget: 5m
inputs:
  - name: site
    prompt: Source site name
    required: true
    pattern: "S[0-9]{4}"
  - name: suffix
    default: -REPL
`

const newSiteMain = `
local names = require("names")
return function(ctx)
  local _, src = ctx.run("site.read", {site = ctx.input.site})
  local _, made = ctx.run("site.make", {name = names.replacement(src.name, ctx.input.suffix)})
  ctx.status("site " .. made.name .. " is " .. made.id)
  if not ctx.doit then
    ctx.status("dry run")
  end
end
`

const names = `
local M = {}
function M.replacement(name, suffix) return name .. suffix end
return M
`

type made struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	writes []script.Write
}

func (m made) PlannedWrites() []script.Write { return m.writes }

type fakeRuntime struct {
	calls []script.Call
}

func (f *fakeRuntime) exec(c script.Call) ([]string, any, error) {
	f.calls = append(f.calls, c)
	switch c.Name {
	case "site.read":
		if c.Args["site"] != "S0272" {
			return nil, nil, fmt.Errorf("no site %s", c.Args["site"])
		}
		return nil, map[string]any{"id": "s1", "name": "S0272"}, nil
	case "site.make":
		w := script.Write{Method: "POST", Path: "/sdwan/v4.9/api/sites", Body: map[string]any{"name": c.Args["name"]}}
		return []string{"make site " + c.Args["name"]}, made{Name: c.Args["name"], ID: "dry-run-1", writes: []script.Write{w}}, nil
	}
	return nil, nil, fmt.Errorf("no command is called %q", c.Name)
}

func loadNewSite(t *testing.T) Workflow {
	t.Helper()
	dir := folder(t, t.TempDir(), "new-site", map[string]string{
		ManifestName: newSite, "main.lua": newSiteMain, "names.lua": names,
	})
	w, err := LoadOne(dir)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestARunGathersTheWritesOfWhatItRan(t *testing.T) {
	w := loadNewSite(t)
	f := &fakeRuntime{}
	var live []string
	res, err := w.Run(context.Background(), map[string]string{"site": "S0272"},
		Host{Exec: f.exec, OnStatus: func(s string) { live = append(live, s) }}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[1].Args["name"] != "S0272-REPL" {
		t.Errorf("calls = %+v, want the required helper and the default suffix applied", f.calls)
	}
	if len(res.Writes) != 1 || res.Writes[0].Method != "POST" || res.DoIt {
		t.Errorf("result = %+v, want the write site.make planned", res)
	}
	if strings.Join(live, "|") != "site S0272-REPL is dry-run-1|dry run" || len(res.Status) != 2 {
		t.Errorf("status as it happened = %v, result = %v", live, res.Status)
	}
}

func TestDoItReachesTheScript(t *testing.T) {
	w := loadNewSite(t)
	f := &fakeRuntime{}
	var live []string
	res, err := w.Run(context.Background(), map[string]string{"site": "S0272"},
		Host{Exec: f.exec, OnStatus: func(s string) { live = append(live, s) }}, true)
	if err != nil || !res.DoIt || strings.Contains(strings.Join(live, "|"), "dry run") {
		t.Errorf("Run() = %+v, %v; said %v", res, err, live)
	}
}

func TestAFailedCommandStopsTheRun(t *testing.T) {
	w := loadNewSite(t)
	_, err := w.Run(context.Background(), map[string]string{"site": "S9999"}, Host{Exec: (&fakeRuntime{}).exec}, false)
	if err == nil || !strings.Contains(err.Error(), "no site S9999") {
		t.Errorf("Run() = %v", err)
	}
}

func TestInputsAreCheckedBeforeAnythingRuns(t *testing.T) {
	w := loadNewSite(t)
	tests := []struct {
		name string
		in   map[string]string
		want string
	}{
		{"missing", map[string]string{}, "site is required"},
		{"pattern", map[string]string{"site": "store 12"}, "does not match"},
		{"unknown", map[string]string{"site": "S0272", "serial": "x"}, "serial is not an input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.Run(context.Background(), tt.in, Host{Exec: (&fakeRuntime{}).exec}, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Run() = %v, want %q", err, tt.want)
			}
		})
	}
	got, err := w.CheckInputs(map[string]string{"site": " S0272 "})
	if err != nil || got["site"] != "S0272" || got["suffix"] != "-REPL" {
		t.Errorf("CheckInputs() = %v, %v", got, err)
	}
}

func TestLoadNamesABrokenFolderAndKeepsTheRest(t *testing.T) {
	root := t.TempDir()
	folder(t, root, "good", map[string]string{ManifestName: "title: Good\n", "main.lua": "return function(ctx) end"})
	folder(t, root, "no-main", map[string]string{ManifestName: "title: No main\n", "other.lua": "return {}"})
	folder(t, root, "bad-yaml", map[string]string{ManifestName: "title: [\n", "main.lua": "return function(ctx) end"})
	folder(t, root, "not-a-workflow", map[string]string{"notes.txt": "x"})

	got, err := Load(root)
	if len(got) != 1 || got[0].Name != "good" || got[0].Title != "Good" {
		t.Errorf("loaded %+v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "no-main: no main.lua") || !strings.Contains(err.Error(), "bad-yaml") || strings.Contains(err.Error(), "not-a-workflow") {
		t.Errorf("Load() error = %v", err)
	}
}

func TestRequireStaysInTheFolder(t *testing.T) {
	dir := folder(t, t.TempDir(), "escape", map[string]string{
		ManifestName: "title: Escape\n",
		"main.lua":   `return function(ctx) require("../../etc/passwd") end`,
	})
	w, err := LoadOne(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Run(context.Background(), nil, Host{}, false)
	if err == nil || !strings.Contains(err.Error(), "beside this script") {
		t.Errorf("Run() = %v", err)
	}
}

func TestRunsComeFromYAMLJSONOrCSV(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	want := `[{"rtr01_serial":"028201-004589-4901","site":"S5037"},{"rtr01_serial":"0272","site":"S5038"}]`
	for _, f := range []string{
		write("a.yaml", "- site: S5037\n  rtr01_serial: 028201-004589-4901\n- site: S5038\n  rtr01_serial: 0272\n"),
		write("a.json", `[{"site":"S5037","rtr01_serial":"028201-004589-4901"},{"site":"S5038","rtr01_serial":"0272"}]`),
		write("a.csv", "site, rtr01_serial\n# a comment\nS5037,028201-004589-4901\nS5038, 0272\n"),
	} {
		runs, err := ReadRuns(f)
		got, _ := json.Marshal(runs)
		if err != nil || string(got) != want {
			t.Errorf("%s: %s, %v", filepath.Base(f), got, err)
		}
	}
	for name, body := range map[string]string{
		"nested.yaml": "- site: {a: 1}\n",
		"empty.yaml":  "[]\n",
		"short.csv":   "site\n",
		"ragged.csv":  "site,suffix\nS1\n",
		"map.yaml":    "site: S1\n",
	} {
		if _, err := ReadRuns(write(name, body)); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if d := Describe(map[string]string{"site": "S1", "a": "x"}); d != "a=x site=S1" {
		t.Errorf("Describe() = %q", d)
	}
}

func TestACSVExcelSavedWithAByteOrderMarkReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.csv")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbfsite,serial\nS1,A1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadRuns(path)
	if err != nil || len(runs) != 1 || runs[0]["site"] != "S1" {
		t.Errorf("runs %v, err %v", runs, err)
	}
}
