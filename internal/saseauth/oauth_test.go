package saseauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewPKCEChallengeMatchesVerifier(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		p, err := newPKCE()
		if err != nil {
			t.Fatalf("newPKCE() error = %v", err)
		}
		sum := sha256.Sum256([]byte(p.verifier))
		want := base64.RawURLEncoding.EncodeToString(sum[:])
		if p.challenge != want {
			t.Errorf("challenge = %q, want %q", p.challenge, want)
		}
		if len(p.challenge) != 43 {
			t.Errorf("challenge length = %d, want 43", len(p.challenge))
		}
		if n := len(p.verifier); n < 43 || n > 128 {
			t.Errorf("verifier length = %d, want within RFC 7636 range 43..128", n)
		}
		if strings.ContainsAny(p.verifier, "+/=") {
			t.Errorf("verifier %q is not base64url", p.verifier)
		}
		if seen[p.verifier] {
			t.Errorf("verifier %q repeated across calls", p.verifier)
		}
		seen[p.verifier] = true
	}
}

func TestScopeFor(t *testing.T) {
	tests := []struct {
		name string
		tsg  string
		want string
	}{
		{"with tsg", "1984021017", "openid profile email tsg_id:1984021017"},
		{"empty tsg", "", "openid profile email tsg_id:none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scopeFor(tt.tsg); got != tt.want {
				t.Errorf("scopeFor(%q) = %q, want %q", tt.tsg, got, tt.want)
			}
		})
	}
}

func TestAuthorizeURL(t *testing.T) {
	p := pkce{verifier: "v", challenge: "chal", state: "st"}

	t.Run("interactive", func(t *testing.T) {
		raw := p.authorizeURL(AuthorizeURL, "123", false)
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got := u.Scheme + "://" + u.Host + u.Path; got != AuthorizeURL {
			t.Errorf("endpoint = %q, want %q", got, AuthorizeURL)
		}
		q := u.Query()
		want := map[string]string{
			"client_id":             ClientID,
			"response_type":         "code",
			"redirect_uri":          RedirectURI,
			"scope":                 "openid profile email tsg_id:123",
			"state":                 "st",
			"code_challenge":        "chal",
			"code_challenge_method": "S256",
		}
		for k, v := range want {
			if got := q.Get(k); got != v {
				t.Errorf("%s = %q, want %q", k, got, v)
			}
		}
		if q.Has("prompt") {
			t.Errorf("prompt = %q, want absent for interactive login", q.Get("prompt"))
		}
		if q.Has("client_secret") {
			t.Error("client_secret must never appear: scm is a public client")
		}
		if strings.Contains(raw, p.verifier+"&") || q.Has("code_verifier") {
			t.Error("verifier must not leave the process in the authorize URL")
		}
	})

	t.Run("silent", func(t *testing.T) {
		u, err := url.Parse(p.authorizeURL(AuthorizeURL, "123", true))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := u.Query().Get("prompt"); got != "none" {
			t.Errorf("prompt = %q, want %q", got, "none")
		}
	})
}

func TestCodeFromCallback(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		state   string
		want    string
		wantErr string
	}{
		{
			name:  "valid",
			raw:   RedirectURI + "?code=sWN8GTm91g16GGtBX3_zYHBzbs8&iss=https%3A%2F%2Fauth.apps.paloaltonetworks.com&state=st",
			state: "st",
			want:  "sWN8GTm91g16GGtBX3_zYHBzbs8",
		},
		{
			name:  "state unchecked when caller has none",
			raw:   RedirectURI + "?code=abc&state=whatever",
			state: "",
			want:  "abc",
		},
		{
			name:  "surrounding whitespace tolerated",
			raw:   "  " + RedirectURI + "?code=abc&state=st\n",
			state: "st",
			want:  "abc",
		},
		{
			name:    "state mismatch",
			raw:     RedirectURI + "?code=abc&state=other",
			state:   "st",
			wantErr: "state mismatch",
		},
		{
			name:    "error without description",
			raw:     RedirectURI + "?error=login_required",
			state:   "st",
			wantErr: "login_required",
		},
		{
			name:    "error with description",
			raw:     RedirectURI + "?error=access_denied&error_description=user+aborted",
			state:   "st",
			wantErr: "user aborted",
		},
		{
			name:    "error wins over code",
			raw:     RedirectURI + "?code=abc&error=login_required&state=st",
			state:   "st",
			wantErr: "login_required",
		},
		{
			name:    "no code",
			raw:     RedirectURI + "?iss=whatever",
			state:   "",
			wantErr: "no code parameter",
		},
		{
			name:    "unparseable",
			raw:     "http://%zz",
			state:   "",
			wantErr: "parse callback url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CodeFromCallback(tt.raw, tt.state)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CodeFromCallback() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CodeFromCallback() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("code = %q, want %q", got, tt.want)
			}
		})
	}
}

func tokenServer(t *testing.T, status int, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		got = r.PostForm
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want form encoding", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestExchangeCode(t *testing.T) {
	const body = `{"access_token":"at","refresh_token":"rt","scope":"openid profile email tsg_id:123","expires_in":899,"token_type":"Bearer"}`
	srv, form := tokenServer(t, http.StatusOK, body)

	tok, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if tok.AccessToken != "at" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "at")
	}
	if tok.RefreshToken != "rt" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "rt")
	}
	if tok.ExpiresIn != 899*time.Second {
		t.Errorf("ExpiresIn = %v, want %v", tok.ExpiresIn, 899*time.Second)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"code_verifier": "the-verifier",
		"redirect_uri":  RedirectURI,
		"client_id":     ClientID,
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("form %s = %q, want %q", k, got, v)
		}
	}
	if form.Has("client_secret") {
		t.Error("form carries client_secret, but scm is a public client")
	}
}

func TestRefresh(t *testing.T) {
	t.Run("rotated refresh token is adopted", func(t *testing.T) {
		srv, form := tokenServer(t, http.StatusOK, `{"access_token":"at2","refresh_token":"rt2","expires_in":899}`)
		tok, err := Refresh(context.Background(), srv.Client(), srv.URL, "rt1", "123")
		if err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
		if tok.RefreshToken != "rt2" {
			t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "rt2")
		}
		if got := form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want %q", got, "refresh_token")
		}
		if got := form.Get("refresh_token"); got != "rt1" {
			t.Errorf("refresh_token = %q, want %q", got, "rt1")
		}
		if got := form.Get("scope"); got != "openid profile email tsg_id:123" {
			t.Errorf("scope = %q, want the tsg-scoped value", got)
		}
	})

	t.Run("omitted refresh token is kept", func(t *testing.T) {
		srv, _ := tokenServer(t, http.StatusOK, `{"access_token":"at2","expires_in":899}`)
		tok, err := Refresh(context.Background(), srv.Client(), srv.URL, "rt1", "123")
		if err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
		if tok.RefreshToken != "rt1" {
			t.Errorf("RefreshToken = %q, want the original %q to be reused", tok.RefreshToken, "rt1")
		}
	})
}

func TestExchangeErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"expired code", http.StatusBadRequest, `{"error":"invalid_grant"}`, "invalid_grant"},
		{"server error surfaces status", http.StatusInternalServerError, `nope`, "500"},
		{"malformed json", http.StatusOK, `{`, "json decode"},
		{"no access_token", http.StatusOK, `{"expires_in":899}`, "no access_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := tokenServer(t, tt.status, tt.body)
			_, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "c", "v")
			if err == nil {
				t.Fatalf("ExchangeCode() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestExchangeCodeContextCancelled(t *testing.T) {
	srv, _ := tokenServer(t, http.StatusOK, `{"access_token":"at"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExchangeCode(ctx, srv.Client(), srv.URL, "c", "v"); err == nil {
		t.Fatal("ExchangeCode() error = nil, want a context error")
	}
}

func TestCompleteManual(t *testing.T) {
	srv, form := tokenServer(t, http.StatusOK, `{"access_token":"at","expires_in":899}`)
	opt := Options{TokenURL: srv.URL, Client: srv.Client()}

	authURL, verifier, state, err := ManualURL("123", opt)
	if err != nil {
		t.Fatalf("ManualURL() error = %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse %q: %v", authURL, err)
	}
	if got := u.Query().Get("state"); got != state {
		t.Errorf("state in URL = %q, want the returned %q", got, state)
	}

	tok, err := CompleteManual(context.Background(), RedirectURI+"?code=abc&state="+state, verifier, state, opt)
	if err != nil {
		t.Fatalf("CompleteManual() error = %v", err)
	}
	if tok.AccessToken != "at" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "at")
	}
	if got := form.Get("code_verifier"); got != verifier {
		t.Errorf("code_verifier = %q, want %q", got, verifier)
	}

	if _, err := CompleteManual(context.Background(), RedirectURI+"?code=abc&state=wrong", verifier, state, opt); err == nil {
		t.Error("CompleteManual() with a mismatched state error = nil, want an error")
	}
}

func TestOptionsDefaults(t *testing.T) {
	var o Options
	if got := o.authorize(); got != AuthorizeURL {
		t.Errorf("authorize() = %q, want %q", got, AuthorizeURL)
	}
	if got := o.token(); got != TokenURL {
		t.Errorf("token() = %q, want %q", got, TokenURL)
	}
	if o.httpClient() != http.DefaultClient {
		t.Error("httpClient() did not fall back to http.DefaultClient")
	}
	if got := o.timeout(); got != 5*time.Minute {
		t.Errorf("timeout() = %v, want %v", got, 5*time.Minute)
	}

	set := Options{AuthorizeURL: "a", TokenURL: "t", Timeout: time.Second, Client: &http.Client{}}
	if got := set.authorize(); got != "a" {
		t.Errorf("authorize() = %q, want %q", got, "a")
	}
	if got := set.token(); got != "t" {
		t.Errorf("token() = %q, want %q", got, "t")
	}
	if got := set.timeout(); got != time.Second {
		t.Errorf("timeout() = %v, want %v", got, time.Second)
	}
	if set.httpClient() == http.DefaultClient {
		t.Error("httpClient() ignored the supplied client")
	}
}

func TestLoginSilentWithoutProfile(t *testing.T) {
	tests := []struct {
		name    string
		dataDir func(t *testing.T) string
	}{
		{"no data dir", func(t *testing.T) string { return "" }},
		{"missing data dir", func(t *testing.T) string { return t.TempDir() + "/absent" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoginSilent(context.Background(), "123", Options{DataDir: tt.dataDir(t)})
			if err == nil {
				t.Fatal("LoginSilent() error = nil, want ErrNoSession")
			}
			if !strings.Contains(err.Error(), ErrNoSession.Error()) {
				t.Errorf("error = %q, want it to wrap %q", err, ErrNoSession)
			}
		})
	}
}
