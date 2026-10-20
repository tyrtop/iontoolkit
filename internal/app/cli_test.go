package app

import (
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

func clearCredEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SCM_TOKEN", "SCM_CLIENT_ID", "SCM_CLIENT_SECRET", "SCM_TSG_ID",
		"ION_USER", "ION_PASS",
	} {
		t.Setenv(k, "")
	}
}

func seedStore(t *testing.T, cfg *config.Config) *config.Store {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return st
}

func serviceTenant(name, tsg string) config.Tenant {
	return config.Tenant{Name: name, TSGID: tsg, ClientID: "client-" + tsg, Auth: config.AuthServiceAccount}
}

func TestResolveCredentialsOrder(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		tenant   string
		env      map[string]string
		wantFrom CredSource
		wantTSG  string
	}{
		{
			name:     "--tenant beats everything",
			cfg:      &config.Config{LastTenant: "Globex", Tenants: []config.Tenant{serviceTenant("Acme", "1"), serviceTenant("Globex", "2")}},
			tenant:   "acme",
			env:      map[string]string{"SCM_CLIENT_ID": "e", "SCM_CLIENT_SECRET": "e", "SCM_TSG_ID": "9"},
			wantFrom: FromFlag,
			wantTSG:  "1",
		},
		{
			name:     "the environment beats a saved tenant",
			cfg:      &config.Config{LastTenant: "Acme", Tenants: []config.Tenant{serviceTenant("Acme", "1")}},
			env:      map[string]string{"SCM_CLIENT_ID": "e", "SCM_CLIENT_SECRET": "e", "SCM_TSG_ID": "9"},
			wantFrom: FromEnv,
			wantTSG:  "9",
		},
		{
			name:     "SCM_TOKEN alone is enough to stay on the environment",
			cfg:      &config.Config{LastTenant: "Acme", Tenants: []config.Tenant{serviceTenant("Acme", "1")}},
			env:      map[string]string{"SCM_TOKEN": "t"},
			wantFrom: FromEnv,
		},
		{
			name:     "a half-set environment falls through to the last tenant",
			cfg:      &config.Config{LastTenant: "Globex", Tenants: []config.Tenant{serviceTenant("Acme", "1"), serviceTenant("Globex", "2")}},
			env:      map[string]string{"SCM_CLIENT_ID": "e"},
			wantFrom: FromLast,
			wantTSG:  "2",
		},
		{
			name:     "the only tenant needs no last_tenant",
			cfg:      &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1")}},
			wantFrom: FromOnly,
			wantTSG:  "1",
		},
		{
			name:     "a stale last_tenant still resolves the only tenant",
			cfg:      &config.Config{LastTenant: "Deleted Co", Tenants: []config.Tenant{serviceTenant("Acme", "1")}},
			wantFrom: FromOnly,
			wantTSG:  "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			st := seedStore(t, tt.cfg)
			for _, ten := range tt.cfg.Tenants {
				if err := st.Secrets.Set(config.ClientSecretKey(ten.TSGID), "secret-"+ten.TSGID); err != nil {
					t.Fatalf("seed secret: %v", err)
				}
			}

			got, err := ResolveCredentials(st, tt.tenant)
			if err != nil {
				t.Fatalf("ResolveCredentials() error = %v", err)
			}
			if got.From != tt.wantFrom {
				t.Errorf("From = %q, want %q", got.From, tt.wantFrom)
			}
			if tt.wantTSG != "" && got.Flags.TSGID != tt.wantTSG {
				t.Errorf("TSGID = %q, want %q", got.Flags.TSGID, tt.wantTSG)
			}
		})
	}
}

func TestResolveCredentialsReadsTheKeyring(t *testing.T) {
	clearCredEnv(t)
	st := seedStore(t, &config.Config{LastTenant: "Acme", Tenants: []config.Tenant{serviceTenant("Acme", "1")}})
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "stored-secret"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	if err := st.Secrets.Set(config.IONPassKey("1", "admin"), "stored-pass"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	cfg, _ := st.Config()
	cfg.Tenants[0].AddUser("admin", true)
	if err := st.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, err := ResolveCredentials(st, "")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if got.Flags.ClientSecret != "stored-secret" {
		t.Errorf("ClientSecret = %q, want the keyring entry", got.Flags.ClientSecret)
	}
	if got.Flags.IONUsername != "admin" || got.Flags.IONPassword != "stored-pass" {
		t.Errorf("device login = %q/%q, want admin/stored-pass", got.Flags.IONUsername, got.Flags.IONPassword)
	}
	if err := got.Flags.Validate(); err == nil {
		t.Error("Validate() error = nil with no elements, want the usual element check still to apply")
	}
}

func TestResolveCredentialsPrefersTheEnvironmentDeviceLogin(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("ION_USER", "bob")
	st := seedStore(t, &config.Config{LastTenant: "Acme", Tenants: []config.Tenant{serviceTenant("Acme", "1")}})
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	if err := st.Secrets.Set(config.IONPassKey("1", "bob"), "bobs-pass"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	cfg, _ := st.Config()
	cfg.Tenants[0].AddUser("admin", true)
	if err := st.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, err := ResolveCredentials(st, "")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if got.Flags.IONUsername != "bob" {
		t.Errorf("IONUsername = %q, want ION_USER to win over the tenant default", got.Flags.IONUsername)
	}
	if got.Flags.IONPassword != "bobs-pass" {
		t.Errorf("IONPassword = %q, want the entry stored for bob", got.Flags.IONPassword)
	}
}

func TestResolveCredentialsDropsAnEnvironmentTokenForAnExplicitTenant(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("SCM_TOKEN", "env-token")
	st := seedStore(t, &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1"), serviceTenant("Globex", "2")}})
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	got, err := ResolveCredentials(st, "Acme")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if got.Flags.Token != "" {
		t.Errorf("Token = %q, want --tenant to override SCM_TOKEN", got.Flags.Token)
	}
	if !strings.Contains(strings.Join(got.Lines(), "\n"), "SCM_TOKEN") {
		t.Errorf("Lines() = %q, want the ignored token said out loud", got.Lines())
	}
}

func TestResolveCredentialsErrors(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *config.Config
		tenant string
		want   []string
	}{
		{
			name:   "unknown name lists the saved tenants",
			cfg:    &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1"), serviceTenant("Globex", "2")}},
			tenant: "acne",
			want:   []string{"acne", "Acme", "Globex"},
		},
		{
			name: "nothing saved and nothing in the environment",
			cfg:  &config.Config{},
			want: []string{"no tenants are saved", "SCM_CLIENT_ID"},
		},
		{
			name: "several tenants and no way to choose",
			cfg:  &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1"), serviceTenant("Globex", "2")}},
			want: []string{"Acme", "Globex"},
		},
		{
			name:   "a tenant whose secret is gone",
			cfg:    &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1")}},
			tenant: "Acme",
			want:   []string{"Acme", "no usable credential"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredEnv(t)
			st := seedStore(t, tt.cfg)

			_, err := ResolveCredentials(st, tt.tenant)
			if err == nil {
				t.Fatal("ResolveCredentials() error = nil, want an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

func TestResolveCredentialsWithoutAStore(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("SCM_TOKEN", "t")

	got, err := ResolveCredentials(nil, "")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v, want the environment to work with no store", err)
	}
	if got.From != FromEnv || got.Flags.Token != "t" {
		t.Errorf("From = %q, Token = %q, want the environment", got.From, got.Flags.Token)
	}
	if _, err := ResolveCredentials(nil, "Acme"); err == nil {
		t.Error("ResolveCredentials() error = nil for --tenant with no store, want an error rather than a silent environment run")
	}
}

func TestCredentialLinesNameThePlaintextStore(t *testing.T) {
	clearCredEnv(t)
	cfg := &config.Config{Tenants: []config.Tenant{serviceTenant("Acme", "1")}}

	st := seedStore(t, cfg)
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s3cr3t"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	got, err := ResolveCredentials(st, "")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	lines := strings.Join(got.Lines(), "\n")
	for _, want := range []string{"Acme", "plaintext", "secrets.json"} {
		if !strings.Contains(lines, want) {
			t.Errorf("Lines() = %q, want it to mention %q", lines, want)
		}
	}
	if strings.Contains(lines, "s3cr3t") {
		t.Errorf("Lines() = %q, want no secret in it", lines)
	}

	root := t.TempDir()
	keyringStore := config.OpenAt(root, fakeKeyring{})
	if err := keyringStore.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	quiet, err := ResolveCredentials(keyringStore, "")
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if lines := strings.Join(quiet.Lines(), "\n"); strings.Contains(lines, "plaintext") {
		t.Errorf("Lines() = %q, want no warning for a keyring", lines)
	}
}

type fakeKeyring struct{}

func (fakeKeyring) Backend() string            { return "keyring" }
func (fakeKeyring) Get(string) (string, error) { return "kept", nil }
func (fakeKeyring) Set(string, string) error   { return nil }
func (fakeKeyring) Delete(string) error        { return nil }

func TestHeadlessFlagsCarryThePagerHandling(t *testing.T) {
	clearCredEnv(t)
	f := BaseFlags()

	if f.PagerOff != "set paging off" {
		t.Errorf("PagerOff = %q, want the syntax proven on ION 6.3.4", f.PagerOff)
	}
	if len(f.PagerPrompts) == 0 {
		t.Error("PagerPrompts is empty, want the scraper armed for firmware that ignores --pager-off")
	}
}

func TestApplyDisplaySettingsReadsTheStore(t *testing.T) {
	clearCredEnv(t)
	t.Cleanup(func() {
		_ = clock.Use(clock.UTC)
		ui.Apply(ui.Default())
	})

	st := seedStore(t, &config.Config{
		LastTenant: "Acme",
		Tenants:    []config.Tenant{serviceTenant("Acme", "1")},
		Zone:       "America/Chicago",
		Theme:      "mono",
	})
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "shh"); err != nil {
		t.Fatal(err)
	}
	ApplyDisplaySettings(st, nil)

	if got := clock.Name(); got != "America/Chicago" {
		t.Errorf("clock.Name() = %q, want the saved zone", got)
	}
	if got := ui.CurrentTheme().Name; got != "mono" {
		t.Errorf("CurrentTheme() = %q, want the saved theme", got)
	}
}

func TestABadZoneDoesNotStopACommand(t *testing.T) {
	clearCredEnv(t)
	t.Cleanup(func() { _ = clock.Use(clock.UTC) })

	st := seedStore(t, &config.Config{
		LastTenant: "Acme",
		Tenants:    []config.Tenant{serviceTenant("Acme", "1")},
		Zone:       "Mars/Olympus_Mons",
	})
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "shh"); err != nil {
		t.Fatal(err)
	}
	ApplyDisplaySettings(st, nil)
	if got := clock.Name(); got != clock.UTC {
		t.Errorf("clock.Name() = %q, want the UTC fallback", got)
	}
	if clock.Problem() == "" {
		t.Error("nothing recorded why the zone was not used")
	}
}
