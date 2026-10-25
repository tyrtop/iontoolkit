package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

type spent struct{ error }

func (spent) Temporary() bool { return false }

func (s spent) Unwrap() error { return s.error }

type oneToken struct {
	mu sync.Mutex
	n  int
}

func (o *oneToken) Token(context.Context) (string, time.Duration, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.n++; o.n == 1 {
		return "tok", 300 * time.Millisecond, nil
	}
	return "", 0, spent{fmt.Errorf("%w: no browser session", saseauth.ErrInteractiveRequired)}
}

type answer func(*http.Request) (*http.Response, error)

func (a answer) RoundTrip(r *http.Request) (*http.Response, error) { return a(r) }

func TestAPooledClientWhoseSignInDiesIsMarkedAndItsRunsStopped(t *testing.T) {
	ok := &http.Client{Transport: answer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	c, err := api.NewTokenSourceClient(t.Context(), ok, ok, &oneToken{}, 100*time.Millisecond, false, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	rt := New()
	rt.UseTenant(config.Tenant{Name: "Tractor Supply", TSGID: "111"})
	rt.Clients().Set("111", c)
	select {
	case <-rt.Clients().Gone(c):
	case <-time.After(5 * time.Second):
		t.Fatal("the client's runs were never told its sign-in was gone")
	}
	if got := rt.TokenDead(); got != "sign-in expired" {
		t.Errorf("TokenDead() = %q, want the sign-in marked expired", got)
	}
}

func TestASignInLostOnAnotherTenantDoesNotMarkTheOneInUse(t *testing.T) {
	ok := &http.Client{Transport: answer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	c, err := api.NewTokenSourceClient(t.Context(), ok, ok, &oneToken{}, 100*time.Millisecond, false, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	rt := New()
	rt.UseTenant(config.Tenant{Name: "Tractor Supply", TSGID: "111"})
	rt.Clients().Set("222", c)
	select {
	case <-rt.Clients().Gone(c):
	case <-time.After(5 * time.Second):
		t.Fatal("the client's runs were never told its sign-in was gone")
	}
	if got := rt.TokenDead(); got != "" {
		t.Errorf("TokenDead() = %q, want the tenant in use left alone", got)
	}
	if rt.Fleet().Issues["222"] == "" {
		t.Error("the tenant that lost its sign-in has no issue recorded")
	}
}

func TestADroppedClientStopsRefreshingAndReportsNothing(t *testing.T) {
	ok := &http.Client{Transport: answer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	src := &oneToken{}
	c, err := api.NewTokenSourceClient(t.Context(), ok, ok, src, 100*time.Millisecond, false, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	rt := New()
	rt.UseTenant(config.Tenant{Name: "Tractor Supply", TSGID: "111"})
	rt.Clients().Set("111", c)
	rt.Clients().Drop("111")
	time.Sleep(time.Second)
	src.mu.Lock()
	asked := src.n
	src.mu.Unlock()
	if asked != 1 {
		t.Errorf("the dropped client asked for a token %d times, want only its first", asked)
	}
	if got := rt.TokenDead(); got != "" {
		t.Errorf("TokenDead() = %q, want nothing from a client nobody uses", got)
	}
}
