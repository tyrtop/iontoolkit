package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAGatewayErrorIsTriedAgainOnlyWhereARepeatIsSafe(t *testing.T) {
	for _, c := range []struct {
		method string
		calls  int
		ok     bool
	}{
		{http.MethodGet, 2, true},
		{http.MethodPut, 2, true},
		{http.MethodPost, 1, false},
	} {
		calls := 0
		cl := snapshotClient(t, routes{"/sdwan/v2.0/api/sites": func() (int, string) {
			calls++
			if calls == 1 {
				return http.StatusBadGateway, `{"error":"gateway"}`
			}
			return http.StatusOK, `{"items":[]}`
		}})
		var err error
		if c.method == http.MethodGet {
			_, err = cl.GetJSON(t.Context(), "/sdwan/v2.0/api/sites")
		} else {
			_, err = cl.Send(t.Context(), c.method, "/sdwan/v2.0/api/sites", []byte(`{}`))
		}
		if calls != c.calls || (err == nil) != c.ok {
			t.Errorf("%s: %d calls, err %v; want %d calls, ok %v", c.method, calls, err, c.calls, c.ok)
		}
	}
}

func TestABadRequestIsNotTriedAgain(t *testing.T) {
	calls := 0
	cl := snapshotClient(t, routes{"/sdwan/v2.0/api/sites": func() (int, string) {
		calls++
		return http.StatusBadRequest, `{"error":"no"}`
	}})
	if _, err := cl.GetJSON(t.Context(), "/sdwan/v2.0/api/sites"); err == nil || calls != 1 {
		t.Errorf("%d calls, err %v", calls, err)
	}
}

func TestTheBackoffIsUrllib3s(t *testing.T) {
	var got []string
	for retry := 1; retry <= 8; retry++ {
		got = append(got, backoffBefore(retry).Round(10*time.Millisecond).String())
	}
	if want := "0s 1.41s 2.82s 5.65s 11.29s 22.59s 45.18s 1m30.35s"; strings.Join(got, " ") != want {
		t.Errorf("sleeps %v, want %s", got, want)
	}
}

func TestA429WithoutRetryAfterBacksOff(t *testing.T) {
	r := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	if retryAfter(r) != 0 {
		t.Error("a 429 with no Retry-After should fall back to the backoff")
	}
	r.Header.Set("Retry-After", "7")
	if retryAfter(r) != 7*time.Second {
		t.Errorf("Retry-After 7 = %s", retryAfter(r))
	}
	r.StatusCode = http.StatusBadGateway
	if retryAfter(r) != 7*time.Second {
		t.Error("urllib3 sleeps for a Retry-After on any status it retries")
	}
}

func TestAConnectionThatNeverCameUpIsTriedAgainEvenForAPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()
	c := NewClient(http.DefaultClient, http.DefaultClient, "tok", false, 100, 100)
	_, status, _, _ := c.attempt(t.Context(), "probe", http.MethodPost, addr+"/x", []byte(`{}`), true)
	if status != noConnection || !again(http.MethodPost, status) {
		t.Errorf("refused connect: status %d", status)
	}
}

func TestABadCertificateIsNotTriedAgain(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := NewClient(http.DefaultClient, http.DefaultClient, "tok", false, 100, 100)
	_, status, _, err := c.attempt(t.Context(), "probe", http.MethodGet, srv.URL+"/x", nil, false)
	if err == nil || again(http.MethodGet, status) {
		t.Errorf("unknown authority: status %d, err %v", status, err)
	}
}

func TestPlainHTTPOnTheTLSPortIsNotTriedAgain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := NewClient(http.DefaultClient, http.DefaultClient, "tok", false, 100, 100)
	_, status, _, err := c.attempt(t.Context(), "probe", http.MethodGet, "https"+strings.TrimPrefix(srv.URL, "http")+"/x", nil, false)
	if err == nil || again(http.MethodGet, status) {
		t.Errorf("scheme mismatch: status %d, err %v", status, err)
	}
}

func TestWritesGetTheSDKsTimeoutAndReadsKeepTheLookupOne(t *testing.T) {
	c := NewClient(&http.Client{Timeout: 15 * time.Second}, nil, "tok", false, 100, 100)
	if got := c.httpFor(false).Timeout; got != 15*time.Second {
		t.Errorf("read timeout %s", got)
	}
	if got := c.httpFor(true).Timeout; got != WriteTimeout {
		t.Errorf("write timeout %s", got)
	}
}

func TestARequestThatNeverReachedTheNetworkIsNotTriedAgain(t *testing.T) {
	c := NewClient(http.DefaultClient, nil, "tok\r", false, 100, 100)
	_, status, _, err := c.attempt(t.Context(), "probe", http.MethodPost, "http://127.0.0.1:1/x", []byte(`{}`), true)
	if err == nil || again(http.MethodPost, status) {
		t.Errorf("bad header: status %d, err %v", status, err)
	}
}

func TestAPatientReadGetsTheSDKsTimeout(t *testing.T) {
	c := NewClient(&http.Client{Timeout: 15 * time.Second}, nil, "tok", false, 100, 100)
	if !patient(Patient(t.Context())) || patient(t.Context()) {
		t.Error("Patient did not mark the context")
	}
	if got := c.httpFor(patient(Patient(t.Context()))).Timeout; got != WriteTimeout {
		t.Errorf("patient read timeout %s", got)
	}
}
