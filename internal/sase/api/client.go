package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"tyrtop.com/iontk/internal/endpoint"
)

// strata expects a browser UA in the header.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type Client struct {
	client   *http.Client
	wsClient *http.Client

	writerOnce sync.Once
	writer     *http.Client
	limiter    *rate.Limiter
	verbose    bool

	tokenMu        sync.RWMutex
	token          string
	expires        time.Time
	refreshStopped bool

	dialMu sync.RWMutex
	dial   DialPolicy
	onDial func(DialAttempt)
	pager  string
	pagers []string

	src       TokenSource
	tokenErrs chan error
	mintMu    sync.Mutex
	stop      context.CancelFunc
}

func (s *Client) Rate() float64 { return float64(s.limiter.Limit()) }

func (s *Client) Close() {
	if s != nil && s.stop != nil {
		s.stop()
	}
}

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

func (s *Client) LookupElement(ctx context.Context, eid string) (Element, error) {
	_, _, el, err := s.elementRecord(ctx, batch, eid)
	return el, err
}

func (s *Client) elementRecord(ctx context.Context, set *endpoint.Set, eid string) ([]byte, string, Element, error) {
	body, url, err := s.getNamed(ctx, "element "+eid, set, "element", vars{"element_id": eid})
	if err != nil {
		return nil, url, Element{}, err
	}
	var el Element
	if err := json.Unmarshal(body, &el); err != nil {
		return body, url, Element{}, fmt.Errorf("element %s: json decode: %w", eid, err)
	}
	return body, url, el, nil
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
	loop, stop := context.WithCancel(ctx)
	s.stop = stop
	go s.refreshLoop(loop, ttl, margin)
	return s, nil
}

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
	loop, stop := context.WithCancel(ctx)
	s.stop = stop
	go s.refreshLoop(loop, ttl, margin)
	return s, nil
}

// the cloudgenix SDK gives every call 240s
const WriteTimeout = 240 * time.Second

func (s *Client) httpFor(write bool) *http.Client {
	if !write || s.client.Timeout == 0 || s.client.Timeout >= WriteTimeout {
		return s.client
	}
	s.writerOnce.Do(func() {
		w := *s.client
		w.Timeout = WriteTimeout
		s.writer = &w
	})
	return s.writer
}
