package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestEveryFlagIsGrouped(t *testing.T) {
	if _, err := groupFlags(rootCmd); err != nil {
		t.Errorf("groupFlags() error = %v", err)
	}
}

func TestAnUngroupedFlagFailsGeneration(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().String("brand-new", "", "something nobody classified")

	_, err := groupFlags(cmd)
	if err == nil {
		t.Fatal("groupFlags() error = nil, want an ungrouped flag to fail")
	}
	for _, want := range []string{"brand-new", "flagdoc.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestGroupedFlagsKeepTheirListedOrder(t *testing.T) {
	grouped, err := groupFlags(rootCmd)
	if err != nil {
		t.Fatalf("groupFlags() error = %v", err)
	}

	var want []string
	for _, g := range flagGroups {
		if g.name == "Connection" {
			want = g.flags
		}
	}
	got := grouped["Connection"]
	if len(got) != len(want) {
		t.Fatalf("Connection has %d flags, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("Connection[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

func TestDeprecatedFlagsAreSeparated(t *testing.T) {
	grouped, err := groupFlags(rootCmd)
	if err != nil {
		t.Fatalf("groupFlags() error = %v", err)
	}
	dep := grouped["Deprecated"]
	if len(dep) == 0 {
		t.Skip("nothing is deprecated right now")
	}
	for _, f := range dep {
		if f.Deprecated == "" {
			t.Errorf("--%s is in Deprecated with no deprecation message", f.Name)
		}
	}
	for _, g := range flagGroups {
		for _, f := range grouped[g.name] {
			if f.Deprecated != "" {
				t.Errorf("--%s is deprecated but listed under %s", f.Name, g.name)
			}
		}
	}
}

func TestTheDocMentionsEveryFlagAndEnvVar(t *testing.T) {
	var b strings.Builder
	if err := writeFlagDocs(&b, rootCmd); err != nil {
		t.Fatalf("writeFlagDocs() error = %v", err)
	}
	doc := b.String()

	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !strings.Contains(doc, "`--"+f.Name+"`") {
			t.Errorf("--%s is missing from the generated doc", f.Name)
		}
	})
	for _, env := range []string{
		"SCM_CLIENT_ID", "SCM_CLIENT_SECRET", "SCM_TSG_ID", "SCM_TOKEN",
		"ION_USER", "ION_PASS",
	} {
		if !strings.Contains(doc, env) {
			t.Errorf("%s is missing from the generated doc", env)
		}
	}
}

func TestGeneratingIntoAMissingDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "docs", "flags.md")

	if err := genFlagDocsCmd.RunE(genFlagDocsCmd, []string{out}); err != nil {
		t.Fatalf("gen-flag-docs %q error = %v", out, err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", out, err)
	}
	if !strings.HasPrefix(string(got), "# Flags") {
		t.Errorf("%q starts with %q, want a flag reference", out, string(got[:min(len(got), 20)]))
	}
}

func TestTheGeneratorIsHidden(t *testing.T) {
	if !genFlagDocsCmd.Hidden {
		t.Error("gen-flag-docs is not hidden")
	}
	if strings.Contains(genFlagDocsCmd.Name(), " ") {
		t.Errorf("command name %q has a space in it", genFlagDocsCmd.Name())
	}
}
