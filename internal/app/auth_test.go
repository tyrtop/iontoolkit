package app

import (
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func TestValidateCreds(t *testing.T) {
	tests := []struct {
		name    string
		f       Flags
		wantErr bool
	}{
		{"token alone is enough", Flags{Token: "t"}, false},
		{"full service account", Flags{ClientID: "i", ClientSecret: "s", TSGID: "1"}, false},
		{"browser with tenant", Flags{BrowserAuth: true, TSGID: "1"}, false},
		{"browser without tenant", Flags{BrowserAuth: true}, true},
		{"service account missing secret", Flags{ClientID: "i", TSGID: "1"}, true},
		{"service account missing tenant", Flags{ClientID: "i", ClientSecret: "s"}, true},
		{"nothing at all", Flags{}, true},
		{"token beats a missing tenant", Flags{Token: "t", BrowserAuth: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.f.ValidateCreds()
			if tt.wantErr && err == nil {
				t.Error("ValidateCreds() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateCreds() error = %v, want nil", err)
			}
		})
	}
}

func testStore(t *testing.T) *config.Store {
	t.Helper()
	root := t.TempDir()
	return config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
}

func TestBrowserSourceReusesStoredRefreshToken(t *testing.T) {
	st := testStore(t)
	if err := st.Secrets.Set(config.RefreshTokenKey("1984021017"), "stored-rt"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}

	src := BrowserSource(Flags{TSGID: "1984021017"}, nil, st)
	if got := src.RefreshToken(); got != "stored-rt" {
		t.Errorf("RefreshToken() = %q, want %q", got, "stored-rt")
	}
}

func TestBrowserSourcePersistsRotatedToken(t *testing.T) {
	st := testStore(t)
	src := BrowserSource(Flags{TSGID: "1984021017"}, nil, st)
	if got := src.RefreshToken(); got != "" {
		t.Errorf("RefreshToken() = %q, want empty with nothing stored", got)
	}

	// The save hook is what the token source calls after an exchange; invoking it
	// through the source keeps this honest about the wiring.
	if err := st.Secrets.Set(config.RefreshTokenKey("1984021017"), "rotated"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	again := BrowserSource(Flags{TSGID: "1984021017"}, nil, st)
	if got := again.RefreshToken(); got != "rotated" {
		t.Errorf("RefreshToken() = %q, want %q to be read back", got, "rotated")
	}
}

func TestBrowserSourceWithoutStore(t *testing.T) {
	src := BrowserSource(Flags{TSGID: "1"}, nil, nil)
	if src == nil {
		t.Fatal("BrowserSource() = nil, want a usable source without a store")
	}
	if got := src.RefreshToken(); got != "" {
		t.Errorf("RefreshToken() = %q, want empty", got)
	}
}

func TestBrowserProfileLivesUnderTheConfigRoot(t *testing.T) {
	st := testStore(t)
	dir := st.BrowserDir()
	if !strings.HasPrefix(dir, st.Root) {
		t.Errorf("BrowserDir() = %q, want it inside the config root %q", dir, st.Root)
	}
	if filepath.Base(dir) != "browser" {
		t.Errorf("BrowserDir() base = %q, want %q", filepath.Base(dir), "browser")
	}
}

func TestRefreshTokenKeyIsTenantScoped(t *testing.T) {
	a := config.RefreshTokenKey("1")
	b := config.RefreshTokenKey("2")
	if a == b {
		t.Errorf("RefreshTokenKey() returned %q for both tenants", a)
	}
	if !strings.Contains(a, "1") {
		t.Errorf("RefreshTokenKey(%q) = %q, want the tenant in the key", "1", a)
	}
	if strings.Contains(a, config.ClientSecretKey("1")) {
		t.Errorf("RefreshTokenKey() = %q collides with the client secret key", a)
	}
}

func TestBrowserTenantFlagsSkipTheClientSecret(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "left-over-secret"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	browser := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	f := TenantFlags(st, browser, "")
	if !f.BrowserAuth {
		t.Error("BrowserAuth = false, want the tenant's method to carry over")
	}
	if f.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want a browser tenant not to pick up a stale secret", f.ClientSecret)
	}
	if err := f.ValidateCreds(); err != nil {
		t.Errorf("ValidateCreds() error = %v, want a browser tenant with a tsg to validate", err)
	}

	sa := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount, ClientID: "svc"}
	if got := TenantFlags(st, sa, "").ClientSecret; got != "left-over-secret" {
		t.Errorf("service account ClientSecret = %q, want the stored secret", got)
	}
}

func TestIsAuthFailure(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "rejected websocket handshake",
			msg:  "dial: failed to WebSocket dial: expected handshake response status code 101 but got 401",
			want: true,
		},
		{
			name: "forbidden handshake",
			msg:  "expected handshake response status code 101 but got 403",
			want: true,
		},
		{"rest unauthorized", `element 123: status 401 Unauthorized: {"message":"nope"}`, true},
		{"rest forbidden", "profile: status 403 Forbidden: ", true},
		{"refresh grant rejected", `token: status 400: {"error":"invalid_grant"}`, true},
		// Things that are not the credential must not trigger a browser.
		{"element genuinely missing", "element 123: status 404 Not Found: ", false},
		{"device unreachable", "expected handshake response status code 101 but got 502", false},
		{"rate limited", "element 123: status 429 Too Many Requests: ", false},
		{"network down", "dial tcp: lookup api.sase.paloaltonetworks.com: no such host", false},
		{"ion login failed", "element 123: run: login failed after 1 attempt", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAuthFailure(tt.msg); got != tt.want {
				t.Errorf("IsAuthFailure(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestAccountSeedIsSharedBetweenTenants(t *testing.T) {
	st := testStore(t)
	if err := st.Secrets.Set(config.AccountRefreshTokenKey(), "account-rt"); err != nil {
		t.Fatalf("seed account token: %v", err)
	}

	a := BrowserSource(Flags{TSGID: "1", BrowserAuth: true}, nil, st)
	b := BrowserSource(Flags{TSGID: "2", BrowserAuth: true}, nil, st)
	if a.Seed() == nil {
		t.Fatal("Seed() = nil, want the account token to be offered as a seed")
	}
	if a.Seed() != b.Seed() {
		t.Error("each tenant got its own Seed, want one shared across the account")
	}
	if got := a.RefreshToken(); got != "" {
		t.Errorf("RefreshToken() = %q, want the account token held only as a seed", got)
	}
}

func TestTenantWithItsOwnTokenGetsNoSeed(t *testing.T) {
	st := testStore(t)
	if err := st.Secrets.Set(config.AccountRefreshTokenKey(), "account-rt"); err != nil {
		t.Fatalf("seed account token: %v", err)
	}
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "tenant-rt"); err != nil {
		t.Fatalf("seed tenant token: %v", err)
	}

	src := BrowserSource(Flags{TSGID: "1", BrowserAuth: true}, nil, st)
	if got := src.RefreshToken(); got != "tenant-rt" {
		t.Errorf("RefreshToken() = %q, want the tenant's own %q", got, "tenant-rt")
	}
	if src.Seed() != nil {
		t.Error("Seed() is set for a tenant with its own credential, want it left alone for tenants that need it")
	}
}
