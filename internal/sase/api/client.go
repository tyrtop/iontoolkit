// Package api is a client for the Prisma SD-WAN endpoints of the SASE API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// strata expects a browser UA in the header.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type Client struct {
	client   *http.Client
	wsClient *http.Client
	limiter  *rate.Limiter
	verbose  bool

	tokenMu sync.RWMutex
	token   string
	// Zero means the source never said, not expired.
	expires        time.Time
	refreshStopped bool

	// Tests only.
	profileBase string

	dialMu sync.RWMutex
	dial   DialPolicy
	onDial func(DialAttempt)
	pager  string
	pagers []string

	src       TokenSource
	tokenErrs chan error
	// Keeps a caller's mint from racing refreshLoop's.
	mintMu sync.Mutex
}

// An unknown expiry counts as good: a static SCM_TOKEN has no lifetime to
// check, and refusing to try would be worse.
func (s *Client) TokenGood(need time.Duration) bool {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	if s.token == "" {
		return false
	}
	if s.refreshStopped {
		return false
	}
	if s.expires.IsZero() {
		return true
	}
	return time.Until(s.expires) >= need
}

// RefreshStopped means every request from here fails until a human signs in.
func (s *Client) RefreshStopped() bool {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.refreshStopped
}

func (s *Client) TokenLeft() (time.Duration, bool) {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	if s.expires.IsZero() {
		return 0, false
	}
	return time.Until(s.expires), true
}

func NewClient(client, wsClient *http.Client, token string, verbose bool, rps float64, burst int) *Client {
	return &Client{
		client:   client,
		wsClient: wsClient,
		limiter:  rate.NewLimiter(rate.Limit(rps), burst),
		token:    token,
		verbose:  verbose,
	}
}

// ElementRecord is LookupElement keeping the body, for callers that want to
// show what the server actually sent.
func (s *Client) ElementRecord(ctx context.Context, eid string) ([]byte, Element, error) {
	body, err := s.get(ctx, "element "+eid, ElementURL(eid))
	if err != nil {
		return nil, Element{}, err
	}
	var el Element
	if err := json.Unmarshal(body, &el); err != nil {
		return body, Element{}, fmt.Errorf("element %s: json decode: %w", eid, err)
	}
	return body, el, nil
}

func (s *Client) LookupElement(ctx context.Context, eid string) (Element, error) {
	_, el, err := s.ElementRecord(ctx, eid)
	return el, err
}

func (s *Client) getToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.token
}

func (s *Client) setToken(t string) {
	s.tokenMu.Lock()
	s.token = t
	s.tokenMu.Unlock()
}

func (s *Client) setExpiry(ttl time.Duration) {
	s.tokenMu.Lock()
	if ttl > 0 {
		s.expires = time.Now().Add(ttl)
	} else {
		s.expires = time.Time{}
	}
	s.tokenMu.Unlock()
}

// SetExpiryForTest exists because the fields are unexported and other
// packages' tests need a near-dead token.
func (s *Client) SetExpiryForTest(ttl time.Duration) { s.setExpiry(ttl) }

func (s *Client) stopRefreshing() {
	s.tokenMu.Lock()
	s.refreshStopped = true
	s.tokenMu.Unlock()
}

func NewServiceAccountClient(ctx context.Context, client, wsClient *http.Client, id, secret, tsg string, margin time.Duration, verbose bool, rps float64, burst int) (*Client, error) {
	s := &Client{
		client:    client,
		wsClient:  wsClient,
		limiter:   rate.NewLimiter(rate.Limit(rps), burst),
		verbose:   verbose,
		src:       serviceAccount{client: client, id: id, secret: secret, tsg: tsg},
		tokenErrs: make(chan error, 1),
	}
	ttl, err := s.mint(ctx)
	if err != nil {
		return nil, err
	}
	if margin >= ttl {
		return nil, fmt.Errorf("refresh margin %s is not shorter than token lifetime %s", margin, ttl)
	}
	go s.refreshLoop(ctx, ttl, margin)
	return s, nil
}

// NewTokenSourceClient builds a client around any TokenSource, priming a token
// before it returns and keeping it fresh afterwards.
func NewTokenSourceClient(ctx context.Context, client, wsClient *http.Client, src TokenSource, margin time.Duration, verbose bool, rps float64, burst int) (*Client, error) {
	s := &Client{
		client:    client,
		wsClient:  wsClient,
		limiter:   rate.NewLimiter(rate.Limit(rps), burst),
		verbose:   verbose,
		src:       src,
		tokenErrs: make(chan error, 1),
	}
	ttl, err := s.mint(ctx)
	if err != nil {
		return nil, err
	}
	if margin >= ttl {
		return nil, fmt.Errorf("refresh margin %s is not shorter than token lifetime %s", margin, ttl)
	}
	go s.refreshLoop(ctx, ttl, margin)
	return s, nil
}
