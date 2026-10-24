package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
)

func throwaway(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	app.Debug = true
	t.Cleanup(func() { app.Debug = false })
}

func tree(in io.Reader) *cobra.Command {
	root := &cobra.Command{Use: "iontk", SilenceUsage: true, SilenceErrors: true}
	addOwn(root, "batch run", &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }})
	addRegistry(root, in, cliRuntime().Commands())
	return root
}

func run(t *testing.T, in string, argv ...string) (string, error) {
	t.Helper()
	root := tree(strings.NewReader(in))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(argv)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestTheTreeIsTheRegistry(t *testing.T) {
	throwaway(t)
	root := tree(strings.NewReader(""))
	find := func(path string) *cobra.Command {
		t.Helper()
		c, rest, err := root.Find(strings.Fields(path))
		if err != nil || len(rest) != 0 || c.Name() != strings.Fields(path)[len(strings.Fields(path))-1] {
			t.Fatalf("no command at %q", path)
		}
		return c
	}
	if f := find("list filter").Flags().Lookup("query"); f == nil || f.Value.Type() != "string" {
		t.Error("list filter has no --query")
	}
	if f := find("element inspect").Flags().Lookup("raw"); f == nil || f.Value.Type() != "bool" {
		t.Error("element inspect --raw is not a switch")
	}
	if find("history").RunE == nil || find("history forget").RunE == nil {
		t.Error("history and history forget are not both commands")
	}
	if find("write").Flags().Lookup("doit") == nil {
		t.Error("a command that writes has no --doit")
	}
	if find("data open").Flags().Lookup("doit") != nil {
		t.Error("a command that does not write has --doit")
	}
	if find("batch run").Annotations[ownCommand] != "yes" || find("batch save").RunE == nil {
		t.Error("the registry replaced a command written here, or left its sibling out")
	}
	if find("auth").Short == "" {
		t.Error("a group says nothing about what is under it")
	}
}

func TestACommandRunsWithItsFlags(t *testing.T) {
	throwaway(t)
	if out, err := run(t, "", "set", "page_step", "20"); err != nil || strings.TrimSpace(out) != "page_step = 20" {
		t.Errorf("set page_step 20: %v %q", err, out)
	}
	if out, err := run(t, "", "set", "--name", "batch_format", "--value", "csv"); err != nil || strings.TrimSpace(out) != "batch_format = csv" {
		t.Errorf("set by flags: %v %q", err, out)
	}
	if _, err := run(t, "", "set", "page_step", "lots"); err == nil {
		t.Error("a bad value was taken")
	}
	if _, err := run(t, "", "list", "filter", "--nope", "x"); err == nil {
		t.Error("an undeclared flag was taken")
	}
}

func TestADashReadsTheValueFromStandardInput(t *testing.T) {
	throwaway(t)
	if _, err := run(t, "", "tenant", "add", "--name", "Acme Co", "--tsg", "111", "--auth", "browser"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "hunter2\r\nsecond\n", "user", "add", "--name", "admin", "--password", "-"); err != nil {
		t.Fatal(err)
	}
	st := app.OpenStore()
	if v, _ := st.Secrets.Get(config.IONPassKey("111", "admin")); v != "hunter2" {
		t.Errorf("password saved = %q", v)
	}
	if _, err := run(t, "", "user", "password", "--user", "admin", "--password", "-"); err == nil {
		t.Error("an empty standard input filled --password -")
	}
}

func TestAWorkflowIsNamedOrAPath(t *testing.T) {
	throwaway(t)
	dir := filepath.Join(t.TempDir(), "elsewhere", "site-report")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("title: Site report\ninputs:\n  - name: site\n    required: true\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "main.lua"), []byte("return function(ctx) end\n"), 0o600)
	t.Cleanup(func() { app.WorkflowPaths, workflowAliases = nil, map[string]string{} })

	if err := workflowByPath([]string{"workflow", "site-report"}); err != nil || len(app.WorkflowPaths) != 0 {
		t.Fatalf("a bare name was read as a path: %v %v", err, app.WorkflowPaths)
	}
	if err := workflowByPath([]string{"workflow", dir + "/"}); err != nil {
		t.Fatal(err)
	}
	if err := workflowByPath([]string{"workflow", filepath.Join(t.TempDir(), "nothing")}); err == nil {
		t.Error("a path with no workflow in it was taken")
	}

	root := tree(strings.NewReader(""))
	aliasWorkflowPaths(root)
	c, _, err := root.Find([]string{"workflow", dir + "/"})
	if err != nil || c.Name() != "site-report" || c.Flags().Lookup("site") == nil || c.Flags().Lookup("doit") == nil {
		t.Fatalf("workflow <path> is not the workflow: %v %v", c.Name(), err)
	}
}
