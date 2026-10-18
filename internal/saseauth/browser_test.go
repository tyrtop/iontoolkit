package saseauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// findChrome locates a browser for the integration tests, which are skipped
// when the machine has none. The tests run it without its sandbox: they drive
// a throwaway profile against a fake server, and CI images forbid the user
// namespaces Chrome would need.
func findChrome(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no chrome or chromium on PATH")
	return ""
}

// fakeAuthServer stands in for the authorization server. Its authorize endpoint
// redirects to RedirectURI, the real address, which the browser cannot load.
// That is the point: the code has to be captured from the redirect itself.
func fakeAuthServer(t *testing.T, redirect func(state string) string) (*httptest.Server, *url.Values) {
	t.Helper()
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect(r.URL.Query().Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"browser-at","refresh_token":"browser-rt","expires_in":899}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &form
}

func TestLoginCapturesCodeFromRedirect(t *testing.T) {
	chrome := findChrome(t)
	srv, form := fakeAuthServer(t, func(state string) string {
		return RedirectURI + "?code=captured-code&state=" + state
	})

	opt := Options{
		Chrome:       chrome,
		NoSandbox:    true,
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		Client:       srv.Client(),
		Timeout:      90 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tok, err := login(ctx, "1984021017", opt, true)
	if err != nil {
		t.Fatalf("login() error = %v", err)
	}
	if tok.AccessToken != "browser-at" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "browser-at")
	}
	if tok.RefreshToken != "browser-rt" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "browser-rt")
	}
	if got := form.Get("code"); got != "captured-code" {
		t.Errorf("exchanged code = %q, want %q", got, "captured-code")
	}
	if got := form.Get("code_verifier"); got == "" {
		t.Error("no code_verifier was sent to the token endpoint")
	}
}

func TestLoginRejectsMismatchedState(t *testing.T) {
	chrome := findChrome(t)
	srv, _ := fakeAuthServer(t, func(string) string {
		return RedirectURI + "?code=captured-code&state=not-the-one-we-sent"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := login(ctx, "123", Options{
		Chrome:       chrome,
		NoSandbox:    true,
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		Client:       srv.Client(),
		Timeout:      90 * time.Second,
	}, true)
	if err == nil {
		t.Fatal("login() error = nil, want a state mismatch error")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("error = %q, want it to mention a state mismatch", err)
	}
}

func TestLoginSurfacesAuthorizationServerError(t *testing.T) {
	chrome := findChrome(t)
	srv, _ := fakeAuthServer(t, func(string) string {
		return RedirectURI + "?error=login_required&error_description=no+session"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := login(ctx, "123", Options{
		Chrome:       chrome,
		NoSandbox:    true,
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		Client:       srv.Client(),
		Timeout:      90 * time.Second,
	}, true)
	if err == nil {
		t.Fatal("login() error = nil, want the login_required error")
	}
	if !strings.Contains(err.Error(), "login_required") {
		t.Errorf("error = %q, want it to mention login_required", err)
	}
}

// TestSignInSurvivesRestart is the property that matters in use: sign in once,
// quit, come back, and get a working token with no browser. The second Source
// stands in for a fresh process, and its authorization server has no authorize
// endpoint at all, so any attempt to open a browser fails the test rather than
// quietly succeeding.
func TestSignInSurvivesRestart(t *testing.T) {
	chrome := findChrome(t)

	var mu sync.Mutex
	var grants []string
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, RedirectURI+"?code=the-code&state="+r.URL.Query().Get("state"), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		grants = append(grants, r.PostFormValue("grant_type"))
		mu.Unlock()
		switch r.PostFormValue("grant_type") {
		case "authorization_code":
			w.Write([]byte(`{"access_token":"at1","refresh_token":"stored-rt","expires_in":899}`))
		case "refresh_token":
			if r.PostFormValue("refresh_token") != "stored-rt" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			w.Write([]byte(`{"access_token":"at2","refresh_token":"stored-rt","expires_in":899}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Stand-in for the keyring: whatever the first run saves, the second reads.
	var saved string
	profile := t.TempDir()

	first := NewSource("1984021017", "", Options{
		Chrome:       chrome,
		NoSandbox:    true,
		DataDir:      profile,
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		Client:       srv.Client(),
		Timeout:      90 * time.Second,
	}, func(rt string) error { saved = rt; return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := first.SignIn(ctx); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if saved != "stored-rt" {
		t.Fatalf("persisted refresh token = %q, want %q", saved, "stored-rt")
	}

	// A fresh process: same stored token, but no authorize endpoint and no
	// browser binary, so only the refresh grant can possibly work.
	noBrowser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("the restarted session reached %s, want only the token endpoint", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer noBrowser.Close()

	second := NewSource("1984021017", saved, Options{
		DataDir:  profile,
		TokenURL: noBrowser.URL + "/token",
		Client:   noBrowser.Client(),
	}, func(rt string) error { saved = rt; return nil })

	tok, ttl, err := second.Token(ctx)
	if err != nil {
		t.Fatalf("Token() after restart error = %v, want a token with no browser", err)
	}
	if tok != "at2" {
		t.Errorf("token = %q, want %q from the refresh grant", tok, "at2")
	}
	if ttl != 899*time.Second {
		t.Errorf("ttl = %v, want %v", ttl, 899*time.Second)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(grants) != 2 || grants[0] != "authorization_code" || grants[1] != "refresh_token" {
		t.Errorf("grant types used = %v, want [authorization_code refresh_token]", grants)
	}
}

// TestDiscoverThenSwitchTenantsSilently is the whole point of signing in with a
// browser: one window, then every tenant scoped without another. The second and
// third tokens are obtained headlessly with prompt=none, which only works
// because the first sign-in left a session in the browser profile.
func TestDiscoverThenSwitchTenantsSilently(t *testing.T) {
	chrome := findChrome(t)

	var mu sync.Mutex
	var prompts []string
	var scopes []string

	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		prompts = append(prompts, q.Get("prompt"))
		scopes = append(scopes, q.Get("scope"))
		mu.Unlock()
		http.Redirect(w, r, RedirectURI+"?code=c&state="+q.Get("state"), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":899}`))
	})
	mux.HandleFunc("/tenancy", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tenancyBody))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	profile := t.TempDir()
	opt := Options{
		Chrome:       chrome,
		NoSandbox:    true,
		DataDir:      profile,
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		Client:       srv.Client(),
		Timeout:      90 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	d, err := Discover(ctx, opt, srv.URL+"/tenancy")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(d.Tenants) != 4 {
		t.Fatalf("Discover() found %d tenants, want 4", len(d.Tenants))
	}

	// Scoping to two different tenants, each without a window.
	for _, tsg := range []string{"1984021017", "1538107912"} {
		src := NewSource(tsg, "", opt, nil)
		tok, _, err := src.Token(ctx)
		if err != nil {
			t.Fatalf("Token() for tenant %s error = %v, want a silent scope change", tsg, err)
		}
		if tok == "" {
			t.Fatalf("no token for tenant %s", tsg)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 {
		t.Fatalf("authorize was called %d times, want 3 (discovery plus two tenants)", len(prompts))
	}
	// Discovery is interactive; both tenant scopings must be silent.
	if prompts[0] != "" {
		t.Errorf("discovery used prompt=%q, want an interactive sign-in", prompts[0])
	}
	for i, p := range prompts[1:] {
		if p != "none" {
			t.Errorf("tenant scoping %d used prompt=%q, want %q so no window appears", i, p, "none")
		}
	}
	if !strings.Contains(scopes[0], "tsg_id:none") {
		t.Errorf("discovery scope = %q, want it tenant-less", scopes[0])
	}
	if !strings.Contains(scopes[1], "tsg_id:1984021017") {
		t.Errorf("second scope = %q, want the chosen tenant", scopes[1])
	}
	if !strings.Contains(scopes[2], "tsg_id:1538107912") {
		t.Errorf("third scope = %q, want the other tenant", scopes[2])
	}
}

func TestCleanUpDoesNotWaitForABrowserThatWillNeverExit(t *testing.T) {
	// chromedp's cancel functions end by waiting for the browser process on a
	// channel that stays open when the browser never started. Without a bound,
	// a failed launch wedges the sign-in until the caller is killed.
	defer func(d time.Duration) { cleanUpGrace = d }(cleanUpGrace)
	cleanUpGrace = 50 * time.Millisecond

	done := make(chan struct{})
	go func() {
		stopWaiting(func() { <-make(chan struct{}) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stopWaiting() never returned for a cancel that blocks forever")
	}
}
