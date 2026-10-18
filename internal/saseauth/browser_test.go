package saseauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// findChrome locates a browser for the integration tests, which are skipped
// when the machine has none.
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
