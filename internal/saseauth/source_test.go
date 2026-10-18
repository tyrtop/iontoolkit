package saseauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
