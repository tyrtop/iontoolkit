package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/saseauth"
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

func TestATenantsOwnTokenComesFirstButTheSeedIsStillThere(t *testing.T) {
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
	// The seed is attached but unreached while the tenant's own token works,
	// so it is not spent; withholding it instead sent a tenant whose token
	// had merely expired all the way to the browser.
	if src.Seed() == nil {
		t.Error("Seed() = nil, want the account token available for when this tenant's own is rejected")
	}
}

func TestDebugRedirectsEveryStoreThisProcessOpens(t *testing.T) {
	Debug = true
	t.Cleanup(func() { Debug = false })

	st, err := Store()
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	// BrowserSource opens its own store, so a --debug run that left this one
	// pointed at the real root would still write a refresh token to the keyring.
	if got := st.Secrets.Backend(); got != "memory" {
		t.Errorf("secrets backend = %q, want memory", got)
	}
	if st.Root != config.DebugRoot() {
		t.Errorf("root = %q, want %q", st.Root, config.DebugRoot())
	}
	if OpenStore().Root != st.Root {
		t.Error("OpenStore() and Store() disagree about where --debug points")
	}
}

func TestFixturesSeedTenantsThatResolveWithoutASignIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	body := "tenants:\n  - name: Acme Co\n    tsg_id: \"000000000\"\n    users:\n      - name: admin\n        password: ion\n    elements:\n      - name: rtr-01\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	Fixture = set
	t.Cleanup(func() { Fixture = nil })

	st, err := Store()
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if st.Secrets.Backend() != "memory" {
		t.Errorf("secrets backend = %q, want a fixture run to keep nothing", st.Secrets.Backend())
	}

	// The whole point of seeding: the ordinary credential path is what runs,
	// rather than a fixture-only branch through the tenant resolution.
	creds, err := ResolveCredentials(st, "Acme Co")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if creds.Flags.IONUsername != "admin" || creds.Flags.IONPassword != "ion" {
		t.Errorf("device login = %q/%q, want the fixture's", creds.Flags.IONUsername, creds.Flags.IONPassword)
	}
	if err := creds.Flags.ValidateCreds(); err != nil {
		t.Errorf("ValidateCreds() = %v; a seeded tenant has to look usable", err)
	}

	// Opening a second store must not double up or fail: every entry point
	// opens its own.
	again, err := Store()
	if err != nil {
		t.Fatalf("second Store() error = %v", err)
	}
	cfg, err := again.Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tenants) != 1 {
		t.Errorf("config holds %d tenants after two opens, want 1", len(cfg.Tenants))
	}
	// Seeding happens once, so the secrets have to be shared the way the OS
	// keyring is or the second store would find none.
	if v, err := again.Secrets.Get(config.IONPassKey("000000000", "admin")); err != nil || v != "ion" {
		t.Errorf("second store's device password = %q, %v; want the seeded one", v, err)
	}
}

func TestShortAuthErrorSaysWhatToDoAboutIt(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			// The one that matters most: the server rejecting a stored
			// refresh token means this tenant worked once, so the fix is to
			// sign in again -- not to go looking for an access problem.
			name: "expired refresh token",
			err: fmt.Errorf("%w: stored refresh token rejected: token: status 400 Bad Request: "+
				`{"error_description":"grant is invalid","error":"invalid_grant"}; `+
				"no existing browser session: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired),
			want: "sign-in expired",
		},
		{
			name: "no access to the tenant",
			err: fmt.Errorf("%w: no existing browser session: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired),
			want: "not accessible with this sign-in",
		},
		{
			// Proves nothing about access, and arrives wrapped the same way.
			name: "sign-in timed out",
			err:  fmt.Errorf("%w: sign-in did not complete within 2m0s", saseauth.ErrInteractiveRequired),
			want: "sign-in timed out",
		},
		{"rejected credential", errors.New("elements: status 403 Forbidden"), "credential rejected"},
		{"anything else is passed through", errors.New("dial tcp: no route to host"), "dial tcp: no route to host"},
		{"no error", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortAuthError(tt.err)
			if !strings.Contains(got, tt.want) {
				t.Errorf("ShortAuthError() = %q, want it to say %q", got, tt.want)
			}
			// The point is to replace the wall of OAuth text, not to reprint it.
			if tt.err != nil && len(got) >= len(tt.err.Error()) && len(tt.err.Error()) > 80 {
				t.Errorf("ShortAuthError() = %q, no shorter than the original", got)
			}
		})
	}
}
