package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/app"
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
	m.runtime().UseConfig(m.store(), &config.Config{})
	m.runtime().ClearTenant()
	m.runtime().ReplaceTenant(config.Tenant{})
	m.width, m.height = 100, 30
	m.spinner = ui.NewSpinner("")
	return m
}

func seedKnown(t *testing.T, m tuiModel, tenants ...config.KnownTenant) {
	t.Helper()
	if err := m.store().SaveTenantCache(&config.TenantCache{
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
	if len(got.rt.Discovered()) != 3 {
		t.Errorf("discovered %d tenants, want 3 offered", len(got.rt.Discovered()))
	}
	if len(got.cfg().Tenants) != 0 {
		t.Errorf("%d tenants were saved before anything was picked", len(got.cfg().Tenants))
	}
	if got.rt.PickedCount() != 0 {
		t.Errorf("%d tenants started ticked, want none on a first sign-in", got.rt.PickedCount())
	}

	cached, err := got.store().TenantCache()
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
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
		{Name: "Tractor Supply Co.", TSGID: "1984021017", Auth: config.AuthBrowser},
	}})

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
	if len(got.cfg().Tenants) != 1 {
		t.Fatalf("config holds %d tenants, want the used one recorded", len(got.cfg().Tenants))
	}
	saved := got.cfg().Tenants[0]
	if saved.TSGID != "1984021017" || saved.Name != "Tractor Supply Co." {
		t.Errorf("saved = %+v, want the portal's name and id", saved)
	}
	if !saved.UsesBrowser() {
		t.Errorf("Auth = %q, want a discovered tenant to be browser-backed", saved.Auth)
	}
	if !got.hasTenant() || got.tenant().TSGID != "1984021017" {
		t.Error("the chosen tenant did not become active")
	}
	if got.cfg().LastTenant != saved.Name {
		t.Errorf("LastTenant = %q, want %q", got.cfg().LastTenant, saved.Name)
	}
	if got.cacheState() != cacheUpdating {
		t.Errorf("cacheState = %v, want a fetch owed for a tenant never used", got.cacheState())
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

	if len(got.cfg().Tenants) != 1 {
		t.Errorf("config holds %d tenants, want 1", len(got.cfg().Tenants))
	}
}

func TestSwitchingKeepsAServiceAccountsOwnAuth(t *testing.T) {
	m := discoverModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
	}})
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.tenant().UsesBrowser() {
		t.Error("switching to a service account tenant turned it into a browser tenant")
	}
	if got.tenant().ClientID != "svc" {
		t.Errorf("ClientID = %q, want the existing record reused", got.tenant().ClientID)
	}
}

func TestSwitchingToATenantWithCachedElements(t *testing.T) {
	m := discoverModel(t)
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Acme Co"})
	if err := m.store().SaveCache(&config.ElementCache{
		TSGID: "111", FetchedAt: time.Now().Add(-2 * time.Minute),
		Elements: []api.Element{{ID: "e1", Name: "s0001"}},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.cacheState() != cacheCached {
		t.Errorf("cacheState = %v, want a fresh cache recognised", got.cacheState())
	}
	if got.cacheCount() != 1 {
		t.Errorf("cacheCount = %d, want 1", got.cacheCount())
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
	m.runtime().TokenDied("sign-in expired", false)
	m.switchCursor = 1

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.pool().Get(got.tenant().TSGID) != nil {
		t.Error("the previous tenant's client was kept, so calls would go to the wrong tenant")
	}
	if got.rt.TokenDead() != "" {
		t.Error("the previous tenant's expiry warning was kept")
	}
	if got.tenant().TSGID != "222" {
		t.Errorf("active tenant = %q, want 222", got.tenant().TSGID)
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

	for _, gone := range []string{"Add Account", "Add Tenant", "switch tenant", "Service Accounts", "start ION shell"} {
		if strings.Contains(joined, gone) {
			t.Errorf("the menu still offers %q: %s", gone, joined)
		}
	}
	for _, want := range []string{"Elements", "Sign In with Browser"} {
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
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Browser Co", TSGID: "111", Auth: config.AuthBrowser},
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount, ClientID: "svc"},
	}})

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
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount},
	}})
	m.runtime().UseTenant(config.Tenant{Name: "CIS root", TSGID: "1643707006"})
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
	m := withStore(tuiModel{}, nil, &config.Config{})
	if got := m.tenantChoices(); len(got) != 0 {
		t.Errorf("tenantChoices() = %+v, want none", got)
	}
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{{Name: "Acme Co", TSGID: "1"}}})
	if got := m.tenantChoices(); len(got) != 1 {
		t.Errorf("tenantChoices() = %+v, want the saved tenant", got)
	}
	var nilCfg tuiModel
	if got := nilCfg.tenantChoices(); len(got) != 0 {
		t.Errorf("tenantChoices() with no config = %+v, want none", got)
	}
}

func TestTenantsWithoutAnIDDoNotCollapse(t *testing.T) {
	m := discoverModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Acme Co"},
		{Name: "Lab"},
	}})
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
			if tt.cached {
				if err := m.store().SaveCache(&config.ElementCache{
					TSGID: "111", FetchedAt: time.Now().Add(-tt.cacheAge),
					Elements: []api.Element{{ID: "e1", Name: "s0001"}},
				}); err != nil {
					t.Fatalf("SaveCache() error = %v", err)
				}
			}
			m.phase = phaseSwitch

			mm, cmd := m.onSwitchKey("enter")
			got := mm.(tuiModel)

			if got.cacheState() != tt.wantState {
				t.Errorf("cacheState = %v, want %v", got.cacheState(), tt.wantState)
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

func TestAddingATenantFetchesEvenBesideAFreshlyCachedOne(t *testing.T) {
	m := discoverModel(t)
	m.runtime().SetDiscovered(discoveredTenants)
	pickOnly(&m, "1643707006", "1984021017")

	if err := m.store().SaveCache(&config.ElementCache{
		TSGID: "1643707006", FetchedAt: time.Now().Add(-2 * time.Minute),
		Elements: []api.Element{{ID: "e1", Name: "s0001-rtr-01"}},
	}); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	mm, cmd := m.saveTenantPick()
	got := mm.(tuiModel)

	if cmd == nil {
		t.Error("nothing was fetched, so the tenant just added has no elements until a manual refresh")
	}
	if got.cacheState() != cacheUpdating {
		t.Errorf("cacheState = %v, want cacheUpdating with a tenant still unread", got.cacheState())
	}
}

func TestSwitchingToATenantWithNoUsableCredentialSaysSo(t *testing.T) {
	m := discoverModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Legacy Co", TSGID: "999", Auth: config.AuthServiceAccount},
	}})
	m.phase = phaseSwitch

	mm, _ := m.onSwitchKey("enter")
	got := mm.(tuiModel)
	if got.cacheState() != cacheFailed {
		t.Errorf("cacheState = %v, want cacheFailed rather than a permanent 'updating'", got.cacheState())
	}
	if got.status == "" {
		t.Error("status is empty, want the credential problem surfaced")
	}
}

func setTestElements(m *tuiModel, els []api.Element) {
	rows := make([]elementRow, 0, len(els))
	for _, el := range els {
		rows = append(rows, elementRow{Element: el, TSG: "1", Tenant: "Acme Co"})
	}
	m.setElements(rows)
}

func setTestClient(m *tuiModel, c *api.Client) {
	m.pool().Set(m.tenant().TSGID, c)
	m.pool().Set("1", c)
}

func TestServiceAccountModeOnlyOffersWhatItCanReach(t *testing.T) {
	m := multiModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{Tenants: []config.Tenant{
		{Name: "Saved SA", TSGID: "111", Auth: config.AuthServiceAccount, ClientID: "sa@1.iam"},
		{Name: "Saved Browser", TSGID: "222", Auth: config.AuthBrowser},
	}})
	m.runtime().UseTenant(m.cfg().Tenants[0])
	if err := m.store().SaveTenantCache(&config.TenantCache{
		Account: "someone@example.com",
		Tenants: []config.KnownTenant{{TSGID: "333", Name: "Discovered"}},
	}); err != nil {
		t.Fatal(err)
	}

	m.setAuthMode(authBrowser)
	if got := len(m.switchChoices()); got != 3 {
		t.Errorf("browser mode offers %d tenants, want all 3", got)
	}

	m.setAuthMode(authTenant)
	got := m.switchChoices()
	if len(got) != 1 || got[0].name != "Saved SA" {
		t.Errorf("service account mode offers %+v, want only the saved service account", got)
	}

	view := plain(m.viewSwitch())
	if !strings.Contains(view, "1 service account\n") {
		t.Errorf("the page does not count service accounts:\n%s", view)
	}
	if strings.Contains(view, "someone@example.com") {
		t.Errorf("the page claims browser provenance in service account mode:\n%s", view)
	}
	if strings.Contains(view, "alt+r") {
		t.Errorf("the page offers a re-list with nothing to ask:\n%s", view)
	}
}

func TestReListIsRefusedInServiceAccountMode(t *testing.T) {
	m := multiModel(t)
	m.setAuthMode(authTenant)
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

func serviceModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.phase = phaseSwitch
	m.setAuthMode(authTenant)
	m.width, m.height = 100, 30
	m.cfg().Tenants = []config.Tenant{
		{Name: "Acme Co", TSGID: "111", Auth: config.AuthServiceAccount,
			ClientID: "svc@1234.iam.panserviceaccount.com", Added: true},
	}
	m.runtime().UseTenant(m.cfg().Tenants[0])
	return m
}

func TestTheServiceAccountPageAddsAndForgets(t *testing.T) {
	m := serviceModel(t)

	out := plain(m.viewSwitch())
	if !strings.Contains(out, "+ New") {
		t.Errorf("the page offers no way to add an account:\n%s", out)
	}
	for _, want := range []string{"x forget", "u undo"} {
		if !strings.Contains(out, want) {
			t.Errorf("the hint does not offer %q:\n%s", want, out)
		}
	}

	if _, ok := m.switchAt(0); ok {
		t.Error("row 0 answered an account, want the action row")
	}
	c, ok := m.switchAt(1)
	if !ok || c.name != "Acme Co" {
		t.Fatalf("switchAt(1) = %+v, %v", c, ok)
	}

	next, _ := m.onSwitchKey("enter")
	if got := next.(tuiModel).formKind; got != formTenant {
		t.Errorf("enter on the action row opened %v, want the tenant form", got)
	}

	m.switchCursor = 1
	next, _ = m.onSwitchKey("x")
	m = next.(tuiModel)
	if _, still := m.cfg().Tenant("Acme Co"); !still {
		t.Fatal("one x forgot the account")
	}
	if !strings.Contains(m.status, "x again") {
		t.Errorf("status = %q, want it to ask for the second press", m.status)
	}
	next, _ = m.onSwitchKey("x")
	m = next.(tuiModel)
	if _, still := m.cfg().Tenant("Acme Co"); still {
		t.Errorf("the second x left it in place: %s", m.status)
	}

	next, _ = m.onSwitchKey("u")
	m = next.(tuiModel)
	if _, back := m.cfg().Tenant("Acme Co"); !back {
		t.Errorf("u did not put the record back: %s", m.status)
	}
	if !strings.Contains(m.status, "secret") {
		t.Errorf("status = %q, want it to say the secret has to be typed again", m.status)
	}
	next, _ = m.onSwitchKey("u")
	if got := next.(tuiModel).status; !strings.Contains(got, "nothing to undo") {
		t.Errorf("status = %q with nothing left to undo", got)
	}
}

func TestTheAccountInUseIsListedEvenWithNoSavedRecord(t *testing.T) {
	m := serviceModel(t)
	m.cfg().Tenants = nil
	m.runtime().UseTenant(config.Tenant{Name: "env", TSGID: "999"})

	choices := m.switchChoices()
	if len(choices) != 1 || choices[0].tsg != "999" {
		t.Fatalf("switchChoices() = %+v, want the account in use", choices)
	}

	out := plain(m.viewSwitch())
	if strings.Contains(out, "0 service account") {
		t.Errorf("the page says there are none while one is in use:\n%s", out)
	}
	if !strings.Contains(out, "from the environment") {
		t.Errorf("nothing says the account has no saved record:\n%s", out)
	}

	m.switchCursor = 1
	next, _ := m.onSwitchKey("x")
	if got := next.(tuiModel).status; !strings.Contains(got, "no saved record") {
		t.Errorf("status = %q, want it to say there is nothing to forget", got)
	}
}

func TestTheBrowserTenantPageHasNoAddRow(t *testing.T) {
	m := serviceModel(t)
	m.setAuthMode(authBrowser)

	if out := plain(m.viewSwitch()); strings.Contains(out, "+ New") {
		t.Errorf("the browser page offers to type one in:\n%s", out)
	}
	if _, ok := m.switchAt(0); !ok {
		t.Error("row 0 is not a tenant on the browser page")
	}
	m.switchCursor = 0
	next, _ := m.onSwitchKey("x")
	if got := next.(tuiModel).status; strings.Contains(got, "x again") {
		t.Error("the browser page armed a forget")
	}
}

func TestRenderTheServiceAccountPage(t *testing.T) {
	m := serviceModel(t)
	t.Log("\n" + m.viewSwitch())
}

func TestABrowserTenantIsNeverListedAsAServiceAccount(t *testing.T) {
	m := serviceModel(t)
	m.cfg().Tenants = []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Auth: config.AuthBrowser, Added: true},
	}
	m.runtime().UseTenant(m.cfg().Tenants[0])

	for _, c := range m.switchChoices() {
		if c.name == "Tractor Supply" {
			t.Errorf("a browser tenant is on the service account page: %+v", c)
		}
	}
	out := plain(m.viewSwitch())
	if strings.Contains(out, "Tractor Supply") {
		t.Errorf("the page lists it:\n%s", out)
	}
	if got := m.switchRows(); got != 1 {
		t.Errorf("switchRows() = %d, want just the action row", got)
	}
	if _, ok := m.switchAt(1); ok {
		t.Error("there is a selectable row below the action row")
	}
}

func TestThePickWarmsEveryTenantSoOneSignInLasts(t *testing.T) {
	m := multiModel(t)
	m.runtime().MarkBrowserSignedIn()
	m.runtime().SetDiscovered([]saseauth.Tenant{
		{ID: "111", Name: "Tractor Supply"},
		{ID: "222", Name: "Niagara Bottling"},
	})
	pickOnly(&m, "111", "222")
	m.spinner = ui.NewSpinner("")

	next, cmd := m.saveTenantPick()
	mm := next.(tuiModel)
	if mm.phase != phaseWarming {
		t.Errorf("phase = %v, want phaseWarming so each tenant gets a credential of its own", mm.phase)
	}
	if mm.rt.Warming() != 2 {
		t.Errorf("warming = %d, want 2 tenants", mm.rt.Warming())
	}
	if cmd == nil {
		t.Fatal("saveTenantPick() returned no command, so nothing warms the tenants")
	}
}

func TestThePickDoesNotWarmWithoutABrowserSignIn(t *testing.T) {
	m := multiModel(t)
	m.runtime().SetDiscovered([]saseauth.Tenant{{ID: "111", Name: "Tractor Supply"}})
	pickOnly(&m, "111")

	next, _ := m.saveTenantPick()
	if mm := next.(tuiModel); mm.phase == phaseWarming {
		t.Error("a service-account-only pick went warming; there is no browser session to warm from")
	}
}

func TestAWarmFailureIsRecordedWithoutKillingTheSignIn(t *testing.T) {
	m := multiModel(t)
	m.runtime().MarkBrowserSignedIn()
	m.spinner = ui.NewSpinner("")

	next, _ := m.onTenantsWarmed(tenantsWarmedMsg{results: []app.TenantWarm{
		{Tenant: m.cfg().Tenants[0]},
		{Tenant: m.cfg().Tenants[1], Err: fmt.Errorf("%w: %w; %w",
			saseauth.ErrInteractiveRequired, saseauth.ErrNoCredential, saseauth.ErrNoSession)},
	}})
	mm := next.(tuiModel)

	if mm.tenantIssues()["222"] == "" {
		t.Error("the tenant that could not be warmed carries no issue, so nothing will say why")
	}
	if mm.tenantIssues()["111"] != "" {
		t.Errorf("the warmed tenant carries %q, want nothing", mm.tenantIssues()["111"])
	}
	if mm.rt.TokenDead() != "" {
		t.Errorf("tokenDead = %q; one tenant refusing must not mark the whole sign-in dead", mm.rt.TokenDead())
	}
	if !strings.Contains(mm.status, "Niagara Bottling") {
		t.Errorf("status = %q, want it to name the tenant that failed", mm.status)
	}
	if mm.phase == phaseWarming {
		t.Error("still warming after the results arrived")
	}
}

func TestASecondBrowserSignInIsNotOfferedForATenantItAlreadyFailed(t *testing.T) {
	m := multiModel(t)
	m.runtime().MarkBrowserSignedIn()
	m.runtime().ClearIssues()
	m.runtime().SetIssue("111", "no sign-in for this tenant")
	m.phase = phaseLoading
	m.spinner = ui.NewSpinner("")

	next, _ := m.startSignInAuto()
	mm := next.(tuiModel)
	if mm.phase == phaseSignIn {
		t.Error("iontk reopened the browser for a tenant a fresh sign-in had just failed to reach")
	}
	if !strings.Contains(mm.status, "will not help") {
		t.Errorf("status = %q, want it to say another sign-in will not help", mm.status)
	}
}

func TestAnExpiredTenantStillGetsABrowserSignIn(t *testing.T) {
	m := multiModel(t)
	m.runtime().MarkBrowserSignedIn()
	m.runtime().TokenDied("sign-in expired", false)
	m.spinner = ui.NewSpinner("")

	next, _ := m.startSignInAuto()
	if mm := next.(tuiModel); mm.phase != phaseSignIn {
		t.Errorf("phase = %v, want phaseSignIn: a tenant with no recorded issue has only aged out", mm.phase)
	}
}

func pickOnly(m *tuiModel, ids ...string) {
	rt := m.runtime()
	for _, t := range rt.Discovered() {
		if rt.Picked(t.ID) {
			rt.TogglePick(t.ID)
		}
	}
	for _, id := range ids {
		if !rt.Picked(id) {
			rt.TogglePick(id)
		}
	}
}
