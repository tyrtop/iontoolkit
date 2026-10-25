package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/search"
)

func searchStore(t *testing.T) *config.Store {
	t.Helper()
	t.Cleanup(func() {
		flagSearch, flagFormat, flagTenant = "", "", ""
		flagCommands, flagElement, flagElementsPath = nil, "", ""
		flagRefresh, flagNoValidate = false, false
		app.Defaults.TSGID = ""
	})

	st := &config.Store{Root: t.TempDir(), Secrets: config.NewMemorySecrets()}
	if err := st.SaveConfig(&config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Added: true},
		{Name: "Niagara Bottling", TSGID: "222", Added: true},
		{Name: "Kroger", TSGID: "333"},
	}}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return st
}

func seedCache(t *testing.T, st *config.Store, tsg string, at time.Time, els []api.Element, sites map[string]string) {
	t.Helper()
	if err := st.SaveCache(&config.ElementCache{
		TSGID: tsg, FetchedAt: at, Elements: els, Sites: sites,
	}); err != nil {
		t.Fatalf("SaveCache(%q) error = %v", tsg, err)
	}
}

func searchRuntime(t *testing.T, st *config.Store) *runtime.Runtime {
	t.Helper()
	cfg, err := st.Config()
	if err != nil {
		t.Fatal(err)
	}
	return app.NewRuntime(st, cfg)
}

func mustScope(t *testing.T, rt *runtime.Runtime) []config.Tenant {
	t.Helper()
	scope, err := rt.SearchScope(flagTenant, app.Defaults.TSGID)
	if err != nil {
		t.Fatalf("SearchScope() error = %v", err)
	}
	return scope
}

func findIn(t *testing.T, st *config.Store, query string) (runtime.Found, string) {
	t.Helper()
	rt := searchRuntime(t, st)
	var log bytes.Buffer
	found := find(context.Background(), rt, mustScope(t, rt), search.Parse(query), false, &log)
	return found, log.String()
}

func TestSearchScopeIsTheWorkingSet(t *testing.T) {
	rt := searchRuntime(t, searchStore(t))
	if names := runtime.TenantNames(mustScope(t, rt)); names != "Tractor Supply, Niagara Bottling" {
		t.Errorf("scope = %q, want both added tenants and not the unadded one", names)
	}
	flagTenant = "Kroger"
	if names := runtime.TenantNames(mustScope(t, rt)); names != "Kroger" {
		t.Errorf("scope = %q, want --tenant to win outright", names)
	}
	flagTenant = ""
	app.Defaults.TSGID = "222"
	if names := runtime.TenantNames(mustScope(t, rt)); names != "Niagara Bottling" {
		t.Errorf("scope = %q, want --tsg to narrow the set", names)
	}
	if _, err := rt.SearchScope("", "999"); err == nil {
		t.Error("SearchScope() error = nil, want an unknown --tsg to fail")
	}
}

func TestSearchScopeFallsBackBeforeAnythingIsAdded(t *testing.T) {
	st := searchStore(t)
	if err := st.SaveConfig(&config.Config{
		LastTenant: "Kroger",
		Tenants:    []config.Tenant{{Name: "Kroger", TSGID: "333"}},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if names := runtime.TenantNames(mustScope(t, searchRuntime(t, st))); names != "Kroger" {
		t.Errorf("scope = %q, want a fresh setup to still be searchable", names)
	}
}

func TestSearchAnswersFromAFreshCacheWithoutDialling(t *testing.T) {
	st := searchStore(t)
	seedCache(t, st, "111", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", SiteID: "s1", Model: "ion 1200", Connected: true},
		{ID: "e2", Name: "s0200-rtr-01", SiteID: "s2", Model: "ion 3200"},
	}, map[string]string{"111/s1": "Store 0100", "111/s2": "Store 0200"})
	seedCache(t, st, "222", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e3", Name: "ord-gw-01", SiteID: "s9", Model: "ion 1200", Connected: true},
	}, map[string]string{"222/s9": "ORD Plant"})

	found, log := findIn(t, st, "1200 connected")
	if err := found.Err(); err != nil {
		t.Fatal(err)
	}
	if len(found.Hits) != 2 {
		t.Fatalf("matched %d elements, want the query to span both tenants: %v", len(found.Hits), names(found.Hits))
	}
	if got := names(found.Hits); got[0] != "ord-gw-01" || got[1] != "s0100-rtr-01" {
		t.Errorf("matched %v, want them in name order", got)
	}
	if !strings.Contains(log, "from the cache") {
		t.Errorf("the log does not say the cache answered:\n%s", log)
	}
}

func TestSearchSaysWhenAnswerIsFromAStaleCache(t *testing.T) {
	st := searchStore(t)
	if err := st.SaveConfig(&config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Added: true},
	}}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	seedCache(t, st, "111", time.Now().Add(-2*config.CacheFresh), []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", Model: "ion 1200"},
	}, nil)

	found, log := findIn(t, st, "s0100")
	if len(found.Hits) != 1 {
		t.Fatalf("matched %d, want the stale cache used rather than dropped", len(found.Hits))
	}
	for _, want := range []string{"stale cache", "30m old", "SCM_CLIENT_ID"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log is missing %q:\n%s", want, log)
		}
	}
}

func TestSearchFailsWhenNoTenantCanBeReached(t *testing.T) {
	if found, _ := findIn(t, searchStore(t), "anything"); found.Err() == nil {
		t.Error("Err() = nil, want a failure when nothing could be searched")
	}
}

func TestAnUnreachableTenantDoesNotSinkTheSearch(t *testing.T) {
	st := searchStore(t)
	seedCache(t, st, "111", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", Model: "ion 1200"},
	}, nil)

	found, log := findIn(t, st, "s0100")
	if err := found.Err(); err != nil {
		t.Fatalf("Err() = %v, want the reachable tenant to still answer", err)
	}
	if len(found.Hits) != 1 {
		t.Fatalf("matched %d, want the one element the reachable tenant holds", len(found.Hits))
	}
	if !strings.Contains(log, "Niagara Bottling: unreachable") {
		t.Errorf("an unreachable tenant was not reported:\n%s", log)
	}
	if !strings.Contains(log, "Tractor Supply: 1 elements") {
		t.Errorf("the reachable tenant was not reported:\n%s", log)
	}
}

func TestResultsKeepTenantOrderThoughTenantsLoadTogether(t *testing.T) {
	st := searchStore(t)
	seedCache(t, st, "111", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e1", Name: "rtr-b"}, {ID: "e2", Name: "rtr-d"},
	}, nil)
	seedCache(t, st, "222", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e3", Name: "rtr-a"}, {ID: "e4", Name: "rtr-c"},
	}, nil)

	want := []string{"rtr-a", "rtr-b", "rtr-c", "rtr-d"}
	for i := range 5 {
		found, _ := findIn(t, st, "rtr")
		got := names(found.Hits)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("run %d matched %v, want %v in name order", i, got, want)
		}
	}
}

func TestAStaleAnswerIsMarkedForRetryNotTakenAsGood(t *testing.T) {
	st := searchStore(t)
	if err := st.SaveConfig(&config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Added: true, Auth: config.AuthBrowser},
	}}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	seedCache(t, st, "111", time.Now().Add(-2*config.CacheFresh),
		[]api.Element{{ID: "e1", Name: "s0100-rtr-01"}}, nil)

	rt := searchRuntime(t, st)
	l := rt.LoadTenant(context.Background(), mustScope(t, rt)[0], false)
	if l.Err != nil {
		t.Errorf("err = %v, want the stale cache to still answer", l.Err)
	}
	if l.Stale == nil {
		t.Error("stale = nil, want the refresh failure recorded behind the stale answer")
	}
	if len(l.Elements) != 1 {
		t.Errorf("loaded %d elements, want the cached one", len(l.Elements))
	}
}

func TestRenewAsksBeforeOpeningABrowser(t *testing.T) {
	st := searchStore(t)
	tenants := mustScope(t, searchRuntime(t, st))
	for _, tt := range []struct{ name, answer string }{{"declined", "n\n"}, {"nothing typed", "\n"}} {
		t.Run(tt.name, func(t *testing.T) {
			var log bytes.Buffer
			if renew(context.Background(), searchRuntime(t, st), tenants, strings.NewReader(tt.answer), &log, true) {
				t.Error("renew() = true after a refusal, want nothing reloaded")
			}
			out := log.String()
			for _, want := range []string{"expired", "[y/N]", "Tractor Supply", "Niagara Bottling"} {
				if !strings.Contains(out, want) {
					t.Errorf("the question is missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestAScriptIsToldWhatToRunRatherThanAsked(t *testing.T) {
	st := searchStore(t)
	var log bytes.Buffer
	if renew(context.Background(), searchRuntime(t, st), mustScope(t, searchRuntime(t, st)), nil, &log, false) {
		t.Error("renew() = true for a scripted run")
	}
	out := log.String()
	if strings.Contains(out, "[y/N]") {
		t.Errorf("a question was put where nothing can answer it:\n%s", out)
	}
	if !strings.Contains(out, "iontk auth login") {
		t.Errorf("the log does not name the way out:\n%s", out)
	}
}

func TestTenantsNeedingACredentialAreNamedWithTheWayOut(t *testing.T) {
	st := searchStore(t)
	if err := st.SaveConfig(&config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Added: true, Auth: config.AuthBrowser},
	}}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	seedCache(t, st, "111", time.Now().Add(-2*config.CacheFresh),
		[]api.Element{{ID: "e1", Name: "s0100-rtr-01"}}, nil)

	found, _ := findIn(t, st, "s0100")
	if len(found.Hits) != 1 {
		t.Fatalf("matched %d, want the stale cache to still answer", len(found.Hits))
	}
	if len(found.SignIn) != 1 {
		t.Fatalf("SignIn = %v, want the browser tenant marked for a sign-in", found.SignIn)
	}
	var log bytes.Buffer
	renew(context.Background(), searchRuntime(t, st), found.SignIn, nil, &log, false)
	if !strings.Contains(log.String(), "iontk auth login") {
		t.Errorf("the log does not name the way out:\n%s", log.String())
	}
}

func names(hits []runtime.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Record.Name)
	}
	return out
}

func twoTenantHits() []runtime.Hit {
	sites := search.SiteMap{"111/s1": "Store 0100"}
	a := api.Element{ID: "e1", Name: "s0100-rtr-01", SiteID: "s1", Model: "ion 1200", Connected: true}
	b := api.Element{ID: "e3", Name: "ord-gw-01", Model: "ion 3200"}
	return []runtime.Hit{
		{Element: a, Tenant: config.Tenant{Name: "Tractor Supply", TSGID: "111"},
			Record: search.Of(a, "111", "Tractor Supply", sites)},
		{Element: b, Tenant: config.Tenant{Name: "Niagara Bottling", TSGID: "222"},
			Record: search.Of(b, "222", "Niagara Bottling", sites)},
	}
}

func TestSearchFormatIsNotTheBatchFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", searchTable, false},
		{"table", searchTable, false},
		{"IDS", searchIDs, false},
		{" json ", searchJSON, false},
		{"csv", "", true},
		{"jsonl", "", true},
		{"nonsense", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			flagFormat = tt.in
			t.Cleanup(func() { flagFormat = "" })
			got, err := searchFormat()
			if (err != nil) != tt.wantErr {
				t.Fatalf("searchFormat(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("searchFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrintHitsIDsAreBareSoTheyPipe(t *testing.T) {
	var buf bytes.Buffer
	if err := printHits(&buf, twoTenantHits(), searchIDs); err != nil {
		t.Fatalf("printHits() error = %v", err)
	}
	if got := buf.String(); got != "e1\ne3\n" {
		t.Errorf("printHits(ids) = %q, want one bare ID per line", got)
	}
}

func TestPrintHitsNamesTheTenantOnlyWhenItVaries(t *testing.T) {
	var both bytes.Buffer
	if err := printHits(&both, twoTenantHits(), searchTable); err != nil {
		t.Fatalf("printHits() error = %v", err)
	}
	if !strings.Contains(both.String(), "TENANT") {
		t.Errorf("the tenant column is missing from a cross-tenant list:\n%s", both.String())
	}

	var one bytes.Buffer
	if err := printHits(&one, twoTenantHits()[:1], searchTable); err != nil {
		t.Fatalf("printHits() error = %v", err)
	}
	if strings.Contains(one.String(), "TENANT") {
		t.Errorf("a single tenant should not get a column of its own:\n%s", one.String())
	}
	if !strings.Contains(one.String(), "Store 0100") {
		t.Errorf("the resolved site name is missing:\n%s", one.String())
	}
}

func TestPrintHitsJSONCarriesTheTenant(t *testing.T) {
	var buf bytes.Buffer
	if err := printHits(&buf, twoTenantHits(), searchJSON); err != nil {
		t.Fatalf("printHits() error = %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, buf.String())
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(rows))
	}
	for _, want := range []string{"id", "tenant", "tsg_id"} {
		if _, ok := rows[0][want]; !ok {
			t.Errorf("the JSON row has no %q: %v", want, rows[0])
		}
	}
}

func TestConfirmRunRefusesRatherThanHangOnAPipe(t *testing.T) {
	flagSearch, flagCommands = "store", []string{"dump device status"}
	t.Cleanup(func() { flagSearch, flagCommands, flagNoValidate = "", nil, false })

	var w bytes.Buffer
	err := confirmRun(strings.NewReader(""), &w, false, twoTenantHits())
	if err == nil {
		t.Fatal("confirmRun() error = nil, want it to refuse a question nobody can answer")
	}
	if !strings.Contains(err.Error(), "--no-validate") {
		t.Errorf("confirmRun() error = %v, want it to name the way out", err)
	}
	if w.Len() != 0 {
		t.Errorf("a prompt was written to a pipe that cannot answer it:\n%s", w.String())
	}

	flagNoValidate = true
	if err := confirmRun(strings.NewReader(""), &w, false, twoTenantHits()); err != nil {
		t.Errorf("confirmRun() with --no-validate error = %v, want it skipped", err)
	}
}

func TestConfirmRunNeedsAnExplicitYes(t *testing.T) {
	flagSearch, flagCommands = "store", []string{"dump device status", "uptime"}
	t.Cleanup(func() { flagSearch, flagCommands = "", nil })

	tests := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
		{"yeah\n", false},
	}
	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.answer), func(t *testing.T) {
			var w bytes.Buffer
			err := confirmRun(strings.NewReader(tt.answer), &w, true, twoTenantHits())
			if (err == nil) != tt.want {
				t.Errorf("confirmRun(%q) error = %v, want proceed = %v", tt.answer, err, tt.want)
			}
			out := w.String()
			for _, want := range []string{"2 commands on 2 elements", "$ uptime", "s0100-rtr-01", "ord-gw-01", "continue?"} {
				if !strings.Contains(out, want) {
					t.Errorf("the prompt is missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestABrowserIsNeverOpenedWithoutATerminal(t *testing.T) {
	t.Cleanup(func() { flagNoLogin = false })
	if canSignIn() {
		t.Error("canSignIn() = true with no terminal, want a scripted run left alone")
	}
	flagNoLogin = true
	if canSignIn() {
		t.Error("canSignIn() = true with --no-login, want it refused outright")
	}
}
