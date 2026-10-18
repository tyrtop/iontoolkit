package saseauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceTokenUsesStoredRefreshToken(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt2","expires_in":899}`))
	}))
	defer srv.Close()

	var saved atomic.Value
	src := NewSource("123", "rt1", Options{TokenURL: srv.URL, Client: srv.Client()},
		func(rt string) error { saved.Store(rt); return nil })

	tok, ttl, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok != "at" {
		t.Errorf("token = %q, want %q", tok, "at")
	}
	if ttl != 899*time.Second {
		t.Errorf("ttl = %v, want %v", ttl, 899*time.Second)
	}
	if got := form.Get("refresh_token"); got != "rt1" {
		t.Errorf("sent refresh_token = %q, want %q", got, "rt1")
	}
	if got := src.RefreshToken(); got != "rt2" {
		t.Errorf("RefreshToken() = %q, want the rotated %q", got, "rt2")
	}
	if got, _ := saved.Load().(string); got != "rt2" {
		t.Errorf("saved refresh token = %q, want %q", got, "rt2")
	}
}

func TestSourceTokenReportsInteractiveRequired(t *testing.T) {
	tests := []struct {
		name    string
		refresh string
		status  int
	}{
		{"no stored credential at all", "", http.StatusOK},
		{"stored refresh token rejected", "rt1", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(`{"error":"invalid_grant"}`))
			}))
			defer srv.Close()

			// No DataDir, so the silent browser attempt cannot run either.
			src := NewSource("123", tt.refresh, Options{TokenURL: srv.URL, Client: srv.Client()}, nil)
			_, _, err := src.Token(context.Background())
			if !errors.Is(err, ErrInteractiveRequired) {
				t.Fatalf("Token() error = %v, want it to wrap ErrInteractiveRequired", err)
			}
		})
	}
}

func TestSourceTokenSurfacesSaveFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt2","expires_in":899}`))
	}))
	defer srv.Close()

	want := errors.New("keyring is locked")
	src := NewSource("123", "rt1", Options{TokenURL: srv.URL, Client: srv.Client()},
		func(string) error { return want })

	tok, _, err := src.Token(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("Token() error = %v, want it to wrap %v", err, want)
	}
	// The token itself is still usable; only persistence failed.
	if tok != "at" {
		t.Errorf("token = %q, want %q returned despite the save failure", tok, "at")
	}
	if got := src.RefreshToken(); got != "rt2" {
		t.Errorf("RefreshToken() = %q, want the in-memory value %q to be updated anyway", got, "rt2")
	}
}

func TestSourceTokenHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"at","expires_in":899}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := NewSource("123", "rt1", Options{TokenURL: srv.URL, Client: srv.Client()}, nil)
	_, _, err := src.Token(ctx)
	if err == nil {
		t.Fatal("Token() error = nil, want a context error")
	}
	if errors.Is(err, ErrInteractiveRequired) {
		t.Error("a cancelled context was reported as needing interactive sign-in")
	}
}

func TestSourceSatisfiesTokenSource(t *testing.T) {
	// Compile-time shape check without importing api, which would be a cycle
	// risk; the signature below is api.TokenSource.
	var _ interface {
		Token(ctx context.Context) (string, time.Duration, error)
	} = (*Source)(nil)
}

func TestSourceFallsBackToTheSharedSeed(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"access_token":"at","refresh_token":"tenant-rt","expires_in":899}`))
	}))
	defer srv.Close()

	var saved atomic.Value
	seed := NewSeed("account-rt")
	src := NewSource("123", "", Options{TokenURL: srv.URL, Client: srv.Client(), Seed: seed},
		func(rt string) error { saved.Store(rt); return nil })

	tok, _, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok != "at" {
		t.Errorf("token = %q, want %q", tok, "at")
	}
	if got := form.Get("refresh_token"); got != "account-rt" {
		t.Errorf("sent refresh_token = %q, want the account seed %q", got, "account-rt")
	}
	if got := form.Get("scope"); got != "openid profile email tsg_id:123" {
		t.Errorf("sent scope = %q, want it narrowed to this tenant", got)
	}
	// What came back is this tenant's own credential from now on, so the next
	// run does not have to touch the shared seed at all.
	if got, _ := saved.Load().(string); got != "tenant-rt" {
		t.Errorf("saved refresh token = %q, want %q", got, "tenant-rt")
	}
}

func TestSourcePrefersItsOwnTokenOverTheSeed(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt2","expires_in":899}`))
	}))
	defer srv.Close()

	seed := NewSeed("account-rt")
	src := NewSource("123", "tenant-rt", Options{TokenURL: srv.URL, Client: srv.Client(), Seed: seed}, nil)
	if _, _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if got := form.Get("refresh_token"); got != "tenant-rt" {
		t.Errorf("sent refresh_token = %q, want the tenant's own %q", got, "tenant-rt")
	}
	// Spending the shared seed when this tenant had a credential of its own
	// would retire it for every tenant that still needs it.
	if _, tried, _ := seed.redeem(context.Background(), srv.Client(), srv.URL, "9"); !tried {
		t.Error("the shared seed was spent by a tenant that did not need it")
	}
}

func TestConcurrentTenantsEachGetTheirOwnTokenFromOneSeed(t *testing.T) {
	// The real failure this guards: several tenants loading at once, none with
	// a credential of its own, all starting from one account refresh token. A
	// server that rotates leaves every tenant but the first with invalid_grant.
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		n++
		seq := n
		mu.Unlock()
		if r.PostForm.Get("refresh_token") != "account-rt" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		fmt.Fprintf(w, `{"access_token":"at-%d","refresh_token":"rotated-%d","expires_in":899}`, seq, seq)
	}))
	defer srv.Close()

	seed := NewSeed("account-rt")
	opt := Options{TokenURL: srv.URL, Client: srv.Client(), Seed: seed}

	var wg sync.WaitGroup
	var ok, interactive atomic.Int32
	for _, tsg := range []string{"1", "2", "3", "4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// No DataDir, so a tenant the seed cannot serve has no silent
			// browser attempt to fall back on and reports as much.
			_, _, err := NewSource(tsg, "", opt, nil).Token(context.Background())
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrInteractiveRequired):
				interactive.Add(1)
			default:
				t.Errorf("tenant %s: Token() error = %v, want nil or ErrInteractiveRequired", tsg, err)
			}
		}()
	}
	wg.Wait()

	// Exactly one tenant can be served by a rotating server; the rest must come
	// back asking for a browser, not reporting a rejected grant.
	if got := ok.Load(); got != 1 {
		t.Errorf("%d tenants got a token from the seed, want 1 from a rotating server", got)
	}
	if got := interactive.Load(); got != 3 {
		t.Errorf("%d tenants asked for interactive sign-in, want 3", got)
	}
	if n != 1 {
		t.Errorf("%d refresh grants were attempted, want 1: a spent seed must not be presented again", n)
	}
}
