package tui

import (
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

var discoveredTenants = []saseauth.Tenant{
	{ID: "1643707006", Name: "CIS root"},
	{ID: "1538107912", Name: "NIAGARA BOTTLING, LLC-145595"},
	{ID: "1984021017", Name: "Tractor Supply Co.", ParentID: "1750385018", Depth: 2},
}

func discoverModel(t *testing.T) tuiModel {
	t.Helper()
	m := signInModel(t, config.Tenant{})
	m.cfg = &config.Config{}
	m.hasTenant = false
	m.tenant = config.Tenant{}
	m.width, m.height = 100, 30
	m.spinner = ui.NewSpinner("")
	return m
}

func seedKnown(t *testing.T, m tuiModel, tenants ...config.KnownTenant) {
	t.Helper()
	if err := m.store.SaveTenantCache(&config.TenantCache{
		FetchedAt: time.Now(),
		Account:   "Ben Hall <ben@example.test>",
		Tenants:   tenants,
	}); err != nil {
		t.Fatalf("SaveTenantCache() error = %v", err)
	}
}

func TestBrowserAccountNeedsNothingTyped(t *testing.T) {
	m := discoverModel(t)
	m.phase = phaseMenu
	for i, e := range m.menuEntries() {
		if e.action == actSignIn {
			m.menuCursor = i
		}
	}

	mm, cmd := m.onMenuKey("enter")
	got := mm.(tuiModel)
	if got.phase != phaseDiscover {
		t.Fatalf("phase = %v, want phaseDiscover rather than a form or a chooser", got.phase)
	}
	if len(got.form) != 0 {
		t.Errorf("a form with %d fields was opened; a browser account asks for nothing", len(got.form))
	}
	if cmd == nil {
		t.Error("no command returned, want the browser opened")
	}
}

func TestSigningInAgainShowsTheListAlreadyDiscovered(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	m.phase = phaseMenu
	for i, e := range m.menuEntries() {
		if e.action == actSignIn {
			m.menuCursor = i
		}
	}

	mm, cmd := m.onMenuKey("enter")
	got := mm.(tuiModel)
	if got.phase != phaseSwitch {
		t.Errorf("phase = %v, want the known list without reopening a browser", got.phase)
	}
	if cmd != nil {
		t.Error("a browser was opened although the tenants were already known")
	}
}

func TestDiscoveryLandsOnTheWorkingSetPicker(t *testing.T) {
	m := discoverModel(t)
	m.phase = phaseDiscover

	mm, _ := m.Update(discoveredMsg{d: saseauth.Discovery{
		Identity: saseauth.Identity{Name: "Ben Hall", Email: "ben@example.test"},
		Tenants:  discoveredTenants,
	}})
	got := mm.(tuiModel)

	if got.phase != phaseTenantPick {
		t.Fatalf("phase = %v, want the tenant picker", got.phase)
	}
	if len(got.discovered) != 3 {
		t.Errorf("discovered %d tenants, want 3 offered", len(got.discovered))
	}
	// Nothing is in the working set until it is ticked.
	if len(got.cfg.Tenants) != 0 {
		t.Errorf("%d tenants were saved before anything was picked", len(got.cfg.Tenants))
	}
	if len(got.picked) != 0 {
		t.Errorf("%d tenants started ticked, want none on a first sign-in", len(got.picked))
	}

	cached, err := got.store.TenantCache()
	if err != nil {
		t.Fatalf("TenantCache() error = %v", err)
	}
	if cached == nil || len(cached.Tenants) != 3 {
		t.Fatalf("cached tenants = %+v, want all 3", cached)
	}
	if cached.Account != "Ben Hall <ben@example.test>" {
		t.Errorf("Account = %q, want the signed-in identity recorded", cached.Account)
	}
}

func TestTenantChoicesMergeDiscoveredAndSaved(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m,
		config.KnownTenant{TSGID: "1643707006", Name: "CIS root"},
		config.KnownTenant{TSGID: "1984021017", Name: "Tractor Supply Co."},
	)
	// A service account tenant the browser account cannot see, plus a saved
	// record for one it can.
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
		{Name: "Tractor Supply Co.", TSGID: "1984021017", Auth: config.AuthBrowser},
	}}

	got := m.tenantChoices()
	if len(got) != 3 {
		t.Fatalf("got %d choices, want 3 (two discovered plus one service account)", len(got))
	}

	byTSG := map[string]tenantChoice{}
	for _, c := range got {
		byTSG[c.tsg] = c
	}
	if c := byTSG["1643707006"]; !c.known || c.saved {
		t.Errorf("CIS root = %+v, want discovered and not yet saved", c)
	}
	if c := byTSG["1984021017"]; !c.known || !c.saved {
		t.Errorf("Tractor Supply = %+v, want both discovered and saved", c)
	}
	if c := byTSG["999"]; c.known || !c.saved {
		t.Errorf("Legacy Co = %+v, want saved only", c)
	}
	// Sorted by name.
	if got[0].name != "CIS root" {
		t.Errorf("first choice = %q, want the list sorted by name", got[0].name)
	}
}

func TestUsingADiscoveredTenantCreatesItsRecordLazily(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "1984021017", Name: "Tractor Supply Co."})
	m.phase = phaseSwitch
	m.switchCursor = 0

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)

	if got.phase != phaseMenu {
		t.Fatalf("phase = %v, want phaseMenu", got.phase)
	}
	if len(got.cfg.Tenants) != 1 {
		t.Fatalf("config holds %d tenants, want the used one recorded", len(got.cfg.Tenants))
	}
	saved := got.cfg.Tenants[0]
	if saved.TSGID != "1984021017" || saved.Name != "Tractor Supply Co." {
		t.Errorf("saved = %+v, want the portal's name and id", saved)
	}
	if !saved.UsesBrowser() {
		t.Errorf("Auth = %q, want a discovered tenant to be browser-backed", saved.Auth)
	}
	if !got.hasTenant || got.tenant.TSGID != "1984021017" {
		t.Error("the chosen tenant did not become active")
	}
	if got.cfg.LastTenant != saved.Name {
		t.Errorf("LastTenant = %q, want %q", got.cfg.LastTenant, saved.Name)
	}
	// A fresh tenant has no elements yet, so a fetch is owed.
	if got.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want a fetch owed for a tenant never used", got.cacheState)
	}
}

func TestUsingATenantTwiceDoesNotDuplicateIt(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	m = mm.(tuiModel)
	mm, _ = m.onSwitchKey("enter")
	got := mm.(tuiModel)

	if len(got.cfg.Tenants) != 1 {
		t.Errorf("config holds %d tenants, want 1", len(got.cfg.Tenants))
	}
}

func TestSwitchingKeepsAServiceAccountsOwnAuth(t *testing.T) {
	m := discoverModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
	}}
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.tenant.UsesBrowser() {
		t.Error("switching to a service account tenant turned it into a browser tenant")
	}
	if got.tenant.ClientID != "svc" {
		t.Errorf("ClientID = %q, want the existing record reused", got.tenant.ClientID)
	}
}

func TestSwitchingToATenantWithCachedElements(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	if err := m.store.SaveCache(&config.ElementCache{
		TSGID: "111", FetchedAt: time.Now().Add(-2 * time.Minute),
		Elements: []api.Element{{ID: "e1", Name: "s0001"}},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.cacheState != cacheCached {
		t.Errorf("cacheState = %v, want a fresh cache recognised", got.cacheState)
	}
	if got.cacheCount != 1 {
		t.Errorf("cacheCount = %d, want 1", got.cacheCount)
	}
}

func TestSwitchingClearsTheOldTenantsClient(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m,
		config.KnownTenant{TSGID: "111", Name: "Acme Co"},
		config.KnownTenant{TSGID: "222", Name: "Beta Co"},
	)
	m.phase = phaseSwitch
	setTestClient(&m, liveClient())
	m.tokenDead = "sign-in expired"
	m.switchCursor = 1

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.client != nil {
		t.Error("the previous tenant's client was kept, so calls would go to the wrong tenant")
	}
	if got.tokenDead != "" {
		t.Error("the previous tenant's expiry warning was kept")
	}
	if got.tenant.TSGID != "222" {
		t.Errorf("active tenant = %q, want 222", got.tenant.TSGID)
	}
}

func TestReListingAsksTheAccountAgain(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	m.phase = phaseSwitch

	mm, cmd := m.onSwitchKey("alt+r")
	got := mm.(tuiModel)
	if got.phase != phaseDiscover {
		t.Errorf("phase = %v, want a re-listing", got.phase)
	}
	if cmd == nil {
		t.Error("no command returned, want the account asked again")
	}
}

func TestDiscoveryFailureReturnsToTheMethodChoice(t *testing.T) {
	m := discoverModel(t)
	m.phase = phaseDiscover

	mm, _ := m.Update(discoveredMsg{err: errTokenTest})
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want to land back on the menu", got.phase)
	}
	if !strings.Contains(got.status, "sign-in failed") {
		t.Errorf("status = %q, want the failure reported", got.status)
	}
}

func TestMenuNamesTheTwoCredentialPaths(t *testing.T) {
	m := discoverModel(t)
	var labels []string
	for _, e := range m.menuEntries() {
		labels = append(labels, e.label)
	}
	joined := strings.Join(labels, " | ")

	for _, gone := range []string{"Add Account", "Add Tenant", "switch tenant", "service accounts", "start ION shell"} {
		if strings.Contains(joined, gone) {
			t.Errorf("the menu still offers %q: %s", gone, joined)
		}
	}
	for _, want := range []string{"elements", "sign in with browser"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the menu is missing %q: %s", want, joined)
		}
	}
}

func TestMenuIsOneEntryPerThing(t *testing.T) {
	m := discoverModel(t)
	seen := map[menuAction]bool{}
	for _, e := range m.menuEntries() {
		seen[e.action] = true
	}
	if !seen[actElements] {
		t.Error("no elements entry")
	}
	if !seen[actSignIn] {
		t.Error("no browser sign-in entry with nothing configured")
	}
}

func TestServiceAndBrowserTenantsArePartitioned(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Browser Co", TSGID: "111", Auth: config.AuthBrowser},
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
	}}

	accounts := m.serviceTenants()
	if len(accounts) != 1 || accounts[0].tsg != "999" {
		t.Fatalf("serviceTenants() = %+v, want only the service account", accounts)
	}
	browsers := m.browserTenants()
	if len(browsers) != 1 || browsers[0].tsg != "111" {
		t.Fatalf("browserTenants() = %+v, want only the discovered tenant", browsers)
	}
}

func TestRenderTenantList(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m,
		config.KnownTenant{TSGID: "1643707006", Name: "CIS root"},
		config.KnownTenant{TSGID: "1984021017", Name: "Tractor Supply Co."},
	)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount},
	}}
	m.tenant, m.hasTenant = config.Tenant{Name: "CIS root", TSGID: "1643707006"}, true
	m.phase = phaseSwitch

	out := plain(m.viewSwitch())
	for _, want := range []string{
		"3 tenants",
		"from Ben Hall <ben@example.test>",
		"CIS root", "1643707006", "(current)",
		"Legacy Co", "(service account)",
		"enter use", "r re-list",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tenant list is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewSwitch())
}

func TestRenderDiscoverWait(t *testing.T) {
	m := discoverModel(t)
	m.phase = phaseDiscover
	out := plain(m.viewDiscover())
	if !strings.Contains(out, "waiting for browser") {
		t.Errorf("the waiting screen does not say what it waits for:\n%s", out)
	}
	if !m.wantsSpinner() {
		t.Error("wantsSpinner() = false while waiting for the browser")
	}
}

func TestTenantChoicesSurviveANilStore(t *testing.T) {
	// menuEntries calls this during rendering, so it must not panic before a
	// store exists.
	m := tuiModel{cfg: &config.Config{}}
	if got := m.tenantChoices(); len(got) != 0 {
		t.Errorf("tenantChoices() = %+v, want none", got)
	}
	m.cfg = &config.Config{Tenants: []config.Tenant{{Name: "Acme Co", TSGID: "1"}}}
	if got := m.tenantChoices(); len(got) != 1 {
		t.Errorf("tenantChoices() = %+v, want the saved tenant", got)
	}
	var nilCfg tuiModel
	if got := nilCfg.tenantChoices(); len(got) != 0 {
		t.Errorf("tenantChoices() with no config = %+v, want none", got)
	}
}

func TestTenantsWithoutAnIDDoNotCollapse(t *testing.T) {
	// A tenant ID is the real identity, but records predating one must stay
	// distinct rather than merging into a single row.
	m := discoverModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Acme Co"},
		{Name: "Lab"},
	}}
	if got := m.tenantChoices(); len(got) != 2 {
		t.Errorf("got %d choices, want both kept apart: %+v", len(got), got)
	}
}

func TestSwitchingActuallyFetchesForTheNewTenant(t *testing.T) {
	tests := []struct {
		name      string
		cacheAge  time.Duration
		cached    bool
		wantState cacheState
		wantCmd   bool
	}{
		{
			name:      "a tenant never used fetches",
			cached:    false,
			wantState: cacheUpdating,
			wantCmd:   true,
		},
		{
			name:      "a stale cache fetches",
			cached:    true,
			cacheAge:  38 * time.Minute,
			wantState: cacheUpdating,
			wantCmd:   true,
		},
		{
			name:      "a fresh cache does not",
			cached:    true,
			cacheAge:  2 * time.Minute,
			wantState: cacheCached,
			wantCmd:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := discoverModel(t)
			seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
			// Browser tenants validate with only a tenant ID, so a fetch is
			// genuinely startable here.
			if tt.cached {
				if err := m.store.SaveCache(&config.ElementCache{
					TSGID: "111", FetchedAt: time.Now().Add(-tt.cacheAge),
					Elements: []api.Element{{ID: "e1", Name: "s0001"}},
				}); err != nil {
					t.Fatalf("SaveCache() error = %v", err)
				}
			}
			m.phase = phaseSwitch

			mm, cmd := m.onSwitchKey("enter")
			got := mm.(tuiModel)

			if got.cacheState != tt.wantState {
				t.Errorf("cacheState = %v, want %v", got.cacheState, tt.wantState)
			}
			if tt.wantCmd && cmd == nil {
				t.Error("no command returned, so the cache line would claim to be updating forever")
			}
			if !tt.wantCmd && cmd != nil {
				t.Error("a fresh cache triggered an unnecessary fetch")
			}
			if got.phase != phaseMenu {
				t.Errorf("phase = %v, want phaseMenu", got.phase)
			}
		})
	}
}

func TestSwitchingToATenantWithNoUsableCredentialSaysSo(t *testing.T) {
	m := discoverModel(t)
	// A service account record with no secret stored cannot authenticate.
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount},
	}}
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.cacheState != cacheFailed {
		t.Errorf("cacheState = %v, want cacheFailed rather than a permanent 'updating'", got.cacheState)
	}
	if got.status == "" {
		t.Error("status is empty, want the credential problem surfaced")
	}
}

// setTestElements loads plain elements as if they all came from one tenant,
// which is what the single-tenant tests mean.
func setTestElements(m *tuiModel, els []api.Element) {
	rows := make([]elementRow, 0, len(els))
	for _, el := range els {
		rows = append(rows, elementRow{Element: el, TSG: "1", Tenant: "Acme Co"})
	}
	m.setElements(rows)
}

// setTestClient makes a client reachable the way production does: in the pool,
// keyed by tenant. Tests that only set m.client would find no client to dial
// with.
func setTestClient(m *tuiModel, c *api.Client) {
	m.client = c
	if m.pool == nil {
		m.pool = newClientPool()
	}
	m.pool.set(m.tenant.TSGID, c)
	m.pool.set("1", c)
}

func TestServiceAccountModeOnlyOffersWhatItCanReach(t *testing.T) {
	m := multiModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Saved SA", TSGID: "111", Auth: config.AuthServiceAccount, ClientID: "sa@1.iam"},
		{Name: "Saved Browser", TSGID: "222", Auth: config.AuthBrowser},
	}}
	m.tenant, m.hasTenant = m.cfg.Tenants[0], true
	// A browser sign-in listed a third tenant that has no saved credential.
	if err := m.store.SaveTenantCache(&config.TenantCache{
		Account: "someone@example.com",
		Tenants: []config.KnownTenant{{TSGID: "333", Name: "Discovered"}},
	}); err != nil {
		t.Fatal(err)
	}

	m.authMode = authBrowser
	if got := len(m.switchChoices()); got != 3 {
		t.Errorf("browser mode offers %d tenants, want all 3", got)
	}

	// A service account credential cannot reach a discovered tenant or a
	// browser-backed one, and offering them is a page of tenants that all fail.
	m.authMode = authTenant
	got := m.switchChoices()
	if len(got) != 1 || got[0].name != "Saved SA" {
		t.Errorf("service account mode offers %+v, want only the saved service account", got)
	}

	view := plain(m.viewSwitch())
	if !strings.Contains(view, "1 service account\n") {
		t.Errorf("the page does not count service accounts:\n%s", view)
	}
	// The browser account that listed those tenants is irrelevant here.
	if strings.Contains(view, "someone@example.com") {
		t.Errorf("the page claims browser provenance in service account mode:\n%s", view)
	}
	if strings.Contains(view, "alt+r") {
		t.Errorf("the page offers a re-list with nothing to ask:\n%s", view)
	}
}

func TestReListIsRefusedInServiceAccountMode(t *testing.T) {
	m := multiModel(t)
	m.authMode = authTenant
	m.phase = phaseSwitch

	mm, cmd := m.onSwitchKey("alt+r")
	got := mm.(tuiModel)
	if cmd != nil {
		t.Error("alt+r started a browser discovery in service account mode")
	}
	if got.phase != phaseSwitch {
		t.Errorf("phase = %v, want to stay on the page", got.phase)
	}
	if !strings.Contains(got.status, "settings") {
		t.Errorf("status = %q, want it to say where service accounts come from", got.status)
	}
}
