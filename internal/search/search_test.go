package search

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
)

func fleet() []Record {
	sites := SiteMap{
		"111/s100": "Store 0100",
		"111/s200": "Store 0200",
		"222/s9":   "ORD Plant",
	}
	var out []Record
	for _, el := range []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", SiteID: "s100", Model: "ion1200", Software: "6.5.1", HWID: "HW-AAA", Connected: true},
		{ID: "e2", Name: "s0100-rtr-02", SiteID: "s100", Model: "ion3200", Software: "6.5.1", HWID: "HW-BBB"},
		{ID: "e3", Name: "s0200-rtr-01", SiteID: "s200", Model: "ion1200", Software: "6.4.3", HWID: "HW-CCC", Connected: true},
	} {
		out = append(out, Of(el, "111", "Tractor Supply", sites))
	}
	out = append(out, Of(
		api.Element{ID: "e4", Name: "ord-gw-01", SiteID: "s9", Model: "ion1200", Software: "6.5.1", HWID: "HW-DDD", Connected: true},
		"222", "Niagara Bottling", sites))
	return out
}

func matched(t *testing.T, records []Record, query string) []string {
	t.Helper()
	q := Parse(query)
	var out []string
	for _, r := range records {
		if q.Match(r) {
			out = append(out, r.Name)
		}
	}
	return out
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	have := map[string]int{}
	for _, g := range got {
		have[g]++
	}
	for _, w := range want {
		have[w]--
	}
	for _, n := range have {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestEveryTermHasToMatchSomeField(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "site, model and tenant together",
			query: "100 1200 tract",
			want:  []string{"s0100-rtr-01"},
		},
		{"nothing typed matches everything", "", []string{"ord-gw-01", "s0100-rtr-01", "s0100-rtr-02", "s0200-rtr-01"}},
		{"one term still works", "1200", []string{"ord-gw-01", "s0100-rtr-01", "s0200-rtr-01"}},
		{"tenant name alone", "niagara", []string{"ord-gw-01"}},
		{"model and tenant", "1200 niagara", []string{"ord-gw-01"}},
		{"site name words", "store 0200", []string{"s0200-rtr-01"}},
		{"software version", "6.4.3", []string{"s0200-rtr-01"}},
		{"hardware id", "hw-ccc", []string{"s0200-rtr-01"}},
		{"connection state", "offline", []string{"s0100-rtr-02"}},
		{"state and model", "connected ion3200", nil},
		{"element id", "e4", []string{"ord-gw-01"}},
		{"terms in any order", "tract 1200 100", []string{"s0100-rtr-01"}},
		{"a term matching nothing excludes everything", "1200 nosuchtenant", nil},
		{"case is ignored", "TRACT ION1200", []string{"s0100-rtr-01", "s0200-rtr-01"}},
		{"extra spaces are ignored", "  1200   niagara  ", []string{"ord-gw-01"}},
		{"a term matching nothing wins over one that matches", "s0100 ion9999", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matched(t, fleet(), tt.query); !same(got, tt.want) {
				t.Errorf("query %q matched %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestOnlyNamesThatContainTheTermMatch(t *testing.T) {
	var records []Record
	for _, el := range []api.Element{
		{ID: "e1", Name: "s1405-rtr-02", Model: "ion 2000"},
		{ID: "e2", Name: "s0502-rtr-01", Model: "ion 1200"},
		{ID: "e3", Name: "s502-rtr-01", Model: "ion 1200"},
	} {
		records = append(records, Of(el, "111", "Tractor Supply", nil))
	}

	for query, want := range map[string][]string{
		"s502":  {"s502-rtr-01"},
		"s0502": {"s0502-rtr-01"},
	} {
		if got := matched(t, records, query); !same(got, want) {
			t.Errorf("query %q matched %q, want %q", query, got, want)
		}
	}
}

func TestTwoCharacterTermsStayLiteral(t *testing.T) {
	for _, r := range fleet() {
		if !Parse("or").Match(r) {
			continue
		}
		var literal bool
		for _, f := range r.Fields() {
			if !f.Exact && strings.Contains(f.Value, "or") {
				literal = true
			}
		}
		if !literal {
			t.Errorf("%q matched \"or\" without containing it in any field", r.Name)
		}
	}
}

func TestOpaqueIdentifiersNeedTheWholeValue(t *testing.T) {
	r := Of(api.Element{
		ID: "1695401228372008796", Name: "s1405-rtr-01",
		SiteID: "1695401228372008796", Model: "ion 2000", HWID: "0123456789",
	}, "1234567890", "Tractor Supply", nil)

	for _, partial := range []string{"16954", "372008", "228372", "34567", "123456789"} {
		if Parse(partial).Match(r) {
			t.Errorf("query %q matched a partial identifier", partial)
		}
	}
	for _, whole := range []string{"1695401228372008796", "1234567890", "0123456789"} {
		if !Parse(whole).Match(r) {
			t.Errorf("query %q did not match the identifier it names in full", whole)
		}
	}
}

func TestAnUnresolvedSiteIsTreatedAsOpaque(t *testing.T) {
	el := api.Element{ID: "e1", Name: "s1405-rtr-01", SiteID: "1695401228372008796", Model: "ion 2000"}

	unresolved := Of(el, "111", "Tractor Supply", SiteMap{})
	if unresolved.SiteKnown {
		t.Error("an empty site map resolved a site")
	}
	if Parse("12283").Match(unresolved) {
		t.Error("a term matched inside an unresolved site id")
	}

	resolved := Of(el, "111", "Tractor Supply", SiteMap{"111/1695401228372008796": "Store 1405"})
	if !Parse("store 1405").Match(resolved) {
		t.Error("a resolved site name is not searchable")
	}
}

func TestASiteResolvedInAnotherTenantDoesNotCountHere(t *testing.T) {
	sites := SiteMap{"111/s100": "Store 0100"}
	r := Of(api.Element{ID: "e1", Name: "ord-gw-01", SiteID: "s100"}, "222", "Niagara Bottling", sites)
	if r.SiteKnown {
		t.Fatalf("SiteKnown = true, want the other tenant's name ignored")
	}
	if Parse("store").Match(r) {
		t.Error("matched another tenant's site name")
	}
}

func TestAnEmptySiteNameIsNotAResolvedOne(t *testing.T) {
	r := Of(api.Element{ID: "e1", Name: "s1405-rtr-01", SiteID: "1695401228372008796"},
		"111", "Tractor Supply", SiteMap{"111/1695401228372008796": ""})
	if r.SiteKnown {
		t.Fatal("an empty name counted as resolved")
	}
	if Parse("12283").Match(r) {
		t.Error("a term matched inside a site id whose name was empty")
	}
}

func TestTheStoreQueryThatHadToWork(t *testing.T) {
	sites := SiteMap{"111/s50": "Store 5024"}
	var records []Record
	for _, el := range []api.Element{
		{ID: "1695401228372008796", Name: "s5024-rtr-01", SiteID: "s50", Model: "ion 1200", Connected: true},
		{ID: "1695401228372008797", Name: "s1405-rtr-01", SiteID: "x", Model: "ion 2000", Connected: true},
		{ID: "1695401228372008798", Name: "s0502-rtr-01", SiteID: "x", Model: "ion 3200", Connected: true},
	} {
		records = append(records, Of(el, "111", "Tractor Supply", sites))
	}
	if got := matched(t, records, "s50 ion 1200"); !same(got, []string{"s5024-rtr-01"}) {
		t.Errorf("matched %q, want only s5024-rtr-01", got)
	}
}

func TestSiteLabelNeverLeavesTheColumnBlank(t *testing.T) {
	tests := []struct {
		name string
		el   api.Element
		want string
	}{
		{"resolved", api.Element{SiteID: "s100"}, "Store 0100"},
		{"unresolved falls back to the id", api.Element{SiteID: "s999"}, "s999"},
		{"no site at all", api.Element{}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Of(tt.el, "111", "Tractor Supply", SiteMap{"111/s100": "Store 0100"})
			if got := r.SiteLabel(); got != tt.want {
				t.Errorf("SiteLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnEmptyFieldIsNotAWildcard(t *testing.T) {
	r := Of(api.Element{ID: "e1", Name: "s0100-rtr-01"}, "111", "Tractor Supply", nil)
	if Parse("ion1200").Match(r) {
		t.Error("an element with no model matched a model term")
	}
}
