package saseauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// seedServer answers refresh grants, recording each one and rotating the token
// when rotate is set. inFlight catches two tenants redeeming at once, which is
// the failure the Seed exists to prevent.
func seedServer(t *testing.T, rotate bool) (*httptest.Server, *[]url.Values, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	var seen []url.Values
	var inFlight, overlap atomic.Int32
	n := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inFlight.Add(1) > 1 {
			overlap.Add(1)
		}
		defer inFlight.Add(-1)

		r.ParseForm()
		mu.Lock()
		seen = append(seen, r.PostForm)
		n++
		next := r.PostForm.Get("refresh_token")
		if rotate {
			next = fmt.Sprintf("rotated-%d", n)
		}
		mu.Unlock()

		fmt.Fprintf(w, `{"access_token":"at","refresh_token":%q,"expires_in":899}`, next)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &overlap
}

func TestSeedRedeemedByOneTenantAtATime(t *testing.T) {
	srv, _, overlap := seedServer(t, true)
	seed := NewSeed("account-rt")

	var wg sync.WaitGroup
	for _, tsg := range []string{"1", "2", "3", "4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seed.redeem(context.Background(), srv.Client(), srv.URL, tsg)
		}()
	}
	wg.Wait()

	if got := overlap.Load(); got != 0 {
		t.Errorf("%d overlapping redemptions, want the seed to serialise them", got)
	}
}

func TestSeedRetiredOnceRotated(t *testing.T) {
	srv, seen, _ := seedServer(t, true)
	seed := NewSeed("account-rt")

	tok, tried, err := seed.redeem(context.Background(), srv.Client(), srv.URL, "1")
	if err != nil || !tried {
		t.Fatalf("redeem() = tried %v, error %v, want a successful first redemption", tried, err)
	}
	if tok.RefreshToken == "account-rt" {
		t.Fatal("the test server did not rotate, so this proves nothing")
	}

	// The token the first tenant presented will not be accepted again, so the
	// second must be told there is nothing to redeem rather than sent to spend
	// a dead credential.
	if _, tried, _ := seed.redeem(context.Background(), srv.Client(), srv.URL, "2"); tried {
		t.Error("redeem() tried = true after a rotation, want the spent seed to be skipped")
	}
	if len(*seen) != 1 {
		t.Errorf("%d requests reached the server, want 1", len(*seen))
	}
}

func TestSeedSurvivesAServerThatDoesNotRotate(t *testing.T) {
	srv, seen, _ := seedServer(t, false)
	seed := NewSeed("account-rt")

	for _, tsg := range []string{"1", "2", "3"} {
		tok, tried, err := seed.redeem(context.Background(), srv.Client(), srv.URL, tsg)
		if err != nil || !tried {
			t.Fatalf("tenant %s: redeem() = tried %v, error %v, want it to succeed", tsg, tried, err)
		}
		if tok.AccessToken != "at" {
			t.Errorf("tenant %s: access token = %q, want %q", tsg, tok.AccessToken, "at")
		}
	}
	if len(*seen) != 3 {
		t.Fatalf("%d requests reached the server, want one per tenant", len(*seen))
	}
	// Each tenant must have asked for its own scope; a shared seed that leaked
	// one tenant's scope to another would hand back the wrong tenant's elements.
	for i, want := range []string{"tsg_id:1", "tsg_id:2", "tsg_id:3"} {
		if got := (*seen)[i].Get("scope"); !strings.Contains(got, want) {
			t.Errorf("request %d scope = %q, want it to contain %q", i, got, want)
		}
	}
}

func TestSeedRetiredOnlyWhenTheTokenItselfIsRefused(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantSpent bool
	}{
		{"token rejected", `{"error":"invalid_grant"}`, true},
		// A scope this account cannot have says nothing about the token, and
		// retiring it would send every other tenant to the browser for one
		// tenant's access problem.
		{"scope refused", `{"error":"invalid_scope"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			seed := NewSeed("account-rt")
			if _, tried, err := seed.redeem(context.Background(), srv.Client(), srv.URL, "1"); !tried || err == nil {
				t.Fatalf("redeem() = tried %v, error %v, want a reported failure", tried, err)
			}
			_, tried, _ := seed.redeem(context.Background(), srv.Client(), srv.URL, "2")
			if tried == tt.wantSpent {
				t.Errorf("second redeem tried = %v, want the seed spent = %v", tried, tt.wantSpent)
			}
		})
	}
}

func TestNilSeedIsNotTried(t *testing.T) {
	var seed *Seed
	tok, tried, err := seed.redeem(context.Background(), http.DefaultClient, "http://unused", "1")
	if tried || tok != nil || err != nil {
		t.Errorf("redeem() on a nil seed = (%v, %v, %v), want it to be a no-op", tok, tried, err)
	}
}

func TestLockProfileSerialisesSignInsSharingOneProfile(t *testing.T) {
	dir := t.TempDir()
	var inFlight, overlap atomic.Int32

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := lockProfile(context.Background(), dir)
			if err != nil {
				t.Errorf("lockProfile() error = %v", err)
				return
			}
			defer unlock()
			if inFlight.Add(1) > 1 {
				overlap.Add(1)
			}
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	if got := overlap.Load(); got != 0 {
		t.Errorf("%d browsers held the same profile at once, want 0", got)
	}
}

func TestLockProfileLetsDifferentProfilesRunAtOnce(t *testing.T) {
	a, err := lockProfile(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("lockProfile() error = %v", err)
	}
	defer a()
	// A second profile must not be made to wait behind the first; only a shared
	// data directory is a conflict.
	b, err := lockProfile(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("lockProfile() on a second profile error = %v", err)
	}
	b()
}

func TestLockProfileGivesUpWithTheContext(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockProfile(context.Background(), dir)
	if err != nil {
		t.Fatalf("lockProfile() error = %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lockProfile(ctx, dir); err == nil {
		t.Error("lockProfile() error = nil on a cancelled context, want it to stop waiting")
	}
}

func TestLockProfileWithoutAProfileIsFree(t *testing.T) {
	// No data directory means no shared state to protect, and two sign-ins
	// must not block each other over nothing.
	a, err := lockProfile(context.Background(), "")
	if err != nil {
		t.Fatalf("lockProfile() error = %v", err)
	}
	defer a()
	if _, err := lockProfile(context.Background(), ""); err != nil {
		t.Errorf("lockProfile() error = %v, want an empty dir never to block", err)
	}
}
