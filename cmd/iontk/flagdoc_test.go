package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestEveryFlagIsGrouped(t *testing.T) {
	// The guard that makes the generated doc worth having: a new flag cannot
	// quietly land in a catch-all section nobody reads.
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
	// The message has to name the flag and where to put it, or it is a riddle.
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

	// Related timeouts read better together than alphabetically.
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
	// A deprecated flag must not also appear in a live group, or it reads as
	// current.
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
	// These are read in BaseFlags() and documented nowhere else.
	for _, env := range []string{
		"SCM_CLIENT_ID", "SCM_CLIENT_SECRET", "SCM_TSG_ID", "SCM_TOKEN",
		"ION_USER", "ION_PASS",
	} {
		if !strings.Contains(doc, env) {
			t.Errorf("%s is missing from the generated doc", env)
		}
	}
}

func TestTheGeneratorIsHidden(t *testing.T) {
	// It would otherwise show up in --help beside run and shell.
	if !genFlagDocsCmd.Hidden {
		t.Error("gen-flag-docs is not hidden")
	}
	if strings.Contains(genFlagDocsCmd.Name(), " ") {
		t.Errorf("command name %q has a space in it", genFlagDocsCmd.Name())
	}
}
