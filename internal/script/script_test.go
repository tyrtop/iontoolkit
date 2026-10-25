package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func set(t *testing.T, files map[string]string) *Set {
	t.Helper()
	dir := filepath.Join(t.TempDir(), DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return s
}

func TestAScriptIsItsFileAndAsksForWhatItWants(t *testing.T) {
	s := set(t, map[string]string{"dark.lua": `
return function(ctx)
  ctx.set("theme", "mono")
  ctx.status("dark it is, for " .. ctx.state.tenant)
end
`})
	if !s.Has("dark") || s.Source("dark") == "" {
		t.Fatalf("Names() = %v, want the file's own name", s.Names())
	}

	got, err := s.Run(context.Background(), Run{
		Name:  "dark",
		State: map[string]any{"tenant": "Acme Co"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []Effect{
		{Kind: EffectSet, Field: "theme", Value: "mono"},
		{Kind: EffectStatus, Value: "dark it is, for Acme Co"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("effects = %v, want %v", got, want)
	}
}

func TestAScriptBranchesOnWhatAVerbReturned(t *testing.T) {
	s := set(t, map[string]string{"ha.lua": `
return function(ctx)
  local _, st = ctx.run("ha.status", { id = ctx.state.element_id })
  local peer = ctx.pick(st.cluster_members, function(m) return m.element_id ~= ctx.state.element_id end)
  if not peer then
    ctx.fail("no peer in the cluster")
  end
  if peer.status == "unknown" then
    ctx.fail("the controller cannot see " .. peer.element_id)
  end
  ctx.status("peer " .. peer.element_id .. " is " .. peer.status)
end
`})

	body := map[string]any{"cluster_members": []any{
		map[string]any{"element_id": "e1", "status": "active"},
		map[string]any{"element_id": "e2", "status": "unknown"},
	}}
	run := Run{Name: "ha", State: map[string]any{"element_id": "e1"},
		Exec: func(Call) ([]string, any, error) { return nil, body, nil }}

	_, err := s.Run(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "cannot see e2") {
		t.Fatalf("error = %v, want the refusal the script wrote", err)
	}

	body["cluster_members"] = []any{
		map[string]any{"element_id": "e1", "status": "active"},
		map[string]any{"element_id": "e2", "status": "backup"},
	}
	got, err := s.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got) != 1 || got[0].Value != "peer e2 is backup" {
		t.Errorf("effects = %v, want the status the script wrote", got)
	}
}

func TestAScriptCannotReachTheControllerItself(t *testing.T) {
	for _, verb := range []string{"get", "try", "put", "post", "patch", "delete"} {
		s := set(t, map[string]string{"write.lua": `
return function(ctx)
  ctx.` + verb + `("/sdwan/v2.0/api/elements/1/operations", {operation = "reboot"})
end
`})
		exec := func(Call) ([]string, any, error) { return nil, nil, nil }
		_, err := s.Run(context.Background(), Run{Name: "write", Exec: exec})
		if err == nil || !strings.Contains(err.Error(), "non-function") {
			t.Errorf("ctx.%s reached past iontk: %v", verb, err)
		}
	}
}

func TestAScriptReachesNothingButItsVerbs(t *testing.T) {
	for _, src := range []string{
		`return function(ctx) local f = io.open("/etc/passwd") end`,
		`return function(ctx) os.execute("rm -rf /") end`,
		`return function(ctx) require("os") end`,
		`return function(ctx) local x = debug.getinfo(1) end`,
		`return function(ctx) print("on the frame") end`,
		`return function(ctx) dofile("/etc/passwd") end`,
	} {
		s := set(t, map[string]string{"bad.lua": src})
		if _, err := s.Run(context.Background(), Run{Name: "bad"}); err == nil {
			t.Errorf("%q ran", src)
		}
	}
}

func TestALoopCannotOutliveItsBudget(t *testing.T) {
	s := set(t, map[string]string{"spin.lua": `
return function(ctx)
  local wait = ctx.budget("10s")
  while wait:tick("50ms") do end
  ctx.status("waited " .. wait:spent())
end
`})
	start := time.Now()
	got, err := s.Run(context.Background(), Run{Name: "spin", Budget: 300 * time.Millisecond})
	if err == nil {
		if len(got) != 1 || !strings.Contains(got[0].Value, "waited") {
			t.Errorf("effects = %v, want the line after the loop", got)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the loop ran for %s, want it stopped at its budget", d)
	}
}

func TestAWhileTrueIsStoppedAndNamed(t *testing.T) {
	s := set(t, map[string]string{"runaway.lua": `
return function(ctx)
  local n = 0
  while true do n = n + 1 end
end
`})
	start := time.Now()
	_, err := s.Run(context.Background(), Run{Name: "runaway", Budget: 200 * time.Millisecond})
	if err == nil {
		t.Fatal("a runaway loop ran to the end")
	}
	if !strings.Contains(err.Error(), "runaway.lua ran past its") || !strings.Contains(err.Error(), "budget") {
		t.Errorf("error = %v, want the script named and the stop said", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("it took %s to stop", d)
	}
}

func TestAFileThatIsNotAScriptIsNamedAtLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "broken.lua"), []byte("return function(ctx"), 0o600)
	os.WriteFile(filepath.Join(dir, "notafunction.lua"), []byte("return 42"), 0o600)
	os.WriteFile(filepath.Join(dir, "fine.lua"), []byte("return function(ctx) end"), 0o600)

	s, err := Load(dir)
	if err == nil {
		t.Fatal("Load() took both broken files")
	}
	for _, want := range []string{"broken.lua", "notafunction.lua", "returning a function"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
	if !s.Has("fine") || s.Has("broken") {
		t.Errorf("Names() = %v, want the good one alone", s.Names())
	}
}

func TestAScriptSeesATimeRatherThanAnInteger(t *testing.T) {
	s := set(t, map[string]string{"when.lua": `
return function(ctx)
  ctx.status("rebooted " .. utc(ctx.state.last_rebooted_time))
end
`})
	_, err := s.Run(context.Background(), Run{Name: "when",
		State: map[string]any{"last_rebooted_time": float64(1760000000)}})
	if err == nil {
		t.Fatal("utc is a helper on ctx, not a global; the script should have failed")
	}

	s = set(t, map[string]string{"when.lua": `
return function(ctx)
  ctx.status("rebooted " .. ctx.utc(ctx.state.last_rebooted_time))
end
`})
	got, err := s.Run(context.Background(), Run{Name: "when",
		State: map[string]any{"last_rebooted_time": float64(1760000000)}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Value, "2025-") {
		t.Errorf("status = %v, want the integer read as a time", got)
	}
}

func TestRunHandsATableAsArgumentsAndGetsTheResultBack(t *testing.T) {
	s := set(t, map[string]string{"retag.lua": `
return function(ctx)
  local _, got = ctx.run("site.push", {
    model = {description = "uplink", admin_up = true, mtu = 1500, tags = {"core", "wan"}, ipv4_config = {type = "dhcp"}},
    note = "first",
  })
  ctx.status("saved " .. got.description)
end
`})
	var calls []Call
	effects, err := s.Run(context.Background(), Run{Name: "retag",
		Exec: func(c Call) ([]string, any, error) {
			calls = append(calls, c)
			return nil, map[string]any{"description": "uplink"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "site.push" || calls[0].Args["note"] != "first" {
		t.Fatalf("calls = %+v", calls)
	}
	var model map[string]any
	if err := json.Unmarshal([]byte(calls[0].Args["model"]), &model); err != nil {
		t.Fatalf("model = %q: %v", calls[0].Args["model"], err)
	}
	tags, _ := model["tags"].([]any)
	cfg, _ := model["ipv4_config"].(map[string]any)
	if model["description"] != "uplink" || model["admin_up"] != true || model["mtu"] != float64(1500) ||
		len(tags) != 2 || tags[0] != "core" || cfg["type"] != "dhcp" {
		t.Errorf("model = %#v", model)
	}
	if len(effects) != 1 || effects[0].Value != "saved uplink" {
		t.Errorf("effects = %+v", effects)
	}
}

func TestARefusedCommandStopsTheScript(t *testing.T) {
	s := set(t, map[string]string{"off.lua": `
return function(ctx)
  ctx.run("site.push", {model = {a = 1}})
  ctx.status("should not get here")
end
`})
	effects, err := s.Run(context.Background(), Run{Name: "off",
		Exec: func(Call) ([]string, any, error) { return nil, nil, fmt.Errorf("the portal said no") }})
	if err == nil || !strings.Contains(err.Error(), "the portal said no") || len(effects) != 0 {
		t.Errorf("Run() = %v, %v", effects, err)
	}
}

func TestRunHandsALineToIontkAndGetsWhatItSaid(t *testing.T) {
	set := set(t, map[string]string{"ran.lua": `
return function(ctx)
  local said = ctx.run("list.filter query=3200")
  ctx.status(#said .. " lines: " .. said[1])
end
`})
	var lines []string
	effects, err := set.Run(context.Background(), Run{Name: "ran", Exec: func(c Call) ([]string, any, error) {
		lines = append(lines, c.Line)
		return []string{"1 match"}, nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "list.filter query=3200" || len(effects) != 1 || effects[0].Value != "1 lines: 1 match" {
		t.Errorf("lines %q effects %+v", lines, effects)
	}

	_, err = set.Run(context.Background(), Run{Name: "ran"})
	if err == nil || !strings.Contains(err.Error(), "ran.lua:3") {
		t.Errorf("with no Exec handed over, ctx.run should not be there: %v", err)
	}
}

func TestARecordSentBackKeepsItsNullsAndEmptyObjects(t *testing.T) {
	s := set(t, map[string]string{"rt.lua": `
return function(ctx)
  local _, rec = ctx.run("site.pull", {site = "x"})
  if rec.parent ~= nil then ctx.fail("a null reads as nil") end
  rec.description = "edited"
  rec.mtu = ctx.null
  rec.extra = ctx.object()
  ctx.run("site.push", {model = rec})
end
`})
	var sent string
	_, err := s.Run(context.Background(), Run{Name: "rt",
		Exec: func(c Call) ([]string, any, error) {
			if c.Name == "site.pull" {
				return nil, map[string]any{"name": "1", "parent": nil, "mtu": 1500.0, "dns": map[string]any{}, "tags": []any{}}, nil
			}
			sent = c.Args["model"]
			return nil, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"description":"edited","dns":{},"extra":{},"mtu":null,"name":"1","parent":null,"tags":[]}`
	if sent != want {
		t.Errorf("sent %s\nwant %s", sent, want)
	}
}

func TestAStoppedRunIsNotCalledOutOfBudget(t *testing.T) {
	s := set(t, map[string]string{"loop.lua": `return function(ctx) while true do end end`})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.Run(ctx, Run{Name: "loop", Budget: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "stopped after") || strings.Contains(err.Error(), "budget") {
		t.Errorf("error = %v, want it said to be stopped, not out of budget", err)
	}
}

func TestExportTakesATableAsJSON(t *testing.T) {
	s := set(t, map[string]string{"x.lua": `return function(ctx) ctx.export({ a = 1, b = ctx.null, c = "R&D <lab>" }, "x") end`})
	var got string
	_, err := s.Run(context.Background(), Run{Name: "x", Export: func(text, slug, ext string) (string, error) { got = text; return "f", nil }})
	if err != nil || got != "{\n  \"a\": 1,\n  \"b\": null,\n  \"c\": \"R&D <lab>\"\n}" {
		t.Errorf("exported %q, err %v", got, err)
	}
}

func TestAWriteThatTimesOutPastTheBudgetIsReportedAsItself(t *testing.T) {
	s := set(t, map[string]string{"p.lua": `return function(ctx) ctx.run("site.push", {}) end`})
	_, err := s.Run(context.Background(), Run{Name: "p", Budget: 50 * time.Millisecond, Exec: func(Call) ([]string, any, error) {
		time.Sleep(100 * time.Millisecond)
		return nil, nil, errors.New(`Post "x": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	}})
	if err == nil || strings.Contains(err.Error(), "budget") || !strings.Contains(err.Error(), "Client.Timeout") {
		t.Errorf("error = %v, want the write's own timeout", err)
	}
}

func TestATopLevelLoopIsStoppedAtItsBudget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pkg")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "top.lua"), []byte("while true do end\nreturn function(ctx) end"), 0o600)
	s, err := LoadPackage(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Run(context.Background(), Run{Name: "top", Budget: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "top.lua ran past its") {
		t.Errorf("error = %v", err)
	}
}

func TestLoadingAScriptThatNeverEndsGivesUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "hang.lua"), []byte("while true do end"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Error("a script whose top level never ends was loaded")
	}
}

func TestAnErrorNamingAnotherScriptKeepsThisOnesName(t *testing.T) {
	s := set(t, map[string]string{"m.lua": "return function(ctx)\n  ctx.run(\"other\", {})\nend"})
	_, err := s.Run(context.Background(), Run{Name: "m", Exec: func(Call) ([]string, any, error) {
		return nil, nil, errors.New("other.lua:3: boom")
	}})
	if err == nil || !strings.HasPrefix(err.Error(), "m.lua:2:") {
		t.Errorf("error = %v", err)
	}
}

func TestLoadingGivesATopLevelItsLibrariesAndPlainMessages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "fmt.lua"), []byte("local f = string.format\nreturn function(ctx) ctx.status(f('%s', 'y')) end"), 0o600)
	os.WriteFile(filepath.Join(dir, "bad.lua"), []byte("return function(ctx)\n  local s = 'abc\nend"), 0o600)
	s, err := Load(dir)
	if !s.Has("fmt") {
		t.Errorf("a top level using string was refused: %v", err)
	}
	if got, rerr := s.Run(context.Background(), Run{Name: "fmt"}); rerr != nil || len(got) != 1 {
		t.Errorf("fmt ran: %v %v", got, rerr)
	}
	if err == nil || !strings.Contains(err.Error(), "bad.lua:3: near 'abc': unterminated string") {
		t.Errorf("syntax error = %v", err)
	}
}

func TestAnErrorKeepsItsOwnSpacing(t *testing.T) {
	s := set(t, map[string]string{"m.lua": "return function(ctx)\n  error('a:  b')\nend"})
	if _, err := s.Run(context.Background(), Run{Name: "m"}); err == nil || !strings.HasSuffix(err.Error(), "a:  b") {
		t.Errorf("error = %v", err)
	}
}

func TestLoadMessagesForRequireAndCompileErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "req.lua"), []byte("local l = require('lib')\nreturn function(ctx) end"), 0o600)
	os.WriteFile(filepath.Join(dir, "jump.lua"), []byte("return function(ctx)\n  goto nowhere\nend"), 0o600)
	os.WriteFile(filepath.Join(dir, "colon.lua"), []byte("return function(ctx)\n  local s = 'abc: def\nend"), 0o600)
	_, err := Load(dir)
	for _, want := range []string{`require "lib": an action has no require`, "jump.lua: near line", "colon.lua:3: near 'abc: def': unterminated string"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("load error lacks %q: %v", want, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "<string>") {
		t.Errorf("load error shows <string>: %v", err)
	}
}

func TestAPushStoppedMidwaySaysSoAndKeepsItsError(t *testing.T) {
	s := set(t, map[string]string{"p.lua": `return function(ctx) ctx.run("site.push", {}) end`})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.Run(ctx, Run{Name: "p", Exec: func(Call) ([]string, any, error) {
		cancel()
		return nil, nil, errors.New("NEW0001 to be claimed: context canceled")
	}})
	if err == nil || !strings.HasPrefix(err.Error(), "stopped after") || !strings.Contains(err.Error(), "to be claimed") {
		t.Errorf("error = %v", err)
	}
}
