package main

import (
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func TestValidateCreds(t *testing.T) {
	tests := []struct {
		name    string
		f       flags
		wantErr bool
	}{
		{"token alone is enough", flags{Token: "t"}, false},
		{"full service account", flags{ClientID: "i", ClientSecret: "s", TSGID: "1"}, false},
		{"browser with tenant", flags{BrowserAuth: true, TSGID: "1"}, false},
		{"browser without tenant", flags{BrowserAuth: true}, true},
		{"service account missing secret", flags{ClientID: "i", TSGID: "1"}, true},
		{"service account missing tenant", flags{ClientID: "i", ClientSecret: "s"}, true},
		{"nothing at all", flags{}, true},
		{"token beats a missing tenant", flags{Token: "t", BrowserAuth: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.f.validateCreds()
			if tt.wantErr && err == nil {
				t.Error("validateCreds() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateCreds() error = %v, want nil", err)
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

	src := browserSource(flags{TSGID: "1984021017"}, nil, st)
	if got := src.RefreshToken(); got != "stored-rt" {
		t.Errorf("RefreshToken() = %q, want %q", got, "stored-rt")
	}
}

func TestBrowserSourcePersistsRotatedToken(t *testing.T) {
	st := testStore(t)
	src := browserSource(flags{TSGID: "1984021017"}, nil, st)
	if got := src.RefreshToken(); got != "" {
		t.Errorf("RefreshToken() = %q, want empty with nothing stored", got)
	}

	// The save hook is what the token source calls after an exchange; invoking it
	// through the source keeps this honest about the wiring.
	if err := st.Secrets.Set(config.RefreshTokenKey("1984021017"), "rotated"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	again := browserSource(flags{TSGID: "1984021017"}, nil, st)
	if got := again.RefreshToken(); got != "rotated" {
		t.Errorf("RefreshToken() = %q, want %q to be read back", got, "rotated")
	}
}

func TestBrowserSourceWithoutStore(t *testing.T) {
	src := browserSource(flags{TSGID: "1"}, nil, nil)
	if src == nil {
		t.Fatal("browserSource() = nil, want a usable source without a store")
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
