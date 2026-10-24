package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/search"
)

// not config.OpenDebug, whose root is one fixed path that every test binary
// clears on first open
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

func TestSearchScopeIsTheWorkingSet(t *testing.T) {
	st := searchStore(t)

	got, err := searchScope(st)
	if err != nil {
		t.Fatalf("searchScope() error = %v", err)
	}
	if names := tenantNames(got); names != "Tractor Supply, Niagara Bottling" {
		t.Errorf("searchScope() = %q, want both added tenants and not the unadded one", names)
	}

	flagTenant = "Kroger"
	got, err = searchScope(st)
	if err != nil {
		t.Fatalf("searchScope() error = %v", err)
	}
	if names := tenantNames(got); names != "Kroger" {
		t.Errorf("searchScope() = %q, want --tenant to win outright", names)
	}
	flagTenant = ""

	app.Defaults.TSGID = "222"
	got, err = searchScope(st)
	if err != nil {
		t.Fatalf("searchScope() error = %v", err)
	}
	if names := tenantNames(got); names != "Niagara Bottling" {
		t.Errorf("searchScope() = %q, want --tsg to narrow the set", names)
	}

	app.Defaults.TSGID = "999"
	if _, err := searchScope(st); err == nil {
		t.Error("searchScope() error = nil, want an unknown --tsg to fail")
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
	got, err := searchScope(st)
	if err != nil {
		t.Fatalf("searchScope() error = %v", err)
	}
	if names := tenantNames(got); names != "Kroger" {
		t.Errorf("searchScope() = %q, want a fresh setup to still be searchable", names)
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

	flagSearch = "1200 connected"
	var log bytes.Buffer
	hits, err := newFleet(st, nil, &log, false, false).find(context.Background(), mustScope(t, st), search.Parse(flagSearch))
	if err != nil {
		t.Fatalf("findElements() error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("matched %d elements, want the query to span both tenants: %v", len(hits), names(hits))
	}
	if got := names(hits); got[0] != "ord-gw-01" || got[1] != "s0100-rtr-01" {
		t.Errorf("matched %v, want them in name order", got)
	}
	if out := log.String(); !strings.Contains(out, "from the cache") {
		t.Errorf("the log does not say the cache answered:\n%s", out)
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

	flagSearch = "s0100"
	var log bytes.Buffer
	hits, err := newFleet(st, nil, &log, false, false).find(context.Background(), mustScope(t, st), search.Parse(flagSearch))
	if err != nil {
		t.Fatalf("findElements() error = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("matched %d, want the stale cache used rather than dropped", len(hits))
	}
	out := log.String()
	if !strings.Contains(out, "stale cache") {
		t.Errorf("a stale answer was not called one:\n%s", out)
	}
	if !strings.Contains(out, "30m old") {
		t.Errorf("the log does not say how old the answer is:\n%s", out)
	}
	if !strings.Contains(out, "SCM_CLIENT_ID") {
		t.Errorf("the log does not say why it could not refresh:\n%s", out)
	}
}

func TestSearchFailsWhenNoTenantCanBeReached(t *testing.T) {
	st := searchStore(t)
	flagSearch = "anything"
	var log bytes.Buffer
	if _, err := newFleet(st, nil, &log, false, false).find(context.Background(), mustScope(t, st), search.Parse(flagSearch)); err == nil {
		t.Error("findElements() error = nil, want a failure when nothing could be searched")
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

func mustScope(t *testing.T, st *config.Store) []config.Tenant {
	t.Helper()
	scope, err := searchScope(st)
	if err != nil {
		t.Fatalf("searchScope() error = %v", err)
	}
	return scope
}

func names(hits []hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.record.Name)
	}
	return out
}

func twoTenantHits() []hit {
	sites := search.SiteMap{"111/s1": "Store 0100"}
	a := api.Element{ID: "e1", Name: "s0100-rtr-01", SiteID: "s1", Model: "ion 1200", Connected: true}
	b := api.Element{ID: "e3", Name: "ord-gw-01", Model: "ion 3200"}
	return []hit{
		{element: a, tenant: config.Tenant{Name: "Tractor Supply", TSGID: "111"},
			record: search.Of(a, "111", "Tractor Supply", sites)},
		{element: b, tenant: config.Tenant{Name: "Niagara Bottling", TSGID: "222"},
			record: search.Of(b, "222", "Niagara Bottling", sites)},
	}
}

func TestAnUnreachableTenantDoesNotSinkTheSearch(t *testing.T) {
	st := searchStore(t)
	seedCache(t, st, "111", time.Now().Add(-time.Minute), []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", Model: "ion 1200"},
	}, nil)

	flagSearch = "s0100"
	var log bytes.Buffer
	hits, err := newFleet(st, nil, &log, false, false).find(
		context.Background(), mustScope(t, st), search.Parse(flagSearch))
	if err != nil {
		t.Fatalf("find() error = %v, want the reachable tenant to still answer", err)
	}
	if len(hits) != 1 {
		t.Fatalf("matched %d, want the one element the reachable tenant holds", len(hits))
	}

	out := log.String()
	if !strings.Contains(out, "Niagara Bottling: unreachable") {
		t.Errorf("an unreachable tenant was not reported:\n%s", out)
	}
	if !strings.Contains(out, "Tractor Supply: 1 elements") {
		t.Errorf("the reachable tenant was not reported:\n%s", out)
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

	flagSearch = "rtr"
	want := []string{"rtr-a", "rtr-b", "rtr-c", "rtr-d"}
	for i := range 5 {
		var log bytes.Buffer
		hits, err := newFleet(st, nil, &log, false, false).find(
			context.Background(), mustScope(t, st), search.Parse(flagSearch))
		if err != nil {
			t.Fatalf("find() error = %v", err)
		}
		got := names(hits)
		if len(got) != len(want) {
			t.Fatalf("run %d matched %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d matched %v, want %v in name order", i, got, want)
			}
		}
	}
}

func TestWhichTenantsABrowserSignInWouldRescue(t *testing.T) {
	interactive := fmt.Errorf("%w: no existing browser session", saseauth.ErrInteractiveRequired)

	tests := []struct {
		name string
		l    loaded
		want bool
	}{
		{"nothing wrong", loaded{note: "refreshed"}, false},
		{"no credential at all", loaded{err: interactive}, true},
		{
			name: "answered, but only from a stale cache",
			l:    loaded{els: []api.Element{{ID: "e1"}}, stale: interactive},
			want: true,
		},
		{"unreachable for some other reason", loaded{err: errors.New("dial tcp: refused")}, false},
		{"stale for some other reason", loaded{stale: errors.New("dial tcp: refused")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.l.wantsSignIn(); got != tt.want {
				t.Errorf("wantsSignIn() = %v, want %v", got, tt.want)
			}
		})
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

	var log bytes.Buffer
	got := newFleet(st, nil, &log, false, false).load(context.Background(), mustScope(t, st))
	if len(got) != 1 {
		t.Fatalf("loaded %d tenants, want 1", len(got))
	}
	if got[0].err != nil {
		t.Errorf("err = %v, want the stale cache to still answer", got[0].err)
	}
	if got[0].stale == nil {
		t.Error("stale = nil, want the refresh failure recorded behind the stale answer")
	}
	if len(got[0].els) != 1 {
		t.Errorf("loaded %d elements, want the cached one", len(got[0].els))
	}
}

func TestRenewAsksBeforeOpeningABrowser(t *testing.T) {
	st := searchStore(t)
	tenants := mustScope(t, st)

	tests := []struct {
		name   string
		answer string
		want   string
	}{
		{"declined", "n\n", "expired"},
		{"nothing typed", "\n", "expired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log bytes.Buffer
			fl := newFleet(st, strings.NewReader(tt.answer), &log, false, true)
			if fl.renew(context.Background(), tenants) {
				t.Error("renew() = true after a refusal, want nothing reloaded")
			}
			out := log.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("the question does not say what is wrong:\n%s", out)
			}
			if !strings.Contains(out, "[y/N]") {
				t.Errorf("no question was put at all:\n%s", out)
			}
			for _, name := range []string{"Tractor Supply", "Niagara Bottling"} {
				if !strings.Contains(out, name) {
					t.Errorf("the question does not name %q:\n%s", name, out)
				}
			}
		})
	}
}

func TestAScriptIsToldWhatToRunRatherThanAsked(t *testing.T) {
	st := searchStore(t)
	var log bytes.Buffer
	fl := newFleet(st, nil, &log, false, false)
	if fl.renew(context.Background(), mustScope(t, st)) {
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

func TestTenantsNeedingACredentialAreNamedWithTheWayOut(t *testing.T) {
	st := searchStore(t)
	if err := st.SaveConfig(&config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Added: true, Auth: config.AuthBrowser},
	}}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	seedCache(t, st, "111", time.Now().Add(-2*config.CacheFresh),
		[]api.Element{{ID: "e1", Name: "s0100-rtr-01"}}, nil)

	flagSearch = "s0100"
	var log bytes.Buffer
	hits, err := newFleet(st, nil, &log, false, false).find(
		context.Background(), mustScope(t, st), search.Parse(flagSearch))
	if err != nil {
		t.Fatalf("find() error = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("matched %d, want the stale cache to still answer", len(hits))
	}
	if out := log.String(); !strings.Contains(out, "iontk auth login") {
		t.Errorf("the log does not name the way out:\n%s", out)
	}
}
