package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestWarmingATenantSpendsTheCredentialsAlreadyHeld(t *testing.T) {
	st := testStore(t)
	if err := st.Secrets.Set(config.AccountRefreshTokenKey(), "account-rt"); err != nil {
		t.Fatalf("seed account token: %v", err)
	}
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "tenant-rt"); err != nil {
		t.Fatalf("seed tenant token: %v", err)
	}

	src := tenantSource(st, nil, "1")
	if got := src.RefreshToken(); got != "tenant-rt" {
		t.Errorf("RefreshToken() = %q, want the stored token tried before any browser", got)
	}
	if src.Seed() == nil {
		t.Error("Seed() = nil, want the account token offered too, or login opens a second window")
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
	if v, err := again.Secrets.Get(config.IONPassKey("000000000", "admin")); err != nil || v != "ion" {
		t.Errorf("second store's device password = %q, %v; want the seeded one", v, err)
	}
}

func TestATenantWithNoSignInReadsTheSameWhateverTheAccountCredentialDid(t *testing.T) {
	const (
		good = "1984021017"
		bad  = "1538107912"
	)

	tests := []struct {
		name string
		seed func(t *testing.T, tokenURL string, client *http.Client) *saseauth.Seed
	}{
		{
			name: "no account credential at all",
			seed: func(*testing.T, string, *http.Client) *saseauth.Seed { return nil },
		},
		{
			name: "another tenant narrowed it first",
			seed: func(t *testing.T, tokenURL string, client *http.Client) *saseauth.Seed {
				s := saseauth.NewSeed("acct")
				opt := saseauth.Options{TokenURL: tokenURL, Client: client, Seed: s}
				if _, _, err := saseauth.NewSource(good, "", opt, nil).Token(context.Background()); err != nil {
					t.Fatalf("the first tenant could not narrow the account credential: %v", err)
				}
				return s
			},
		},
		{
			name: "it was refused outright",
			seed: func(*testing.T, string, *http.Client) *saseauth.Seed {
				return saseauth.NewSeed("dead")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				if r.PostFormValue("refresh_token") == "acct" {
					w.Write([]byte(`{"access_token":"at","refresh_token":"narrowed","expires_in":899}`))
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant"}`))
			}))
			defer srv.Close()

			opt := saseauth.Options{
				TokenURL: srv.URL,
				Client:   srv.Client(),
				Seed:     tt.seed(t, srv.URL, srv.Client()),
			}
			_, _, err := saseauth.NewSource(bad, "", opt, nil).Token(context.Background())
			if err == nil {
				t.Fatal("Token() succeeded for a tenant with no sign-in of its own")
			}
			if got := ShortAuthError(err); got != "no sign-in for this tenant" {
				t.Errorf("ShortAuthError() = %q, want %q — the message must not report what the "+
					"shared account credential did as a verdict on this tenant", got, "no sign-in for this tenant")
			}
			if !errors.Is(err, saseauth.ErrInteractiveRequired) {
				t.Errorf("Token() error = %v, want it to still wrap ErrInteractiveRequired so the TUI offers a sign-in", err)
			}
		})
	}
}

func TestShortAuthErrorSaysWhatToDoAboutIt(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "expired refresh token",
			err: fmt.Errorf("%w: stored refresh token rejected: token: status 400 Bad Request: "+
				`{"error_description":"grant is invalid","error":"invalid_grant"}; `+
				"no existing browser session: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired),
			want: "sign-in expired",
		},
		{
			name: "never signed in to this tenant",
			err: fmt.Errorf("%w: %w; %w: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired, saseauth.ErrNoCredential, saseauth.ErrNoSession),
			want: "no sign-in for this tenant",
		},
		{
			name: "another tenant had already spent the account credential",
			err: fmt.Errorf("%w: %w; account credential rejected: token: status 400 Bad Request: "+
				`{"error":"invalid_grant"}`+"; %w: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired, saseauth.ErrNoCredential, saseauth.ErrNoSession),
			want: "no sign-in for this tenant",
		},
		{
			name: "nothing silent is left to try",
			err: fmt.Errorf("%w: %w: authorization failed: interaction_required",
				saseauth.ErrInteractiveRequired, saseauth.ErrNoSession),
			want: "sign-in needed",
		},
		{
			name: "the authorization server refused an interactive sign-in",
			err:  errors.New("authorization failed: login_required: no access to this tsg"),
			want: "not accessible with this sign-in",
		},
		{
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
			if tt.err != nil && len(got) >= len(tt.err.Error()) && len(tt.err.Error()) > 80 {
				t.Errorf("ShortAuthError() = %q, no shorter than the original", got)
			}
		})
	}
}
