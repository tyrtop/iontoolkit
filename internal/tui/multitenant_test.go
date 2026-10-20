package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

func multiModel(t *testing.T) tuiModel {
	t.Helper()
	m := discoverModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Auth: config.AuthBrowser, Added: true},
		{Name: "Niagara Bottling", TSGID: "222", Auth: config.AuthBrowser, Added: true},
	}}
	m.tenant, m.hasTenant = m.cfg.Tenants[0], true
	m.selected = map[string]bool{}
	m.filter = newField("", "", false).input
	m.table = newElementTable()
	m.pool = newClientPool()
	m.ctx = context.Background()
	return m
}

func TestElementsFromSeveralTenantsAreOneList(t *testing.T) {
	m := multiModel(t)

	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "a", Name: "s0100-rtr-01"},
		{ID: "b", Name: "s0100-rtr-02"},
	})
	if len(m.all) != 2 {
		t.Fatalf("got %d elements, want 2", len(m.all))
	}
	if m.showTenantColumn() {
		t.Error("the tenant column showed with only one tenant loaded")
	}

	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{
		{ID: "c", Name: "ord-gw-01"},
	})
	if len(m.all) != 3 {
		t.Fatalf("got %d elements, want 3 across both tenants", len(m.all))
	}
	if !m.showTenantColumn() {
		t.Error("the tenant column did not show with two tenants loaded")
	}

	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "a", Name: "s0100-rtr-01"},
	})
	if len(m.all) != 2 {
		t.Fatalf("got %d elements, want 2 after one tenant shrank", len(m.all))
	}
	var tenants []string
	for _, r := range m.all {
		tenants = append(tenants, r.Tenant)
	}
	if !strings.Contains(strings.Join(tenants, ","), "Niagara") {
		t.Errorf("the other tenant's elements were dropped: %v", tenants)
	}
}

func TestSameElementIDInTwoTenantsStaysDistinct(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "same", Name: "a"}})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "same", Name: "b"}})

	if len(m.all) != 2 {
		t.Fatalf("got %d elements, want both kept", len(m.all))
	}
	keys := map[string]bool{}
	for _, r := range m.all {
		if keys[r.key()] {
			t.Errorf("two elements share the key %q", r.key())
		}
		keys[r.key()] = true
	}

	m.selected[m.all[0].key()] = true
	if len(m.selected) != 1 {
		t.Fatal("selection did not record")
	}
	if m.selected[m.all[1].key()] {
		t.Error("selecting one tenant's element also selected the other's")
	}
}

func TestEachElementDialsWithItsOwnTenantsClient(t *testing.T) {
	m := multiModel(t)
	first := liveClient()
	second := liveClient()
	m.pool.set("111", first)
	m.pool.set("222", second)

	rows := []elementRow{
		{Element: api.Element{ID: "a"}, TSG: "111", Tenant: "Tractor Supply"},
		{Element: api.Element{ID: "c"}, TSG: "222", Tenant: "Niagara Bottling"},
		{Element: api.Element{ID: "x"}, TSG: "333", Tenant: "Unloaded"},
	}
	got := m.targetsFor(rows)
	if len(got) != 3 {
		t.Fatalf("got %d targets, want 3", len(got))
	}
	if got[0].Client != first {
		t.Error("the first element did not get its own tenant's client")
	}
	if got[1].Client != second {
		t.Error("the second element did not get its own tenant's client")
	}
	if got[2].Client != nil {
		t.Error("an unloaded tenant handed out a client")
	}
}

func TestDeviceCredentialsComeFromTheElementsTenant(t *testing.T) {
	m := multiModel(t)
	m.cfg.Tenants[0].Users = []config.IONUser{{Name: "tsc-ops", Default: true}}
	m.cfg.Tenants[1].Users = []config.IONUser{{Name: "nb-ops", Default: true}}
	if err := m.store.Secrets.Set(config.IONPassKey("111", "tsc-ops"), "pass-111"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := m.store.Secrets.Set(config.IONPassKey("222", "nb-ops"), "pass-222"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := m.credsFor("111")
	if a.User != "tsc-ops" || a.Pass != "pass-111" {
		t.Errorf("creds for 111 = %+v, want that tenant's own login", a)
	}
	b := m.credsFor("222")
	if b.User != "nb-ops" || b.Pass != "pass-222" {
		t.Errorf("creds for 222 = %+v, want that tenant's own login", b)
	}
	if a.Pass == b.Pass {
		t.Error("both tenants were given the same device password")
	}
	if got := m.credsFor("999"); got.User != "" || got.Pass != "" {
		t.Errorf("creds for an unknown tenant = %+v, want empty", got)
	}
}

func TestLoadFromCacheAggregatesActiveTenantsOnly(t *testing.T) {
	m := multiModel(t)
	m.cfg.Tenants = append(m.cfg.Tenants, config.Tenant{
		Name: "Excluded Co", TSGID: "333", Auth: config.AuthBrowser, Added: false,
	})

	for _, seed := range []struct {
		tsg string
		n   int
		age time.Duration
	}{{"111", 2, 3 * time.Minute}, {"222", 1, 9 * time.Minute}, {"333", 5, time.Minute}} {
		els := make([]api.Element, seed.n)
		for i := range els {
			els[i] = api.Element{ID: seed.tsg + "-" + string(rune('a'+i)), Name: "e"}
		}
		if err := m.store.SaveCache(&config.ElementCache{
			TSGID: seed.tsg, FetchedAt: time.Now().Add(-seed.age), Elements: els,
		}); err != nil {
			t.Fatalf("SaveCache(%s) error = %v", seed.tsg, err)
		}
	}

	m.loadFromCache()
	if len(m.all) != 3 {
		t.Fatalf("loaded %d elements, want 3 from the two active tenants", len(m.all))
	}
	for _, r := range m.all {
		if r.TSG == "333" {
			t.Error("an inactive tenant's elements were loaded")
		}
	}
	if m.cacheState != cacheCached {
		t.Errorf("cacheState = %v, want cacheCached", m.cacheState)
	}
	if age := m.cacheAgeNow(); age < 8*time.Minute {
		t.Errorf("cache age = %v, want the oldest tenant's age", age)
	}
}

func TestLoadFromCacheKeepsSiteNames(t *testing.T) {
	m := multiModel(t)
	if err := m.store.SaveCache(&config.ElementCache{
		TSGID: "111", FetchedAt: time.Now().Add(-time.Minute),
		Elements: []api.Element{{ID: "e1", Name: "s0100-rtr-01", SiteID: "s1"}},
		Sites:    map[string]string{"111/s1": "Store 0100"},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	m.loadFromCache()
	if len(m.all) != 1 {
		t.Fatalf("loaded %d elements, want 1", len(m.all))
	}
	if got := m.siteName(m.all[0]); got != "Store 0100" {
		t.Errorf("siteName() = %q, want the cached name rather than the raw id", got)
	}
	m.filter.SetValue("store 0100")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Errorf("matched %d, want the cached site name to be searchable", len(m.matches))
	}
}

func TestLoadFromCacheMarksAStaleAggregateAsOwed(t *testing.T) {
	m := multiModel(t)
	if err := m.store.SaveCache(&config.ElementCache{
		TSGID: "111", FetchedAt: time.Now().Add(-2 * time.Minute),
		Elements: []api.Element{{ID: "a"}},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	if err := m.store.SaveCache(&config.ElementCache{
		TSGID: "222", FetchedAt: time.Now().Add(-40 * time.Minute),
		Elements: []api.Element{{ID: "c"}},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	m.loadFromCache()
	if m.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want a refresh owed when any tenant is stale", m.cacheState)
	}
}

func TestRefreshSkipsTenantsWithNoCredentialRatherThanFailing(t *testing.T) {
	m := multiModel(t)
	m.cfg.Tenants[1].TSGID = ""
	m.spinner = ui.NewSpinner("")

	mm, cmd := m.refreshActive()
	got := mm.(tuiModel)
	if cmd == nil {
		t.Fatal("no command returned, want the usable tenant still fetched")
	}
	if got.pendingTenants != 1 {
		t.Errorf("pendingTenants = %d, want only the usable tenant counted", got.pendingTenants)
	}
	if !strings.Contains(got.status, "Niagara") {
		t.Errorf("status = %q, want the skipped tenant named", got.status)
	}
}

func TestRefreshWithNothingUsableSaysSo(t *testing.T) {
	m := multiModel(t)
	m.cfg.Tenants[0].TSGID = ""
	m.cfg.Tenants[1].TSGID = ""
	m.hasTenant = false

	mm, cmd := m.refreshActive()
	got := mm.(tuiModel)
	if cmd != nil {
		t.Error("a command was issued with no usable tenant")
	}
	if got.cacheState != cacheFailed {
		t.Errorf("cacheState = %v, want cacheFailed", got.cacheState)
	}
	if got.status == "" {
		t.Error("status is empty, want the reason given")
	}
}

func TestAggregateFinishesOnlyWhenEveryTenantHasAnswered(t *testing.T) {
	m := multiModel(t)
	m.spinner = ui.NewSpinner("")
	m.pendingTenants = 2
	m.cacheState = cacheUpdating

	mm, _ := m.Update(tenantElementsMsg{
		tsg: "111", tenant: "Tractor Supply",
		elements: []api.Element{{ID: "a", Name: "s0100-rtr-01"}},
	})
	m = mm.(tuiModel)
	if m.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want still updating with one tenant outstanding", m.cacheState)
	}
	if len(m.all) != 1 {
		t.Errorf("got %d elements, want the first tenant's already shown", len(m.all))
	}

	mm, _ = m.Update(tenantElementsMsg{
		tsg: "222", tenant: "Niagara Bottling",
		elements: []api.Element{{ID: "c", Name: "ord-gw-01"}},
	})
	m = mm.(tuiModel)
	if m.cacheState != cacheUpdated {
		t.Errorf("cacheState = %v, want cacheUpdated once all tenants answered", m.cacheState)
	}
	if m.cacheCount != 2 {
		t.Errorf("cacheCount = %d, want 2", m.cacheCount)
	}
}

func TestOneTenantFailingDoesNotLoseTheOthers(t *testing.T) {
	m := multiModel(t)
	m.spinner = ui.NewSpinner("")
	m.pendingTenants = 2

	mm, _ := m.Update(tenantElementsMsg{
		tsg: "111", tenant: "Tractor Supply",
		elements: []api.Element{{ID: "a", Name: "s0100-rtr-01"}},
	})
	m = mm.(tuiModel)
	mm, _ = m.Update(tenantElementsMsg{
		tsg: "222", tenant: "Niagara Bottling",
		err: errTokenTest,
	})
	got := mm.(tuiModel)

	if len(got.all) != 1 {
		t.Errorf("got %d elements, want the healthy tenant's kept", len(got.all))
	}
	if !strings.Contains(got.status, "Niagara") {
		t.Errorf("status = %q, want the failing tenant named", got.status)
	}
	if got.cacheState != cacheUpdated {
		t.Errorf("cacheState = %v, want the partial result accepted", got.cacheState)
	}
}

func TestATenantTheAccountCannotReachIsNotCalledASignInProblem(t *testing.T) {
	m := multiModel(t)
	m.spinner = ui.NewSpinner("")
	m.pendingTenants = 2

	mm, _ := m.Update(tenantElementsMsg{
		tsg: "111", tenant: "Tractor Supply",
		elements: []api.Element{{ID: "a", Name: "s0100-rtr-01"}},
	})
	m = mm.(tuiModel)
	mm, _ = m.Update(tenantElementsMsg{
		tsg: "222", tenant: "CIS_LAB",
		err: errInteractionRequired,
	})
	got := mm.(tuiModel)

	if !strings.Contains(got.status, "not accessible") {
		t.Errorf("status = %q, want it to describe access rather than a sign-in", got.status)
	}
	if strings.Contains(got.status, "interaction_required") {
		t.Errorf("status = %q, want the raw error chain trimmed", got.status)
	}
	if got.tokenDead != "" {
		t.Error("one unreachable tenant marked the whole credential dead")
	}
	if len(got.all) != 1 {
		t.Errorf("got %d elements, want the healthy tenant's kept", len(got.all))
	}
	if got.tenantIssues["222"] == "" {
		t.Error("the problem tenant was not recorded for settings to flag")
	}

	mm, _ = got.Update(tenantElementsMsg{tsg: "222", tenant: "CIS_LAB", elements: nil})
	if cleared := mm.(tuiModel); cleared.tenantIssues["222"] != "" {
		t.Error("the issue survived a successful fetch")
	}
}

func TestSettingsFlagsAnUnreachableTenant(t *testing.T) {
	m := multiModel(t)
	m.cfg.Tenants[1].Added = true
	m.tenantIssues = map[string]string{"222": "not accessible with this sign-in"}
	m.phase = phaseSettings
	m.setSection = secTenants
	m.setFocus = focusContent
	m.width, m.height = 110, 30
	m.spinner = ui.NewSpinner("")

	out := plain(m.viewSettings())
	if !strings.Contains(out, "not accessible") {
		t.Errorf("settings does not flag the unreachable tenant:\n%s", out)
	}
	t.Log("\n" + m.viewSettings())
}

var errInteractionRequired = errInteraction{}

type errInteraction struct{}

func (errInteraction) Error() string {
	return "interactive sign-in required: no existing browser session: authorization failed: " +
		"interaction_required: The request requires some interaction that is not allowed"
}

func TestATenantRejectingTheTokenRoutesToSignIn(t *testing.T) {
	m := multiModel(t)
	m.spinner = ui.NewSpinner("")
	m.pendingTenants = 1

	mm, _ := m.Update(tenantElementsMsg{
		tsg: "222", tenant: "Niagara Bottling",
		err: errAuthTest,
	})
	got := mm.(tuiModel)
	if got.tokenDead == "" {
		t.Error("an auth failure from one tenant did not mark the credential dead")
	}
}

func TestFilterNarrowsByTenant(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a", Name: "s0100-rtr-01"}})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c", Name: "ord-gw-01"}})

	m.filter.SetValue("niagara")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Fatalf("tenant filter matched %d, want 1", len(m.matches))
	}
	if m.all[m.matches[0]].TSG != "222" {
		t.Error("the tenant filter matched the wrong tenant")
	}
}

func TestRenderCrossTenantElementList(t *testing.T) {
	m := multiModel(t)
	m.width, m.height = 110, 30
	m.spinner = ui.NewSpinner("")
	m.sites = map[string]string{"111/s1": "Site 0100", "222/s9": "ORD"}
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "a", Name: "s0100-rtr-01", SiteID: "s1", Model: "ion1200", Connected: true},
	})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{
		{ID: "c", Name: "ord-gw-01", SiteID: "s9", Model: "ion3200", Connected: true},
	})
	m.cacheState, m.cacheCount, m.cacheAt = cacheUpdated, 2, time.Now()
	m.phase = phasePicking

	out := plain(m.viewPicking())
	for _, want := range []string{"s0100-rtr-01", "ord-gw-01", "Tractor Supply", "Niagara Bottling", "tenant"} {
		if !strings.Contains(out, want) {
			t.Errorf("the element list is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewPicking())
}

var errAuthTest = errTokenAuth{}

type errTokenAuth struct{}

func (errTokenAuth) Error() string { return "status 401 Unauthorized: " }

func TestModeCycling(t *testing.T) {
	seen := map[pickMode]bool{}
	mode := modeShell
	for range pickModes {
		seen[mode] = true
		mode = mode.next()
	}
	if mode != modeShell {
		t.Errorf("cycling ended at %v, want to wrap to shell", mode)
	}
	if len(seen) != len(pickModes) {
		t.Errorf("visited %d modes, want all %d", len(seen), len(pickModes))
	}
	if got := pickMode(99).next(); got != modeShell {
		t.Errorf("next() on an unknown mode = %v, want shell", got)
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want pickMode
		ok   bool
	}{
		{"shell", modeShell, true},
		{"batch", modeBatch, true},
		{"", modeShell, false},
		{"portal", modeShell, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseMode(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseMode(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTheMenuOnlySwitchesTheCredentialMode(t *testing.T) {
	for _, key := range []string{"alt+m", "alt+a"} {
		t.Run(key, func(t *testing.T) {
			m := multiModel(t)
			m.phase = phaseMenu
			m.authMode = authBrowser
			m.mode = modeShell

			mm, _ := m.onMenuKey(key)
			got := mm.(tuiModel)
			if got.authMode != authTenant {
				t.Fatalf("authMode = %v, want %s to switch to service account mode", got.authMode, key)
			}
			if got.mode != modeShell {
				t.Errorf("%s changed the element list mode as well", key)
			}
			if got.cfg.DefaultMode != "" {
				t.Errorf("DefaultMode = %q, want the element list's saved default left alone", got.cfg.DefaultMode)
			}
			saved, err := got.store.Config()
			if err != nil {
				t.Fatalf("Config() error = %v", err)
			}
			if saved.AuthMode != "tenant" {
				t.Errorf("saved AuthMode = %q, want it persisted", saved.AuthMode)
			}

			mm, _ = got.onMenuKey(key)
			if back := mm.(tuiModel); back.authMode != authBrowser {
				t.Errorf("authMode = %v, want %s to cycle back", back.authMode, key)
			}
		})
	}
}

func TestDefaultModeSettingPersistsAndApplies(t *testing.T) {
	m := multiModel(t)
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.setCursor = modeRow
	m.spinner = ui.NewSpinner("")

	if got := m.defaultMode(); got != modeShell {
		t.Errorf("defaultMode() = %v, want shell with nothing saved", got)
	}

	mm, _ := m.onGeneralKey("enter")
	got := mm.(tuiModel)
	if got.cfg.DefaultMode != "batch" {
		t.Errorf("DefaultMode = %q, want it cycled and saved", got.cfg.DefaultMode)
	}
	if got.mode != modeBatch {
		t.Error("changing the default did not take effect now as well")
	}
	saved, err := got.store.Config()
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if saved.DefaultMode != "batch" {
		t.Errorf("saved DefaultMode = %q, want it persisted", saved.DefaultMode)
	}
}

func TestSettingsChecklistControlsTheAggregate(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a", Name: "s0100-rtr-01"}})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c", Name: "ord-gw-01"}})
	m.phase = phaseSettings
	m.setSection = secTenants
	m.setFocus = focusContent
	m.setCursor = 1

	mm, _ := m.onTenantsKey("space")
	got := mm.(tuiModel)

	if got.cfg.Tenants[1].Added {
		t.Error("space did not exclude the tenant")
	}
	if len(got.all) != 1 {
		t.Errorf("got %d elements, want the excluded tenant's dropped", len(got.all))
	}
	for _, r := range got.all {
		if r.TSG == "222" {
			t.Error("an excluded tenant's elements are still listed")
		}
	}
	saved, err := got.store.Config()
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if saved.Tenants[1].Added {
		t.Error("the exclusion was not persisted")
	}

	mm, _ = got.onTenantsKey("space")
	back := mm.(tuiModel)
	if !back.cfg.Tenants[1].Added {
		t.Error("space did not include the tenant again")
	}
}

func TestSettingsAddsAServiceAccount(t *testing.T) {
	m := multiModel(t)
	m.phase = phaseSettings
	m.setSection = secTenants
	m.setFocus = focusContent

	mm, _ := m.onTenantsKey("a")
	got := mm.(tuiModel)
	if got.phase != phaseAddTenant {
		t.Fatalf("phase = %v, want the service account form", got.phase)
	}
	if len(got.form) != 4 {
		t.Errorf("form has %d fields, want name, tsg, client id and secret", len(got.form))
	}
	if got.formAuth != config.AuthServiceAccount {
		t.Errorf("formAuth = %q, want %q", got.formAuth, config.AuthServiceAccount)
	}
}

func TestUsingATenantIncludesItInTheAggregate(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if len(got.cfg.Tenants) != 1 {
		t.Fatalf("saved %d tenants, want 1", len(got.cfg.Tenants))
	}
	if !got.cfg.Tenants[0].Added {
		t.Error("choosing a tenant did not include it in the element list")
	}
}

func TestClientPoolIsSharedAcrossModelCopies(t *testing.T) {
	m := multiModel(t)
	c := liveClient()
	m.pool.set("111", c)

	copied := m
	if copied.pool.get("111") != c {
		t.Error("a copied model lost the pool's clients")
	}
	copied.pool.set("222", liveClient())
	if m.pool.get("222") == nil {
		t.Error("a client added through a copy was not visible to the original")
	}
	if m.pool.count() != 2 {
		t.Errorf("pool holds %d clients, want 2", m.pool.count())
	}

	m.pool.drop("111")
	if copied.pool.get("111") != nil {
		t.Error("dropping a client was not visible to the copy")
	}

	var nilPool *clientPool
	if nilPool.get("x") != nil || nilPool.count() != 0 {
		t.Error("a nil pool should behave as empty")
	}
	nilPool.set("x", c)
	nilPool.drop("x")
}

func TestTheTenantColumnCanAppearAndDisappear(t *testing.T) {
	m := multiModel(t)
	m.width, m.height = 110, 30

	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a", Name: "one"}})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c", Name: "two"}})
	if !m.showTenantColumn() {
		t.Fatal("two tenants did not produce a tenant column")
	}

	m.mergeTenantElements("222", "Niagara Bottling", nil)
	if m.showTenantColumn() {
		t.Error("the tenant column stayed after the second tenant emptied")
	}

	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c", Name: "two"}})
	if !m.showTenantColumn() {
		t.Error("the tenant column did not come back")
	}
}

func TestMenuSecondEntryFollowsTheCredentialMode(t *testing.T) {
	tests := []struct {
		mode   authMode
		want   string
		absent string
	}{
		{authBrowser, "Sign In with Browser", "Service Accounts"},
		{authTenant, "Service Accounts", "browser"},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			for _, listMode := range pickModes {
				m := multiModel(t)
				m.authMode = tt.mode
				m.mode = listMode

				joined := strings.Join(menuLabels(m), " | ")
				if !strings.Contains(joined, tt.want) {
					t.Errorf("%v/%v is missing %q: %s", tt.mode, listMode, tt.want, joined)
				}
				if strings.Contains(joined, tt.absent) {
					t.Errorf("%v/%v still offers %q: %s", tt.mode, listMode, tt.absent, joined)
				}
			}
		})
	}
}

func TestTenantSelectOpensTheTenantList(t *testing.T) {
	m := multiModel(t)
	m.authMode = authTenant
	m.phase = phaseMenu
	for i, e := range m.menuEntries() {
		if e.action == actSwitch {
			m.menuCursor = i
		}
	}

	mm, _ := m.onMenuKey("enter")
	got := mm.(tuiModel)
	if got.phase != phaseSwitch {
		t.Errorf("phase = %v, want the tenant list", got.phase)
	}
}

func TestScopeLabelDescribesWhatIsLoaded(t *testing.T) {
	m := multiModel(t)
	if got := m.scopeLabel(); got != "Tractor Supply" {
		t.Errorf("scopeLabel() = %q, want the current tenant with nothing loaded", got)
	}

	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a"}})
	if got := m.scopeLabel(); got != "Tractor Supply" {
		t.Errorf("scopeLabel() = %q, want the single tenant named", got)
	}

	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c"}})
	if got := m.scopeLabel(); got != "2 tenants" {
		t.Errorf("scopeLabel() = %q, want a count rather than one tenant's name", got)
	}

	bare := tuiModel{}
	if got := bare.scopeLabel(); got != "no tenant" {
		t.Errorf("scopeLabel() = %q with nothing at all, want %q", got, "no tenant")
	}
}

func TestWorkingSetPickerStartsFromWhatIsAlreadyAdded(t *testing.T) {
	m := discoverModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "CIS root", TSGID: "1643707006", Auth: config.AuthBrowser, Added: true},
		{Name: "Tractor Supply Co.", TSGID: "1984021017", Auth: config.AuthBrowser},
	}}
	m.discovered = discoveredTenants

	next, _ := m.startTenantPick()
	if !next.picked["1643707006"] {
		t.Error("a tenant already in the set did not start ticked, so re-listing would drop it")
	}
	if next.picked["1984021017"] {
		t.Error("a tenant with a record but not added started ticked")
	}
	if next.phase != phaseTenantPick {
		t.Errorf("phase = %v, want the picker", next.phase)
	}
}

func TestPickingTheWorkingSet(t *testing.T) {
	m := discoverModel(t)
	m.discovered = discoveredTenants
	m.spinner = ui.NewSpinner("")
	next, _ := m.startTenantPick()
	m = next

	mm, _ := m.onTenantPickKey("space")
	m = mm.(tuiModel)
	m.pickCursor = 2
	mm, _ = m.onTenantPickKey("space")
	m = mm.(tuiModel)
	if len(m.picked) != 2 {
		t.Fatalf("picked %d, want 2", len(m.picked))
	}

	mm, _ = m.onTenantPickKey("enter")
	got := mm.(tuiModel)

	added := got.cfg.AddedTenants()
	if len(added) != 2 {
		t.Fatalf("working set has %d tenants, want 2: %+v", len(added), got.cfg.Tenants)
	}
	ids := map[string]bool{}
	for _, t := range added {
		ids[t.TSGID] = true
	}
	if !ids["1643707006"] || !ids["1984021017"] {
		t.Errorf("working set = %+v, want the two ticked tenants", added)
	}
	if got.cfg.IsAdded("1538107912") {
		t.Error("an unticked tenant ended up in the set")
	}
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want the menu after picking", got.phase)
	}
	if got.picked != nil || got.discovered != nil {
		t.Error("picker state was not cleared")
	}

	saved, err := got.store.Config()
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if len(saved.AddedTenants()) != 2 {
		t.Error("the working set was not persisted")
	}
}

func TestPickerRefusesAnEmptySet(t *testing.T) {
	m := discoverModel(t)
	m.discovered = discoveredTenants
	next, _ := m.startTenantPick()

	mm, _ := next.onTenantPickKey("enter")
	got := mm.(tuiModel)
	if got.phase != phaseTenantPick {
		t.Error("an empty pick was accepted")
	}
	if got.status == "" {
		t.Error("no explanation for refusing an empty pick")
	}
}

func TestPickerSelectAllToggles(t *testing.T) {
	m := discoverModel(t)
	m.discovered = discoveredTenants
	next, _ := m.startTenantPick()

	mm, _ := next.onTenantPickKey("ctrl+a")
	all := mm.(tuiModel)
	if len(all.picked) != 3 {
		t.Fatalf("picked %d, want all 3", len(all.picked))
	}
	mm, _ = all.onTenantPickKey("ctrl+a")
	if none := mm.(tuiModel); len(none.picked) != 0 {
		t.Errorf("picked %d, want ctrl+a to clear when everything is ticked", len(none.picked))
	}
}

func TestSearchSpansTheWholeWorkingSet(t *testing.T) {
	m := multiModel(t)
	m.authMode = authBrowser
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "a", Name: "s0100-rtr-01"},
		{ID: "b", Name: "s0200-rtr-01"},
	})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{
		{ID: "c", Name: "s0100-gw-01"},
	})

	m.filter.SetValue("s0100")
	m.applyFilter()
	if len(m.matches) != 2 {
		t.Fatalf("filter matched %d, want 2 across both tenants", len(m.matches))
	}
	tenants := map[string]bool{}
	for _, i := range m.matches {
		tenants[m.all[i].TSG] = true
	}
	if len(tenants) != 2 {
		t.Errorf("matches came from %d tenants, want both", len(tenants))
	}
}

func TestAggregateSpansTheWorkingSetNotJustTheCurrentTenant(t *testing.T) {
	m := multiModel(t)
	m.authMode = authBrowser
	m.cfg.Tenants[0].Added = true
	m.cfg.Tenants[1].Added = true
	m.tenant = m.cfg.Tenants[0]

	if got := m.aggregateTenants(); len(got) != 2 {
		t.Errorf("aggregate has %d tenants, want the whole working set: %+v", len(got), got)
	}

	m.authMode = authTenant
	if got := m.aggregateTenants(); len(got) != 1 {
		t.Errorf("tenant mode aggregate has %d tenants, want 1", len(got))
	}
}

func TestRefreshNoteGivesFeedback(t *testing.T) {
	m := multiModel(t)
	m.spinner = ui.NewSpinner("")

	m.cacheState = cacheUpdated
	if got := plain(m.refreshNote()); !strings.Contains(got, "Elements refreshed!") {
		t.Errorf("refreshNote() = %q, want it to say the elements are fresh", got)
	}
	if got := plain(m.refreshNote()); !strings.Contains(got, "last updated") {
		t.Errorf("refreshNote() = %q, want it to say how long ago", got)
	}

	if n := strings.Count(m.refreshNote(), "\n"); n != 1 {
		t.Errorf("refreshNote() has %d newlines, want the two notes stacked", n)
	}

	m.cacheState = cacheUpdating
	note := plain(m.refreshNote())
	if !strings.Contains(note, "refreshing elements") {
		t.Errorf("refreshNote() = %q, want it to name the element refresh", note)
	}
	m.haInFlight = map[string]bool{"1": true}
	m.haTotal, m.haDone = 312, 48
	if got := plain(m.refreshNote()); !strings.Contains(got, "refreshing HA 48/312 clusters") {
		t.Errorf("refreshNote() = %q, want the HA sweep named and counted", got)
	}
	m.haInFlight = nil
	m.haAt = time.Now()
	if got := plain(m.refreshNote()); strings.Contains(got, "refreshing HA") {
		t.Errorf("refreshNote() = %q, want the sweep's note gone", got)
	}
	if got := plain(m.refreshNote()); !strings.Contains(got, "HA refreshed!") {
		t.Errorf("refreshNote() = %q, want it to say the sweep finished", got)
	}
	m.haTotal, m.haDone, m.haAt = 0, 0, time.Time{}
	m.cacheState = cacheUpdating

	m.pendingTenants = 3
	if got := plain(m.refreshNote()); !strings.Contains(got, "3 tenants") {
		t.Errorf("refreshNote() = %q, want the outstanding tenant count", got)
	}

	m.cacheState = cacheFailed
	if got := plain(m.refreshNote()); !strings.Contains(got, "failed") {
		t.Errorf("refreshNote() = %q, want a failure reported", got)
	}
}

func TestTenantRowsDoNotShiftWithSelection(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m,
		config.KnownTenant{TSGID: "1643707006", Name: "CIS root"},
		config.KnownTenant{TSGID: "1538107912", Name: "NIAGARA BOTTLING, LLC-145595"},
	)
	m.width, m.height = 100, 30
	m.phase = phaseSwitch

	columnOf := func(out string, id string) int {
		for _, line := range strings.Split(out, "\n") {
			if i := strings.Index(line, id); i >= 0 {
				return i
			}
		}
		return -1
	}

	first := plain(m.viewSwitch())
	a1, b1 := columnOf(first, "1643707006"), columnOf(first, "1538107912")
	if a1 < 0 || b1 < 0 {
		t.Fatalf("ids missing from the list:\n%s", first)
	}
	if a1 != b1 {
		t.Errorf("ids start at columns %d and %d, want them aligned:\n%s", a1, b1, first)
	}

	m.switchCursor = 1
	second := plain(m.viewSwitch())
	if a2, b2 := columnOf(second, "1643707006"), columnOf(second, "1538107912"); a2 != a1 || b2 != b1 {
		t.Errorf("ids moved when the cursor did: %d,%d then %d,%d\n%s", a1, b1, a2, b2, second)
	}
}

func TestRenderWorkingSetPicker(t *testing.T) {
	m := discoverModel(t)
	m.discovered = discoveredTenants
	m.ident = saseauth.Identity{Name: "Ben Hall", Email: "ben@example.test"}
	m.width, m.height = 100, 30
	next, _ := m.startTenantPick()
	next.picked["1984021017"] = true
	next.pickCursor = 1

	out := plain(next.viewTenantPick())
	for _, want := range []string{
		"signed in as Ben Hall <ben@example.test>",
		"searching spans all of them",
		"CIS root", "1643707006",
		"[x]", "[ ]",
		"1 picked", "space pick", "ctrl+a all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + next.viewTenantPick())
}

func credsModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.cfg.Tenants[0].Users = []config.IONUser{{Name: "tsc-ops", Default: true}, {Name: "tsc-ro"}}
	m.cfg.Tenants[1].Users = []config.IONUser{{Name: "nb-ops", Default: true}}
	for _, seed := range []struct{ tsg, user, pass string }{
		{"111", "tsc-ops", "pw-tsc-ops"},
		{"111", "tsc-ro", "pw-tsc-ro"},
		{"222", "nb-ops", "pw-nb"},
	} {
		if err := m.store.Secrets.Set(config.IONPassKey(seed.tsg, seed.user), seed.pass); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a", Name: "s0100-rtr-01"}})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c", Name: "ord-gw-01"}})
	return m
}

func TestRunAsIsChosenPerTenant(t *testing.T) {
	m := credsModel(t)

	if got := m.runAsFor("111"); got != "tsc-ops" {
		t.Errorf("runAsFor(111) = %q, want the default", got)
	}
	if got := m.runAsFor("222"); got != "nb-ops" {
		t.Errorf("runAsFor(222) = %q, want the default", got)
	}

	m.setRunAs("111", "tsc-ro")
	if got := m.runAsFor("111"); got != "tsc-ro" {
		t.Errorf("runAsFor(111) = %q, want the chosen user", got)
	}
	if got := m.runAsFor("222"); got != "nb-ops" {
		t.Errorf("runAsFor(222) = %q, want it unchanged", got)
	}

	a := m.credsFor("111")
	if a.User != "tsc-ro" || a.Pass != "pw-tsc-ro" {
		t.Errorf("credsFor(111) = %+v, want the chosen user's pair", a)
	}
	b := m.credsFor("222")
	if b.User != "nb-ops" || b.Pass != "pw-nb" {
		t.Errorf("credsFor(222) = %+v, want its own pair", b)
	}

	m.setRunAs("111", "")
	if got := m.runAsFor("111"); got != "tsc-ops" {
		t.Errorf("runAsFor(111) = %q, want the default back", got)
	}
}

func TestUsersPanelOpensOnTheHighlightedElementsTenant(t *testing.T) {
	m := credsModel(t)
	m.phase = phasePicking
	m.tenant = m.cfg.Tenants[0]

	for i, idx := range m.matches {
		if m.all[idx].TSG == "222" {
			m.table.SetCursor(i)
		}
	}

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModAlt}), "alt+u")
	got := mm.(tuiModel)
	if got.phase != phaseUsers {
		t.Fatalf("phase = %v, want the users panel", got.phase)
	}
	if got.usersTSG != "222" {
		t.Errorf("usersTSG = %q, want the highlighted element's tenant", got.usersTSG)
	}
	if name := got.usersPanelTenant().Name; name != "Niagara Bottling" {
		t.Errorf("panel tenant = %q, want the highlighted element's", name)
	}
}

func TestUsersPanelCyclesTenants(t *testing.T) {
	m := credsModel(t)
	m.phase = phaseUsers
	m.usersTSG = "111"

	if n := len(m.usersPanelTenants()); n != 2 {
		t.Fatalf("panel offers %d tenants, want 2", n)
	}

	mm, _ := m.onUsersKey("right")
	fwd := mm.(tuiModel)
	if fwd.usersTSG == "111" {
		t.Error("right did not move to another tenant")
	}
	mm, _ = fwd.onUsersKey("left")
	if back := mm.(tuiModel); back.usersTSG != "111" {
		t.Errorf("usersTSG = %q, want left to come back", back.usersTSG)
	}
}

func TestUsersPanelActsOnItsOwnTenant(t *testing.T) {
	m := credsModel(t)
	m.phase = phaseUsers
	m.usersTSG = "222"
	m.tenant = m.cfg.Tenants[0]
	m.ionUser = "tsc-ops"

	mm, _ := m.onUsersKey("enter")
	got := mm.(tuiModel)
	if got.runAsFor("222") != "nb-ops" {
		t.Errorf("runAsFor(222) = %q, want nb-ops", got.runAsFor("222"))
	}
	if got.ionUser != "tsc-ops" {
		t.Errorf("ionUser = %q, want the browsed tenant's user untouched", got.ionUser)
	}
	if !strings.Contains(got.status, "Niagara Bottling") {
		t.Errorf("status = %q, want the tenant named", got.status)
	}

	mm, _ = m.onUsersKey("p")
	pw := mm.(tuiModel)
	if pw.formTenantName != "Niagara Bottling" {
		t.Errorf("formTenantName = %q, want the panel's tenant", pw.formTenantName)
	}
	if pw.formUserName != "nb-ops" {
		t.Errorf("formUserName = %q, want that tenant's user", pw.formUserName)
	}
}

func TestPasswordPromptAsksForEveryTenantInTheSelection(t *testing.T) {
	m := credsModel(t)
	m.phase = phasePicking
	m.mode = modeShell
	setTestClient(&m, liveClient())
	m.pool.set("222", liveClient())

	if err := m.store.Secrets.Delete(config.IONPassKey("222", "nb-ops")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	mm, _ := m.onPickEnter()
	got := mm.(tuiModel)
	if got.phase != phasePassword {
		t.Fatalf("phase = %v, want the credential prompt", got.phase)
	}
	if got.formTenantName != "Niagara Bottling" {
		t.Errorf("formTenantName = %q, want the tenant that is actually missing one", got.formTenantName)
	}
	if got.formTenantTSG != "222" {
		t.Errorf("formTenantTSG = %q, want 222", got.formTenantTSG)
	}
	if !strings.Contains(got.formTitle, "Niagara Bottling") {
		t.Errorf("title = %q, want the right tenant named", got.formTitle)
	}
}

func TestPasswordIsSavedAgainstTheTenantThatWasAsked(t *testing.T) {
	m := credsModel(t)
	m.tenant = m.cfg.Tenants[0]
	m.ionUser = "tsc-ops"

	target := m.cfg.Tenants[1]
	mm, _ := m.openPasswordForm(target, "nb-ops")
	m = mm.(tuiModel)
	m.form[0].input.SetValue("brand-new")

	mm, _ = m.submitForm()
	got := mm.(tuiModel)

	if v, err := got.store.Secrets.Get(config.IONPassKey("222", "nb-ops")); err != nil || v != "brand-new" {
		t.Errorf("password for 222 = %q (err %v), want it saved there", v, err)
	}
	if v, _ := got.store.Secrets.Get(config.IONPassKey("111", "nb-ops")); v != "" {
		t.Errorf("password leaked into the browsed tenant: %q", v)
	}
	if got.tenant.Name != "Tractor Supply" {
		t.Errorf("tenant = %q, want the browsed tenant unchanged", got.tenant.Name)
	}
	if got.ionUser != "tsc-ops" {
		t.Errorf("ionUser = %q, want it unchanged", got.ionUser)
	}
}

func TestSessionsUseEachTenantsOwnLogin(t *testing.T) {
	m := credsModel(t)
	m.setRunAs("111", "tsc-ro")

	a := m.credsFor("111")
	b := m.credsFor("222")
	if a.User == b.User || a.Pass == b.Pass {
		t.Errorf("both tenants resolved to the same login: %+v and %+v", a, b)
	}
	if a.Pass != "pw-tsc-ro" {
		t.Errorf("credsFor(111).Pass = %q, want the chosen user's", a.Pass)
	}
}

func TestRenderPerTenantUsersPanel(t *testing.T) {
	m := credsModel(t)
	m.phase = phaseUsers
	m.usersTSG = "222"
	m.width, m.height = 100, 30
	m.spinner = ui.NewSpinner("")

	out := plain(m.viewUsers())
	for _, want := range []string{"ion users", "Niagara Bottling", "of 2 tenants", "nb-ops", "←→ tenant"} {
		if !strings.Contains(out, want) {
			t.Errorf("the users panel is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tsc-ops") {
		t.Errorf("the panel shows another tenant's users:\n%s", out)
	}
	t.Log("\n" + m.viewUsers())
}

func TestAltWClosesWhatYouAreIn(t *testing.T) {
	t.Run("on the element list it returns to the menu", func(t *testing.T) {
		m := credsModel(t)
		m.phase = phasePicking
		m.spinner = ui.NewSpinner("")

		mm, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModAlt}), "alt+w")
		got := mm.(tuiModel)
		if got.phase != phaseMenu {
			t.Errorf("phase = %v, want the menu", got.phase)
		}
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Error("alt+w on the element list quit the app")
			}
		}
	})

	t.Run("sessions survive leaving the element list", func(t *testing.T) {
		m := credsModel(t)
		m.phase = phasePicking
		m.sessions = ionterm.NewManager()

		mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModAlt}), "alt+w")
		got := mm.(tuiModel)
		if got.sessions == nil {
			t.Error("the session manager was discarded")
		}
	})

	t.Run("on the menu it quits", func(t *testing.T) {
		m := credsModel(t)
		m.phase = phaseMenu

		_, cmd := m.onMenuKey("alt+w")
		if cmd == nil {
			t.Fatal("alt+w on the menu returned no command")
		}
		if _, quit := cmd().(tea.QuitMsg); !quit {
			t.Errorf("alt+w on the menu produced %T, want a quit", cmd())
		}
	})

	t.Run("the element list advertises it as the way back", func(t *testing.T) {
		m := credsModel(t)
		m.phase = phasePicking
		m.width, m.height = 200, 40
		m.spinner = ui.NewSpinner("")

		out := plain(m.viewPicking())
		if !strings.Contains(out, "alt+w back") {
			t.Errorf("the element list does not advertise alt+w as the way back:\n%s", out)
		}
		if strings.Contains(out, "alt+w quit") {
			t.Errorf("the element list still offers alt+w as quit:\n%s", out)
		}
	})
}

func TestAddingAUserToATenantWithNoRecordYet(t *testing.T) {
	m := multiModel(t)
	m.cfg = &config.Config{}
	m.mergeTenantElements("333", "Synovus", []api.Element{{ID: "s1", Name: "syn-rtr-01"}})
	m.phase = phaseUsers
	m.usersTSG = "333"

	if _, ok := m.tenantOf("333"); ok {
		t.Fatal("the tenant should have no record for this test to mean anything")
	}
	if name := m.usersPanelTenant().Name; name != "Synovus" {
		t.Fatalf("panel tenant = %q, want the discovered name", name)
	}

	mm, _ := m.onUsersKey("a")
	form := mm.(tuiModel)
	if form.phase != phaseAddUser {
		t.Fatalf("phase = %v, want the add-user form", form.phase)
	}
	if form.formTenantTSG != "333" {
		t.Errorf("formTenantTSG = %q, want the tenant id carried into the form", form.formTenantTSG)
	}

	form.form[0].input.SetValue("syn-ops")
	form.form[1].input.SetValue("syn-pass")
	mm, _ = form.submitForm()
	got := mm.(tuiModel)

	if got.status != "" {
		t.Fatalf("status = %q, want the save to succeed", got.status)
	}
	saved, ok := got.cfg.Tenant("Synovus")
	if !ok {
		t.Fatal("the tenant record was not created")
	}
	if saved.TSGID != "333" {
		t.Errorf("TSGID = %q, want 333", saved.TSGID)
	}
	if !saved.Added {
		t.Error("giving a tenant a login did not put it in the working set")
	}
	if saved.DefaultUser() != "syn-ops" {
		t.Errorf("DefaultUser() = %q, want the new user", saved.DefaultUser())
	}
	if v, err := got.store.Secrets.Get(config.IONPassKey("333", "syn-ops")); err != nil || v != "syn-pass" {
		t.Errorf("password = %q (err %v), want it stored under that tenant", v, err)
	}
	if got.runAsFor("333") != "syn-ops" {
		t.Errorf("runAsFor(333) = %q, want the new user", got.runAsFor("333"))
	}
}

func TestCrossTenantBatchPairsEachElementWithItsTenantsLogin(t *testing.T) {
	m := credsModel(t)
	m.mode = modeBatch
	setTestClient(&m, liveClient())
	m.pool.set("222", liveClient())
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	var picked []elementRow
	for _, r := range m.all {
		if m.selected[r.key()] {
			picked = append(picked, r)
		}
	}
	targets := m.targetsFor(picked)
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}

	byElement := map[string][2]string{}
	for _, tg := range targets {
		byElement[tg.ElementID] = [2]string{tg.User, tg.Pass}
	}
	if got := byElement["a"]; got != [2]string{"tsc-ops", "pw-tsc-ops"} {
		t.Errorf("element a paired with %v, want Tractor Supply's login", got)
	}
	if got := byElement["c"]; got != [2]string{"nb-ops", "pw-nb"} {
		t.Errorf("element c paired with %v, want Niagara's login", got)
	}
	if byElement["a"] == byElement["c"] {
		t.Error("both tenants were given the same login")
	}
}

func TestCrossTenantBatchRefusesWhenATenantHasNoLogin(t *testing.T) {
	m := credsModel(t)
	m.mode = modeBatch
	setTestClient(&m, liveClient())
	m.pool.set("222", liveClient())
	m.cmdInput = newField("", "", false).input

	if err := m.store.Secrets.Delete(config.IONPassKey("222", "nb-ops")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	mm, _ := m.startRun([]string{"dump device status"})
	got := mm.(tuiModel)
	if got.phase == phaseRunning {
		t.Error("the run started with a tenant that has no login")
	}
	if !strings.Contains(got.status, "Niagara Bottling") {
		t.Errorf("status = %q, want the tenant without a login named", got.status)
	}
}

func TestBatchAcrossTenantsAfterSuppliedPasswords(t *testing.T) {
	m := credsModel(t)
	m.mode = modeBatch
	m.phase = phasePicking
	setTestClient(&m, liveClient())
	m.pool.set("222", liveClient())
	m.cmdInput = newField("", "", false).input
	if err := m.store.Secrets.Delete(config.IONPassKey("222", "nb-ops")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	mm, _ := m.onPickEnter()
	prompt := mm.(tuiModel)
	if prompt.phase != phasePassword {
		t.Fatalf("phase = %v, want the credential prompt", prompt.phase)
	}
	if prompt.formTenantName != "Niagara Bottling" {
		t.Fatalf("prompting for %q, want the tenant that is missing one", prompt.formTenantName)
	}

	prompt.form[0].input.SetValue("supplied")
	mm, _ = prompt.submitForm()
	after := mm.(tuiModel)

	if after.phase != phaseCommand {
		t.Fatalf("phase = %v, want the command prompt once all logins are in", after.phase)
	}
	if v, _ := after.store.Secrets.Get(config.IONPassKey("222", "nb-ops")); v != "supplied" {
		t.Errorf("password for 222 = %q, want it saved there", v)
	}
	if v, _ := after.store.Secrets.Get(config.IONPassKey("111", "nb-ops")); v != "" {
		t.Errorf("password leaked into the other tenant: %q", v)
	}
}

func TestSelectingFromACachedListConnectsItsTenant(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.phase = phasePicking
	m.mode = modeShell
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()

	targets := []sessionTarget{{eid: "a", name: "s0100-rtr-01", tsg: "111"}}
	mm, cmd := m.openSessions(targets)
	got := mm.(tuiModel)

	if cmd == nil {
		t.Fatal("no command returned, want the tenant connected")
	}
	if got.connectWait != 1 {
		t.Errorf("connectWait = %d, want one outstanding connection", got.connectWait)
	}
	if len(got.resumeSessions) != 1 {
		t.Errorf("resumeSessions = %v, want the request held for replay", got.resumeSessions)
	}
	if strings.Contains(got.status, "not connected") || strings.Contains(got.status, "skipped") {
		t.Errorf("status = %q, want no complaint about connectivity", got.status)
	}
	if got.connecting == "" {
		t.Error("nothing reported as connecting")
	}
}

func TestAConnectedTenantReplaysWhatWasWaiting(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.phase = phaseSession
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()
	m.connectWait = 1
	m.resumeSessions = []sessionTarget{{eid: "a", name: "s0100-rtr-01", tsg: "111"}}

	mm, cmd := m.Update(tenantConnectedMsg{tsg: "111", client: liveClient()})
	got := mm.(tuiModel)

	if got.pool.get("111") == nil {
		t.Error("the connected client was not pooled")
	}
	if got.connectWait != 0 {
		t.Errorf("connectWait = %d, want 0", got.connectWait)
	}
	if len(got.resumeSessions) != 0 {
		t.Error("resumeSessions was not consumed, so it would replay again")
	}
	if cmd == nil {
		t.Error("no command returned, want the held request carried out")
	}
	if got.connecting != "s0100-rtr-01" {
		t.Errorf("connecting = %q, want the replayed session", got.connecting)
	}
}

func TestSeveralTenantsAreAwaitedBeforeReplaying(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.phase = phaseSession
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()
	m.connectWait = 2
	m.resumeSessions = []sessionTarget{
		{eid: "a", name: "s0100-rtr-01", tsg: "111"},
		{eid: "c", name: "ord-gw-01", tsg: "222"},
	}

	mm, cmd := m.Update(tenantConnectedMsg{tsg: "111", client: liveClient()})
	half := mm.(tuiModel)
	if cmd != nil {
		t.Error("replayed with a tenant still connecting")
	}
	if len(half.resumeSessions) != 2 {
		t.Error("the held request was dropped early")
	}

	mm, cmd = half.Update(tenantConnectedMsg{tsg: "222", client: liveClient()})
	done := mm.(tuiModel)
	if cmd == nil {
		t.Error("no command returned once every tenant was connected")
	}
	if len(done.resumeSessions) != 0 {
		t.Error("resumeSessions survived the replay")
	}
}

func TestAFailedConnectionIsReportedNotSwallowed(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.phase = phaseSession
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()
	m.connectWait = 1
	m.resumeSessions = []sessionTarget{{eid: "c", name: "ord-gw-01", tsg: "222"}}

	mm, _ := m.Update(tenantConnectedMsg{tsg: "222", err: errInteractionRequired})
	got := mm.(tuiModel)
	if !strings.Contains(got.status, "Niagara Bottling") {
		t.Errorf("status = %q, want the tenant named", got.status)
	}
	if got.tenantIssues["222"] == "" {
		t.Error("the failure was not recorded")
	}
}

func TestBatchWaitsForItsTenantsToConnect(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.mode = modeBatch
	m.phase = phaseCommand
	m.spinner = ui.NewSpinner("")
	m.cmdInput = newField("", "", false).input
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	mm, cmd := m.startRun([]string{"dump device status"})
	got := mm.(tuiModel)
	if cmd == nil {
		t.Fatal("no command returned, want the tenants connected first")
	}
	if got.phase == phaseRunning {
		t.Error("the run started before its tenants were connected")
	}
	if got.connectWait != 2 {
		t.Errorf("connectWait = %d, want both tenants awaited", got.connectWait)
	}
	if len(got.resumeRun) != 1 {
		t.Errorf("resumeRun = %v, want the commands held", got.resumeRun)
	}
	if !strings.Contains(got.status, "connecting") {
		t.Errorf("status = %q, want the wait reported", got.status)
	}
}

func TestEnsureClientsSkipsWhatIsAlreadyConnected(t *testing.T) {
	m := credsModel(t)
	m.pool.set("111", liveClient())

	cmds := m.ensureClients([]string{"111", "222", "222", ""})
	if len(cmds) != 1 {
		t.Errorf("got %d connect commands, want 1 for the unconnected tenant", len(cmds))
	}
	if len(m.ensureClients([]string{"111"})) != 0 {
		t.Error("a connected tenant was asked to connect again")
	}
}

func TestEnsureClientsSkipsTenantsWithNoCredential(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.cfg.Tenants[1].TSGID = ""
	m.cfg.Tenants[1].Name = "Broken"

	cmds := m.ensureClients([]string{"111", ""})
	if len(cmds) != 1 {
		t.Errorf("got %d connect commands, want only the usable tenant", len(cmds))
	}
}

func TestAFailedTenantIsNotRetriedForever(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()
	m.tenantIssues = map[string]string{"222": "not accessible with this sign-in"}

	if cmds := m.ensureClients([]string{"222"}); len(cmds) != 0 {
		t.Errorf("got %d connect commands, want none for a tenant that just failed", len(cmds))
	}
	if cmds := m.ensureClients([]string{"111"}); len(cmds) != 1 {
		t.Errorf("got %d connect commands for a healthy tenant, want 1", len(cmds))
	}
}

func TestPressingEnterAgainRetriesAFailedTenant(t *testing.T) {
	m := credsModel(t)
	m.pool = newClientPool()
	m.phase = phasePicking
	m.mode = modeShell
	m.spinner = ui.NewSpinner("")
	m.sessions = ionterm.NewManager()
	m.tenantIssues = map[string]string{"222": "not accessible with this sign-in"}
	for _, r := range m.all {
		if r.TSG == "222" {
			m.selected[r.key()] = true
		}
	}

	mm, cmd := m.onPickEnter()
	got := mm.(tuiModel)
	if got.tenantIssues["222"] != "" {
		t.Error("the earlier failure still blocks a deliberate retry")
	}
	if cmd == nil {
		t.Error("no command returned, want the retry to connect")
	}
	if got.connectWait != 1 {
		t.Errorf("connectWait = %d, want the tenant connecting again", got.connectWait)
	}
}

func liveClient() *api.Client {
	return api.NewClient(nil, nil, "test-token", false, 1, 1)
}

func spentClient(t *testing.T) *api.Client {
	t.Helper()
	c := api.NewClient(nil, nil, "about-to-expire", false, 1, 1)
	c.SetExpiryForTest(30 * time.Second)
	return c
}

func TestEnsureClientsRenewsATokenThatWillNotOutlastTheOperation(t *testing.T) {
	m := credsModel(t)
	spent := spentClient(t)
	m.pool.set("111", spent)

	cmds := m.ensureClients([]string{"111"})
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want the spent token renewed before use", len(cmds))
	}
	if got := m.pool.get("111"); got != spent {
		t.Error("the client was replaced, want its token renewed in place")
	}
}

func TestEnsureClientsLeavesAHealthyTokenAlone(t *testing.T) {
	m := credsModel(t)
	c := api.NewClient(nil, nil, "good", false, 1, 1)
	c.SetExpiryForTest(15 * time.Minute)
	m.pool.set("111", c)

	if cmds := m.ensureClients([]string{"111"}); len(cmds) != 0 {
		t.Errorf("got %d commands, want none for a token with life left", len(cmds))
	}
}

func TestEnsureClientsRenewsOnlyTheTenantsThatNeedIt(t *testing.T) {
	m := credsModel(t)
	good := api.NewClient(nil, nil, "good", false, 1, 1)
	good.SetExpiryForTest(15 * time.Minute)
	m.pool.set("111", good)
	m.pool.set("222", spentClient(t))

	if cmds := m.ensureClients([]string{"111", "222"}); len(cmds) != 1 {
		t.Errorf("got %d commands, want 1 for the one spent tenant", len(cmds))
	}
}
