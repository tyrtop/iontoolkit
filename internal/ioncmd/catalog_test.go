package ioncmd

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/ioncli"
)

func TestEmbeddedReferenceParses(t *testing.T) {
	cmds := Embedded()
	if len(cmds) < 500 {
		t.Fatalf("parsed %d commands, want at least 500", len(cmds))
	}

	index := map[string]bool{}
	for _, c := range cmds {
		index[c] = true
	}

	for _, want := range []string{
		"dump",
		"dump device status",
		"dump interface status",
		"inspect system arp",
		"config",
	} {
		if !index[want] {
			t.Errorf("reference missing %q", want)
		}
	}
}

func TestParsedCommandsAreCleanStrings(t *testing.T) {
	for _, c := range Embedded() {
		if c == "" {
			t.Fatal("empty command in catalog")
		}
		if strings.ContainsAny(c, "┬├└─│") {
			t.Errorf("box-drawing char survived into %q", c)
		}
		if c != strings.TrimSpace(c) || strings.Contains(c, "  ") {
			t.Errorf("badly spaced command %q", c)
		}
	}
}

func TestFuzzyPrefersSubstringMatches(t *testing.T) {
	cmds := []string{
		"dump interface status",
		"dump device status",
		"debug utility ping status",
	}

	got := Fuzzy(cmds, "interface status")
	if len(got) == 0 || got[0] != "dump interface status" {
		t.Errorf("Fuzzy = %v, want the substring match first", got)
	}
}

func TestFuzzyMatchesOutOfOrderFragments(t *testing.T) {
	cmds := []string{"dump interface status", "dump device status", "config banner"}

	got := Fuzzy(cmds, "dmpifst")
	if len(got) == 0 {
		t.Fatal("fuzzy found nothing for an abbreviation")
	}
	if got[0] != "dump interface status" {
		t.Errorf("best match = %q, want dump interface status", got[0])
	}

	if n := len(Fuzzy(cmds, "zzzz")); n != 0 {
		t.Errorf("nonsense query matched %d commands", n)
	}
}

func TestFuzzyEmptyQueryReturnsEverything(t *testing.T) {
	cmds := []string{"a", "b"}
	if got := Fuzzy(cmds, "  "); len(got) != 2 {
		t.Errorf("empty query returned %d, want all", len(got))
	}
}

func TestFuzzyOnTheRealCatalog(t *testing.T) {
	cmds := Embedded()

	got := Fuzzy(cmds, "dump int stat")
	if len(got) == 0 {
		t.Fatal("no hits for 'dump int stat'")
	}
	if !strings.HasPrefix(got[0], "dump interface status") {
		t.Errorf("best match = %q", got[0])
	}

	if got := Fuzzy(cmds, "uptime"); len(got) != 0 {
		t.Logf("note: %d commands match 'uptime': %v", len(got), got[:min(3, len(got))])
	}
}

func TestReadOnlyFilterAgreesWithTheGuard(t *testing.T) {
	all := Embedded()
	ro := ReadOnly(all, ioncli.IsReadOnly)

	if len(ro) == 0 || len(ro) >= len(all) {
		t.Fatalf("read-only subset is %d of %d", len(ro), len(all))
	}
	for _, c := range ro {
		if !ioncli.IsReadOnly(c) {
			t.Errorf("%q passed the filter but fails the guard", c)
		}
	}
	for _, c := range ro {
		if v := ioncli.Verb(c); v == "config" || v == "set" || v == "clear" {
			t.Errorf("mutating command %q in the read-only subset", c)
		}
	}
	t.Logf("read-only: %d of %d commands", len(ro), len(all))
}
