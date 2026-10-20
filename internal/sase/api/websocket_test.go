package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsServer(t *testing.T, status int, acceptOn int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var tries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := tries.Add(1); acceptOn > 0 && n >= acceptOn {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
				Subprotocols: []string{"Bearer"},
			})
			if err != nil {
				return
			}
			c.Close(websocket.StatusNormalClosure, "")
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &tries
}

func testClient(t *testing.T, p DialPolicy) *Client {
	t.Helper()
	c := NewClient(nil, &http.Client{}, "tok", false, 1, 1)
	c.SetDialPolicy(p)
	return c
}

func TestDialRetriesASocketThatWillNotComeUp(t *testing.T) {
	srv, tries := wsServer(t, http.StatusBadGateway, 0)
	c := testClient(t, DialPolicy{Attempts: 3, Timeout: time.Second, Backoff: time.Millisecond})

	_, err := c.dialURL(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), "e1")
	if err == nil {
		t.Fatal("dial error = nil, want the failure reported after the retries")
	}
	if got := tries.Load(); got != 3 {
		t.Errorf("%d handshake attempts, want 3", got)
	}
}

func TestDialStopsOnceItSucceeds(t *testing.T) {
	srv, tries := wsServer(t, http.StatusBadGateway, 2)
	c := testClient(t, DialPolicy{Attempts: 5, Timeout: time.Second, Backoff: time.Millisecond})

	_, _ = c.dialURL(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), "e1")
	if got := tries.Load(); got != 2 {
		t.Errorf("%d handshake attempts, want it to stop at 2", got)
	}
}

func TestDialDoesNotRetryARejectedCredential(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"no such element", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, tries := wsServer(t, tt.status, 0)
			c := testClient(t, DialPolicy{Attempts: 4, Timeout: time.Second, Backoff: time.Millisecond})

			_, err := c.dialURL(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), "e1")
			if err == nil {
				t.Fatal("dial error = nil, want the rejection reported")
			}
			if got := tries.Load(); got != 1 {
				t.Errorf("%d handshake attempts, want 1: retrying cannot change the answer", got)
			}
		})
	}
}

func TestDialGivesUpWithTheContext(t *testing.T) {
	srv, _ := wsServer(t, http.StatusBadGateway, 0)
	c := testClient(t, DialPolicy{Attempts: 50, Timeout: time.Second, Backoff: 50 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	began := time.Now()
	if _, err := c.dialURL(ctx, strings.Replace(srv.URL, "http://", "ws://", 1), "e1"); err == nil {
		t.Fatal("dial error = nil, want the cancelled context reported")
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("took %s, want the retries abandoned with the context", took)
	}
}

func TestDialPolicyDefaults(t *testing.T) {
	got := DialPolicy{}.withDefaults()
	if got != DefaultDialPolicy {
		t.Errorf("withDefaults() = %+v, want %+v", got, DefaultDialPolicy)
	}
	got = DialPolicy{Attempts: 7}.withDefaults()
	if got.Attempts != 7 {
		t.Errorf("Attempts = %d, want the override kept", got.Attempts)
	}
	if got.Timeout != DefaultDialPolicy.Timeout {
		t.Errorf("Timeout = %v, want the default kept", got.Timeout)
	}
}

func TestRetryableDial(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"transport failure with no response", dialError{err: errors.New("connection refused")}, true},
		{"bad gateway", dialError{err: errors.New("502"), status: http.StatusBadGateway}, true},
		{"timeout", context.DeadlineExceeded, true},
		{"unauthorized", dialError{err: errors.New("401"), status: http.StatusUnauthorized}, false},
		{"forbidden", dialError{err: errors.New("403"), status: http.StatusForbidden}, false},
		{"not found", dialError{err: errors.New("404"), status: http.StatusNotFound}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableDial(tt.err); got != tt.want {
				t.Errorf("retryableDial(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDialReporterSeesEachAttemptAsItStarts(t *testing.T) {
	srv, _ := wsServer(t, http.StatusBadGateway, 0)
	c := testClient(t, DialPolicy{Attempts: 3, Timeout: time.Second, Backoff: time.Millisecond})

	var mu sync.Mutex
	var seen []DialAttempt
	c.SetDialReporter(func(a DialAttempt) {
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
	})

	_, _ = c.dialURL(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), "e1")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("%d attempts reported, want 3", len(seen))
	}
	for i, a := range seen {
		if a.Attempt != i+1 || a.Of != 3 {
			t.Errorf("attempt %d reported as %d/%d, want %d/3", i, a.Attempt, a.Of, i+1)
		}
		if a.ElementID != "e1" {
			t.Errorf("attempt %d element = %q, want %q", i, a.ElementID, "e1")
		}
	}
	if seen[0].Err != nil {
		t.Errorf("first attempt carried err %v, want nil", seen[0].Err)
	}
	for i, a := range seen[1:] {
		if a.Err == nil {
			t.Errorf("attempt %d carried no previous error", i+2)
		}
	}
}

func TestDialWithoutAReporterStillWorks(t *testing.T) {
	srv, tries := wsServer(t, http.StatusBadGateway, 2)
	c := testClient(t, DialPolicy{Attempts: 3, Timeout: time.Second, Backoff: time.Millisecond})

	_, _ = c.dialURL(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), "e1")
	if got := tries.Load(); got != 2 {
		t.Errorf("%d attempts, want the dial unaffected by having no reporter", got)
	}
}
