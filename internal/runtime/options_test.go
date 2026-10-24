package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func withConfig(t *testing.T) (*Runtime, *config.Store) {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, err := st.Config()
	if err != nil {
		t.Fatal(err)
	}
	rt := loaded(t)
	rt.UseConfig(st, cfg)
	return rt, st
}

func get(t *testing.T, rt *Runtime, name string) string {
	t.Helper()
	v, err := rt.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSetTogglesABoolAndSavesIt(t *testing.T) {
	rt, st := withConfig(t)
	var said []string
	if err := rt.Exec(&Call{Rest: []string{"allow_writes"}, Out: func(s string) { said = append(said, s) }}, "set"); err != nil {
		t.Fatal(err)
	}
	if get(t, rt, "allow_writes") != "true" || strings.Join(said, "") != "allow_writes = true" {
		t.Errorf("after set: %q, said %v", get(t, rt, "allow_writes"), said)
	}
	saved, _ := st.Config()
	if !saved.AllowWrites {
		t.Errorf("saved = %+v, want writes on", saved)
	}
	if err := rt.Set("allow_writes", "off"); err != nil || get(t, rt, "allow_writes") != "false" {
		t.Errorf("set off: %v", err)
	}
}

func TestNextStepsAChoiceAndAnIntCycle(t *testing.T) {
	rt, _ := withConfig(t)
	if get(t, rt, "batch_format") != "json" || get(t, rt, "page_step") != "10" {
		t.Fatalf("defaults: %s %s", get(t, rt, "batch_format"), get(t, rt, "page_step"))
	}
	run(t, rt, "set", "batch_format", "next")
	run(t, rt, "set", "page_step", "next")
	if get(t, rt, "batch_format") != "jsonl" || get(t, rt, "page_step") != "20" {
		t.Errorf("after next: %s %s", get(t, rt, "batch_format"), get(t, rt, "page_step"))
	}
	run(t, rt, "set", "batch_format", "ndjson")
	if get(t, rt, "batch_format") != "jsonl" {
		t.Errorf("an alias was not normalised: %s", get(t, rt, "batch_format"))
	}
}

func TestBadValuesChangeNothing(t *testing.T) {
	rt, st := withConfig(t)
	tests := []struct{ name, value, want string }{
		{"page_step", "lots", "not a whole number"},
		{"session_cols", "9", "outside 40–400"},
		{"allow_writes", "maybe", "not true or false"},
		{"default_mode", "macro", "not one of"},
		{"batch_format", "xml", "unknown output format"},
		{"nope", "1", "no option is called"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rt.Set(tt.name, tt.value)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("set %s %s = %v, want %q", tt.name, tt.value, err, tt.want)
			}
		})
	}
	saved, _ := st.Config()
	if saved.PageStep != 0 || saved.SessionCols != 0 || saved.AllowWrites || saved.DefaultMode != "" || saved.BatchFormat != "" {
		t.Errorf("a refused set was saved: %+v", saved)
	}
}

func TestDefaultModeAlsoSwitchesTheList(t *testing.T) {
	rt, _ := withConfig(t)
	rt.ArmMacro("x")
	run(t, rt, "set", "default_mode", "rich data")
	if rt.Mode() != ModeData || rt.Armed() != "" {
		t.Errorf("list mode = %q armed %q", rt.Mode(), rt.Armed())
	}
}

func TestOptionsListsEveryOne(t *testing.T) {
	rt, _ := withConfig(t)
	var lines []string
	if err := rt.Exec(&Call{Out: func(s string) { lines = append(lines, s) }}, "options"); err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(rt.Options()) || !strings.HasPrefix(lines[0], "allow_writes") {
		t.Errorf("options printed %v", lines)
	}
}

func TestSetWithNoConfigSaysSo(t *testing.T) {
	rt := New()
	if err := rt.Set("page_step", "5"); err == nil || !strings.Contains(err.Error(), "no config") {
		t.Errorf("set with no config = %v", err)
	}
	if v, err := rt.Get("page_step"); err != nil || v != "10" {
		t.Errorf("get with no config = %q %v, want the default", v, err)
	}
}
