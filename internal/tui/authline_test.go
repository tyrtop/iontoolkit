package tui

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

// refreshTokenExpiring builds a refresh token that says it expires at exp.
func refreshTokenExpiring(t *testing.T, exp time.Time) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"exp": float64(exp.Unix())})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func authModel(t *testing.T, auth string) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Auth: auth, Added: true},
	}}
	m.tenant, m.hasTenant = m.cfg.Tenants[0], true
	m.authMode = authBrowser
	if auth != config.AuthBrowser {
		m.authMode = authTenant
	}
	return m
}

func TestAuthLineNamesTheCredentialKind(t *testing.T) {
	sa := authModel(t, config.AuthServiceAccount)
	if got := plain(sa.authLine()); got != "SASE Service Account" {
		t.Errorf("authLine() = %q, want the service account wording", got)
	}

	browser := authModel(t, config.AuthBrowser)
	if got := plain(browser.authLine()); !strings.HasPrefix(got, "Browser Sign-In") {
		t.Errorf("authLine() = %q, want the browser wording", got)
	}
	if strings.Contains(plain(browser.authLine()), "Mode") {
		t.Errorf("authLine() = %q, want no trailing \"Mode\"", plain(browser.authLine()))
	}

	var none tuiModel
	if got := plain(none.authLine()); got != "no tenant selected" {
		t.Errorf("authLine() = %q with no tenant, want %q", got, "no tenant selected")
	}
}

func TestAuthLineColourTracksTheSessionLeft(t *testing.T) {
	// The colour is what makes this readable at a glance, so assert on the code
	// rather than only the text.
	const (
		red    = "\x1b[31m"
		green  = "\x1b[32m"
		yellow = "\x1b[33m"
		orange = "\x1b[91m"
	)
	tests := []struct {
		name      string
		left      time.Duration
		wantCode  string
		wantTimer bool
	}{
		{"hours left is fine", 7 * time.Hour, green, false},
		{"just over an hour is still fine", 61 * time.Minute, green, false},
		{"under an hour warns", 45 * time.Minute, yellow, true},
		{"under fifteen minutes is urgent", 9 * time.Minute, orange, true},
		{"expired", -time.Minute, red, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := authModel(t, config.AuthBrowser)
			if err := m.store.Secrets.Set(config.RefreshTokenKey("111"),
				refreshTokenExpiring(t, time.Now().Add(tt.left))); err != nil {
				t.Fatalf("seed: %v", err)
			}

			line := m.authLine()
			if !strings.Contains(line, tt.wantCode) {
				t.Errorf("authLine() = %q, want colour %q", line, strings.TrimPrefix(tt.wantCode, "\x1b"))
			}
			text := plain(line)
			hasTimer := strings.Contains(text, "left")
			if hasTimer != tt.wantTimer {
				t.Errorf("authLine() = %q, timer present = %v, want %v", text, hasTimer, tt.wantTimer)
			}
		})
	}
}

func TestAuthLineIsRedWhenASignInIsNeeded(t *testing.T) {
	t.Run("nothing stored", func(t *testing.T) {
		m := authModel(t, config.AuthBrowser)
		got := plain(m.authLine())
		if !strings.Contains(got, "sign-in needed") {
			t.Errorf("authLine() = %q, want it to say a sign-in is needed", got)
		}
		if !strings.Contains(m.authLine(), "\x1b[31m") {
			t.Error("want red when there is nothing to mint from")
		}
	})

	t.Run("credential known dead", func(t *testing.T) {
		m := authModel(t, config.AuthBrowser)
		if err := m.store.Secrets.Set(config.RefreshTokenKey("111"),
			refreshTokenExpiring(t, time.Now().Add(8*time.Hour))); err != nil {
			t.Fatalf("seed: %v", err)
		}
		m.tokenDead = "sign-in expired"
		if !strings.Contains(m.authLine(), "\x1b[31m") {
			t.Error("want red once the credential is known dead, whatever the token claims")
		}
	})
}

func TestSessionLeftPrefersWhatThisRunSaw(t *testing.T) {
	m := authModel(t, config.AuthBrowser)
	if err := m.store.Secrets.Set(config.RefreshTokenKey("111"),
		refreshTokenExpiring(t, time.Now().Add(30*time.Minute))); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A token minted this run supersedes whatever is on disk.
	m.ident = saseauth.Identity{TSG: "111", SessionExpires: time.Now().Add(8 * time.Hour)}
	left, ok := m.sessionLeft()
	if !ok {
		t.Fatal("sessionLeft() reported nothing")
	}
	if left < 7*time.Hour {
		t.Errorf("sessionLeft() = %v, want the freshly minted figure", left)
	}

	// An identity for a different tenant must not be used for this one.
	m.ident = saseauth.Identity{TSG: "999", SessionExpires: time.Now().Add(8 * time.Hour)}
	left, ok = m.sessionLeft()
	if !ok {
		t.Fatal("sessionLeft() reported nothing")
	}
	if left > time.Hour {
		t.Errorf("sessionLeft() = %v, want the stored figure for this tenant", left)
	}
}

func TestSessionLeftIgnoresServiceAccounts(t *testing.T) {
	m := authModel(t, config.AuthServiceAccount)
	if _, ok := m.sessionLeft(); ok {
		t.Error("a service account reported a browser session lifetime")
	}
}

func TestMenuOffersSignInBeforeAndRefreshAfter(t *testing.T) {
	t.Run("before any sign-in", func(t *testing.T) {
		m := authModel(t, config.AuthBrowser)
		joined := strings.Join(menuLabels(m), " | ")
		if !strings.Contains(joined, "sign in with browser") {
			t.Errorf("want the invitation to sign in: %s", joined)
		}
		if strings.Contains(joined, "refresh browser token") {
			t.Errorf("want no refresh entry before signing in: %s", joined)
		}
	})

	t.Run("after signing in", func(t *testing.T) {
		m := authModel(t, config.AuthBrowser)
		seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Tractor Supply"})
		joined := strings.Join(menuLabels(m), " | ")
		if strings.Contains(joined, "sign in with browser") {
			t.Errorf("the sign-in invitation survived signing in: %s", joined)
		}
		if !strings.Contains(joined, "refresh browser token") {
			t.Errorf("want a refresh entry once signed in: %s", joined)
		}
	})
}

func TestTokenRefreshUpdatesTheLineAndDropsTheOldClient(t *testing.T) {
	m := authModel(t, config.AuthBrowser)
	m.spinner = ui.NewSpinner("")
	setTestClient(&m, nil)
	m.pool.set("111", nil)

	mm, _ := m.Update(tokenRefreshedMsg{
		ident: saseauth.Identity{TSG: "111", SessionExpires: time.Now().Add(8 * time.Hour)},
		ttl:   899 * time.Second,
	})
	got := mm.(tuiModel)

	if got.status != "token refreshed" {
		t.Errorf("status = %q, want it reported", got.status)
	}
	if got.tokenTTL != 899*time.Second {
		t.Errorf("tokenTTL = %v, want the new lifetime", got.tokenTTL)
	}
	if got.pool.get("111") != nil {
		t.Error("the client holding the old token was kept")
	}
	if !strings.Contains(got.authLine(), "\x1b[32m") {
		t.Error("the line did not go green after a successful refresh")
	}
}

func TestTokenRefreshFallsBackToTheBrowserWhenSpent(t *testing.T) {
	m := authModel(t, config.AuthBrowser)
	m.spinner = ui.NewSpinner("")
	m.phase = phaseMenu

	mm, cmd := m.Update(tokenRefreshedMsg{err: saseauth.ErrInteractiveRequired})
	got := mm.(tuiModel)
	if got.phase != phaseSignIn {
		t.Errorf("phase = %v, want the browser opened when the stored token is spent", got.phase)
	}
	if cmd == nil {
		t.Error("no command returned")
	}
}

func TestRenderMenuWithCredentialLine(t *testing.T) {
	m := authModel(t, config.AuthBrowser)
	m.width, m.height = 90, 30
	m.spinner = ui.NewSpinner("")
	seedKnown(t, m, config.KnownTenant{TSGID: "111", Name: "Tractor Supply"})
	if err := m.store.Secrets.Set(config.RefreshTokenKey("111"),
		refreshTokenExpiring(t, time.Now().Add(42*time.Minute))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m.cacheState, m.cacheCount, m.cacheAt = cacheUpdated, 5613, time.Now()

	out := plain(m.viewMenu())
	for _, want := range []string{"5613 elements cached", "Browser Sign-In", "41m left", "refresh browser token"} {
		if !strings.Contains(out, want) {
			t.Errorf("the menu is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewMenu())
}
