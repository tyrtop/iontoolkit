package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/script"
)

func actionsRuntime(t *testing.T, files map[string]string) *runtime.Runtime {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	dir := filepath.Join(root, "acts")
	os.MkdirAll(dir, 0o700)
	for f, body := range files {
		os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600)
	}
	ActionsFolder = dir
	t.Cleanup(func() { ActionsFolder = "" })
	return NewRuntime(st, cfg)
}

func TestALuaActionIsACommandThatRunsIontksOwn(t *testing.T) {
	rt := actionsRuntime(t, map[string]string{"step.lua": `
return function(ctx)
  local said = ctx.run("get page_step")
  ctx.status("was " .. said[1])
  ctx.set("jump", "25")
  ctx.open("settings")
end
`})
	var said []string
	c := &runtime.Call{Ctx: context.Background(), Out: func(l string) { said = append(said, l) }}
	if err := rt.Exec(c, "action.step"); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || !strings.HasPrefix(said[0], "was page_step = ") {
		t.Errorf("said %q", said)
	}
	if v, _ := rt.Get("page_step"); v != "25" {
		t.Errorf("page_step = %q, want what the action set", v)
	}
	effects, _ := c.Result.([]script.Effect)
	if len(effects) != 3 || effects[2].Kind != script.EffectGoto {
		t.Errorf("effects for the front end = %+v", effects)
	}
}

func TestARefusedActionFileIsAWarning(t *testing.T) {
	rt := actionsRuntime(t, map[string]string{"broken.lua": "return function(ctx", "fine.lua": "return function(ctx) end"})
	if _, ok := rt.Command("action.fine"); !ok {
		t.Error("the file that loads was not registered")
	}
	if w := strings.Join(rt.Warnings(), "\n"); !strings.Contains(w, ActionsRefused) || !strings.Contains(w, "broken.lua") {
		t.Errorf("warnings = %q", w)
	}
}

func TestALuaActionCannotSaveConfigFiles(t *testing.T) {
	rt := actionsRuntime(t, nil)
	target := filepath.Join(rt.Store().Root, "writes.yaml")
	os.WriteFile(filepath.Join(ActionsFolder, "sneak.lua"), []byte(`
return function(ctx)
  ctx.run("config.save", { file = "`+target+`", text = "x" })
end
`), 0o600)
	Reload(rt)
	err := rt.Exec(&runtime.Call{Ctx: context.Background()}, "action.sneak")
	if err == nil || !strings.Contains(err.Error(), "not open to scripts") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the action wrote a config file")
	}
}
