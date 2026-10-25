package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type stubSource struct {
	ttl   time.Duration
	calls atomic.Int32
	err   error
}

func (s *stubSource) Token(ctx context.Context) (string, time.Duration, error) {
	n := s.calls.Add(1)
	if s.err != nil {
		return "", 0, s.err
	}
	return fmt.Sprintf("token-%d", n), s.ttl, nil
}

func activateClient(src TokenSource) *Client {
	c := NewClient(&http.Client{Transport: routes{"/api/profile": func() (int, string) { return http.StatusOK, `{}` }}}, nil, "", false, 100, 100)
	c.src = src
	return c
}

func TestTokenGood(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		expires time.Duration
		stopped bool
		need    time.Duration
		want    bool
	}{
		{"plenty of life left", "t", 15 * time.Minute, false, 90 * time.Second, true},
		{"inside the pre-flight window", "t", 30 * time.Second, false, 90 * time.Second, false},
		{"already expired", "t", -time.Second, false, 90 * time.Second, false},
		{"unknown lifetime", "t", 0, false, 90 * time.Second, true},
		{"no token at all", "", 0, false, 90 * time.Second, false},
		{"refreshing stopped", "t", 15 * time.Minute, true, 90 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{token: tt.token, refreshStopped: tt.stopped}
			if tt.expires != 0 {
				c.expires = time.Now().Add(tt.expires)
			}
			if got := c.TokenGood(tt.need); got != tt.want {
				t.Errorf("TokenGood(%v) = %v, want %v", tt.need, got, tt.want)
			}
		})
	}
}

func TestEnsureTokenIsFreeWhenTheTokenIsGood(t *testing.T) {
	src := &stubSource{ttl: 15 * time.Minute}
	c := activateClient(src)
	c.setToken("t")
	c.setExpiry(15 * time.Minute)

	if err := c.EnsureToken(context.Background(), 90*time.Second); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if got := src.calls.Load(); got != 0 {
		t.Errorf("source asked %d times, want 0 for a token with life left", got)
	}
}

func TestEnsureTokenMintsWhenTheTokenIsSpent(t *testing.T) {
	src := &stubSource{ttl: 15 * time.Minute}
	c := activateClient(src)
	c.setToken("stale")
	c.setExpiry(10 * time.Second)

	if err := c.EnsureToken(context.Background(), 90*time.Second); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if got := src.calls.Load(); got != 1 {
		t.Errorf("source asked %d times, want 1", got)
	}
	if got := c.getToken(); got != "token-1" {
		t.Errorf("token = %q, want the fresh %q", got, "token-1")
	}
	if !c.TokenGood(90 * time.Second) {
		t.Error("TokenGood() = false straight after a mint")
	}
}

func TestEnsureTokenReportsACredentialThatCannotBeRenewed(t *testing.T) {
	src := &stubSource{err: fmt.Errorf("token: status 400: invalid_grant")}
	c := activateClient(src)
	c.setToken("stale")
	c.setExpiry(10 * time.Second)

	err := c.EnsureToken(context.Background(), 90*time.Second)
	if err == nil {
		t.Fatal("EnsureToken() error = nil, want the dead credential reported")
	}
	if got := err.Error(); got == "" {
		t.Error("EnsureToken() error has no message")
	}
}

func TestEnsureTokenOnAStaticTokenDoesNothing(t *testing.T) {
	c := NewClient(nil, nil, "static", false, 1, 1)
	if err := c.EnsureToken(context.Background(), time.Hour); err != nil {
		t.Errorf("EnsureToken() error = %v, want nil for a static token", err)
	}
}

func TestEnsureTokenMintsOnceUnderConcurrentCallers(t *testing.T) {
	src := &stubSource{ttl: 15 * time.Minute}
	c := activateClient(src)
	c.setToken("stale")
	c.setExpiry(10 * time.Second)

	done := make(chan error, 8)
	for range 8 {
		go func() { done <- c.EnsureToken(context.Background(), 90*time.Second) }()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
	}
	if got := src.calls.Load(); got > 2 {
		t.Errorf("source asked %d times, want the mint shared rather than one each", got)
	}
}

func TestTokenLeft(t *testing.T) {
	c := &Client{token: "t"}
	if _, ok := c.TokenLeft(); ok {
		t.Error("TokenLeft() ok = true with no expiry recorded, want false")
	}
	c.setExpiry(10 * time.Minute)
	left, ok := c.TokenLeft()
	if !ok {
		t.Fatal("TokenLeft() ok = false after an expiry was recorded")
	}
	if left <= 9*time.Minute || left > 10*time.Minute {
		t.Errorf("TokenLeft() = %v, want just under 10m", left)
	}
}

func TestRefreshStoppedMarksTheClientUnusable(t *testing.T) {
	c := &Client{token: "t"}
	c.setExpiry(time.Hour)
	if !c.TokenGood(time.Minute) {
		t.Fatal("TokenGood() = false before refreshing stopped")
	}
	c.stopRefreshing()
	if c.TokenGood(time.Minute) {
		t.Error("TokenGood() = true after refreshing stopped for good")
	}
}
