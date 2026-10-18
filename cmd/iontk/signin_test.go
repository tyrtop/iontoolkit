package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
)

func signInModel(t *testing.T, tenant config.Tenant) tuiModel {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg := &config.Config{Tenants: []config.Tenant{tenant}, LastTenant: tenant.Name}
	return tuiModel{
		cfg:       cfg,
		store:     st,
		tenant:    tenant,
		hasTenant: true,
		selected:  map[string]bool{},
		width:     80,
		height:    24,
	}
}

func menuLabels(m tuiModel) []string {
	var out []string
	for _, e := range m.menuEntries() {
		out = append(out, e.label)
	}
	return out
}

func TestSignInEntryOnlyForBrowserTenants(t *testing.T) {
	tests := []struct {
		name   string
		tenant config.Tenant
		want   bool
	}{
		{"browser tenant", config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}, true},
		{"service account tenant", config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount}, false},
		{"tenant saved before auth modes existed", config.Tenant{Name: "Acme Co", TSGID: "1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signInModel(t, tt.tenant)
			var found bool
			for _, e := range m.menuEntries() {
				if e.action == actSignIn {
					found = true
				}
			}
			if found != tt.want {
				t.Errorf("sign-in entry present = %v, want %v (menu: %v)", found, tt.want, menuLabels(m))
			}
		})
	}
}

func TestAddTenantAsksForTheMethodFirst(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
	m.phase = phaseMenu

	// Walk the menu to "Add Tenant" rather than assuming its index.
	idx := -1
	for i, e := range m.menuEntries() {
		if e.action == actAddTenant {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no Add Tenant entry in %v", menuLabels(m))
	}
	m.menuCursor = idx

	mm, _ := m.onMenuKey("enter")
	got := mm.(tuiModel)
	if got.phase != phaseAuthPick {
		t.Fatalf("phase = %v, want phaseAuthPick", got.phase)
	}
	if len(got.form) != 0 {
		t.Errorf("a form was opened before the method was chosen: %d fields", len(got.form))
	}
}

func TestAuthPickShapesTheTenantForm(t *testing.T) {
	tests := []struct {
		name       string
		cursor     int
		wantFields int
		wantAuth   string
	}{
		{"service account keeps the client id and secret", 0, 4, config.AuthServiceAccount},
		{"browser needs only name and tenant", 1, 2, config.AuthBrowser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
			m.phase = phaseAuthPick
			m.authCursor = tt.cursor

			mm, _ := m.onAuthPickKey("enter")
			got := mm.(tuiModel)
			if got.phase != phaseAddTenant {
				t.Fatalf("phase = %v, want phaseAddTenant", got.phase)
			}
			if len(got.form) != tt.wantFields {
				t.Errorf("form has %d fields, want %d", len(got.form), tt.wantFields)
			}
			if got.formAuth != tt.wantAuth {
				t.Errorf("formAuth = %q, want %q", got.formAuth, tt.wantAuth)
			}
		})
	}
}

func TestBrowserTenantIsSavedWithoutAClientSecret(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Old", TSGID: "9"})
	mm, _ := m.openTenantForm(config.AuthBrowser)
	m = mm.(tuiModel)

	m.form[0].input.SetValue("Acme Co")
	m.form[1].input.SetValue("1984021017")

	mm, _ = m.submitForm()
	m = mm.(tuiModel)
	if m.status != "" {
		t.Fatalf("status = %q, want the form to submit cleanly", m.status)
	}

	saved, ok := m.cfg.Tenant("Acme Co")
	if !ok {
		t.Fatal("tenant was not saved")
	}
	if !saved.UsesBrowser() {
		t.Errorf("Auth = %q, want %q", saved.Auth, config.AuthBrowser)
	}
	if saved.ClientID != "" {
		t.Errorf("ClientID = %q, want empty for a browser tenant", saved.ClientID)
	}
	if v, err := m.store.Secrets.Get(config.ClientSecretKey("1984021017")); err == nil && v != "" {
		t.Errorf("a client secret was stored for a browser tenant: %q", v)
	}
}

func TestBrowserTenantFlagsSkipTheClientSecret(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "left-over-secret"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	browser := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	f := tenantFlags(st, browser, "")
	if !f.BrowserAuth {
		t.Error("BrowserAuth = false, want the tenant's method to carry over")
	}
	if f.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want a browser tenant not to pick up a stale secret", f.ClientSecret)
	}
	if err := f.validateCreds(); err != nil {
		t.Errorf("validateCreds() error = %v, want a browser tenant with a tsg to validate", err)
	}

	sa := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount, ClientID: "svc"}
	if got := tenantFlags(st, sa, "").ClientSecret; got != "left-over-secret" {
		t.Errorf("service account ClientSecret = %q, want the stored secret", got)
	}
}

func TestNeedsSignInOpensTheWaitingScreen(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseLoading

	mm, cmd := m.Update(needsSignInMsg{})
	got := mm.(tuiModel)
	if got.phase != phaseSignIn {
		t.Fatalf("phase = %v, want phaseSignIn", got.phase)
	}
	if !got.signInResume {
		t.Error("signInResume = false, want the interrupted connect to resume after signing in")
	}
	if cmd == nil {
		t.Error("no command returned, want the browser to be opened")
	}
}

func TestSignInWaitingScreenSaysWhatItIsWaitingFor(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.spinner = newSpinner("")

	out := m.viewSignIn()
	if !strings.Contains(out, "waiting for browser") {
		t.Errorf("viewSignIn() = %q, want it to say what it is waiting for", out)
	}
	if !strings.Contains(out, "Acme Co") {
		t.Error("viewSignIn() does not name the tenant being signed in to")
	}
	if !m.wantsSpinner() {
		t.Error("wantsSpinner() = false, want the spinner animating while waiting")
	}
	t.Log("\n" + out)
}

func TestSignedInResumesOrReturns(t *testing.T) {
	tests := []struct {
		name      string
		resume    bool
		wantPhase tuiPhase
	}{
		{"an interrupted connect resumes", true, phaseLoading},
		{"an explicit sign-in returns to the menu", false, phaseMenu},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
			m.phase = phaseSignIn
			m.signInResume = tt.resume
			m.spinner = newSpinner("")

			mm, _ := m.Update(signedInMsg{})
			got := mm.(tuiModel)
			if got.phase != tt.wantPhase {
				t.Errorf("phase = %v, want %v", got.phase, tt.wantPhase)
			}
			if got.signInResume {
				t.Error("signInResume stayed set after the sign-in completed")
			}
		})
	}
}

func TestSignInFailureReturnsToTheMenu(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.signInResume = true

	mm, _ := m.Update(signedInMsg{err: errors.New("browser closed before sign-in completed")})
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want phaseMenu", got.phase)
	}
	if !strings.Contains(got.status, "browser closed") {
		t.Errorf("status = %q, want the browser error surfaced", got.status)
	}
	if got.signInResume {
		t.Error("signInResume stayed set after a failed sign-in, so a later success would connect unexpectedly")
	}
}

func TestEscStopsWaitingForTheBrowser(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.signInResume = true

	mm, _ := m.onKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want phaseMenu", got.phase)
	}
	if got.signInResume {
		t.Error("signInResume stayed set after cancelling")
	}
}

func TestEditingATenantKeepsItsRefreshToken(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "rt-for-tenant"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}

	tenant := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	m := signInModel(t, tenant)
	m.store = st

	mm, _ := m.openEditTenantForm(tenant)
	m = mm.(tuiModel)
	if len(m.form) != 2 {
		t.Fatalf("edit form has %d fields, want 2 for a browser tenant", len(m.form))
	}
	m.form[1].input.SetValue("2")

	mm, _ = m.submitForm()
	m = mm.(tuiModel)

	if v, err := st.Secrets.Get(config.RefreshTokenKey("2")); err != nil || v != "rt-for-tenant" {
		t.Errorf("refresh token under the new tsg = %q (err %v), want it migrated", v, err)
	}
	if v, _ := st.Secrets.Get(config.RefreshTokenKey("1")); v != "" {
		t.Errorf("refresh token still under the old tsg: %q", v)
	}
}

func TestForgettingATenantDropsItsRefreshToken(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	tenant := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "rt"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &config.Config{Tenants: []config.Tenant{tenant}}
	if err := forgetTenant(st, cfg, tenant); err != nil {
		t.Fatalf("forgetTenant() error = %v", err)
	}
	if v, _ := st.Secrets.Get(config.RefreshTokenKey("1")); v != "" {
		t.Errorf("refresh token survived forgetTenant: %q", v)
	}
}
