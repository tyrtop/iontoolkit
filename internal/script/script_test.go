package script

import (
	"context"
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
  local st = ctx.get("/sdwan/v2.0/api/elements/" .. ctx.state.element_id .. "/status")
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
		Get: func(string, map[string]string) (any, error) { return body, nil }}

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

func TestAVerbIontkDidNotHandOverIsNotThere(t *testing.T) {
	s := set(t, map[string]string{"write.lua": `
return function(ctx)
  ctx.post("/sdwan/v2.0/api/elements/1/operations", {operation = "reboot"})
end
`})
	_, err := s.Run(context.Background(), Run{Name: "write"})
	if err == nil {
		t.Fatal("a script posted to the portal through a verb nobody handed it")
	}
	for _, want := range []string{"write", ":3:", "non-function"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
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
	if !strings.Contains(err.Error(), "runaway") || !strings.Contains(err.Error(), "stopped") {
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
