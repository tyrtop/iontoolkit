package saseauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/logging"
)

func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func TestIdentityFromTokens(t *testing.T) {
	exp := time.Now().Add(8 * time.Hour).Truncate(time.Second)
	tok := &Token{
		IDToken: jwt(t, map[string]any{
			"given_name":  "Ben",
			"family_name": "Hall",
			"name":        "Ben",
			"email":       "ben.hall@example.test",
			"sub":         "1c2b96ea-cde3-4b78-95b9-7d90841ed50e",
		}),
		AccessToken:  jwt(t, map[string]any{"tsg_id": "1984021017", "sub": "1c2b96ea"}),
		RefreshToken: jwt(t, map[string]any{"exp": float64(exp.Unix())}),
		Scope:        "openid profile email tsg_id:1984021017",
		ExpiresIn:    899 * time.Second,
	}

	id := identityFrom(tok)
	if id.Name != "Ben Hall" {
		t.Errorf("Name = %q, want %q built from the given and family names", id.Name, "Ben Hall")
	}
	if id.Email != "ben.hall@example.test" {
		t.Errorf("Email = %q", id.Email)
	}
	if id.TSG != "1984021017" {
		t.Errorf("TSG = %q, want it read from the access token", id.TSG)
	}
	if !id.SessionExpires.Equal(exp) {
		t.Errorf("SessionExpires = %v, want %v from the refresh token", id.SessionExpires, exp)
	}
	left, ok := id.SessionLeft()
	if !ok {
		t.Fatal("SessionLeft() reported no expiry although the token stated one")
	}
	if left < 7*time.Hour || left > 8*time.Hour {
		t.Errorf("SessionLeft() = %v, want roughly 8h", left)
	}
	if got := id.Display(); got != "Ben Hall <ben.hall@example.test>" {
		t.Errorf("Display() = %q", got)
	}
}

func TestIdentityDisplayFallsBack(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		want string
	}{
		{"name and email", Identity{Name: "Ben Hall", Email: "b@e.test"}, "Ben Hall <b@e.test>"},
		{"name only", Identity{Name: "Ben Hall"}, "Ben Hall"},
		{"email only", Identity{Email: "b@e.test"}, "b@e.test"},
		{"subject only", Identity{Subject: "1c2b96ea"}, "1c2b96ea"},
		{"nothing at all", Identity{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.Display(); got != tt.want {
				t.Errorf("Display() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIdentityScopedTo(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		tsg  string
		want bool
	}{
		{"tsg claim matches", Identity{TSG: "123"}, "123", true},
		{"tsg claim differs", Identity{TSG: "none"}, "123", false},
		{"falls back to the scope string", Identity{Scope: "openid profile tsg_id:123"}, "123", true},
		{"scope string lacks the tenant", Identity{Scope: "openid profile tsg_id:none"}, "123", false},
		{"no tenant asked for", Identity{TSG: "none"}, "", true},
		{"nothing to check against is not a failure", Identity{}, "123", true},
		{"a tsg claim wins over the scope string", Identity{TSG: "123", Scope: "tsg_id:none"}, "123", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.ScopedTo(tt.tsg); got != tt.want {
				t.Errorf("ScopedTo(%q) = %v, want %v", tt.tsg, got, tt.want)
			}
		})
	}
}

func TestIdentityFromMalformedTokens(t *testing.T) {
	for _, tok := range []*Token{
		nil,
		{},
		{IDToken: "not-a-jwt"},
		{IDToken: "a.b"},
		{IDToken: "a.!!!notbase64!!!.c"},
		{IDToken: "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte("{not json")) + ".sig"},
	} {
		id := identityFrom(tok)
		if id.Name != "" || id.Email != "" {
			t.Errorf("identityFrom(%v) = %+v, want empty", tok, id)
		}
	}
}

func TestSourceExposesIdentityAndTTL(t *testing.T) {
	src := NewSource("123", "", Options{}, nil)
	if got := src.Identity(); got.Display() != "unknown" {
		t.Errorf("Identity() = %+v before any token, want empty", got)
	}
	if got := src.LastTTL(); got != 0 {
		t.Errorf("LastTTL() = %v before any token, want 0", got)
	}

	tok := &Token{
		IDToken:     jwt(t, map[string]any{"given_name": "Ben", "sub": "abc"}),
		AccessToken: jwt(t, map[string]any{"tsg_id": "123"}),
		ExpiresIn:   899 * time.Second,
	}
	if _, _, err := src.accept(tok, logging.L()); err != nil {
		t.Fatalf("accept() error = %v", err)
	}
	if got := src.Identity().Name; got != "Ben" {
		t.Errorf("Identity().Name = %q, want %q", got, "Ben")
	}
	if got := src.LastTTL(); got != 899*time.Second {
		t.Errorf("LastTTL() = %v, want %v", got, 899*time.Second)
	}
}
