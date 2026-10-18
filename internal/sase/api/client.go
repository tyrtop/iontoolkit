// Package api is a client for the Prisma SD-WAN endpoints of the SASE API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const scmBase = "https://api.sase.paloaltonetworks.com/sdwan/v3.2/api"

// strata expects a browser UA in the header.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type Client struct {
	client   *http.Client
	wsClient *http.Client
	limiter  *rate.Limiter
	verbose  bool

	tokenMu sync.RWMutex
	token   string

	auth *auth
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
	if err := s.limiter.Wait(ctx); err != nil {
		return Element{}, fmt.Errorf("element %s: rate limit wait: %w", eid, err)
	}

	url := scmBase + "/elements/" + eid

	if s.verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Element{}, fmt.Errorf("element %s: build request: %w", eid, err)
	}

	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return Element{}, fmt.Errorf("element %s: response: %w", eid, err)
	}

	if s.verbose {
		for k, v := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-ratelimit") || strings.EqualFold(k, "retry-after") {
				fmt.Fprintln(os.Stderr, k+":", v)
			}
		}
	}

	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Element{}, fmt.Errorf("element %s: status %s: %s", eid, resp.Status, body)
	}

	var el Element
	if err := json.NewDecoder(resp.Body).Decode(&el); err != nil {
		return Element{}, fmt.Errorf("element %s: json decode: %w", eid, err)
	}

	return el, nil
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

func NewServiceAccountClient(ctx context.Context, client, wsClient *http.Client, id, secret, tsg string, margin time.Duration, verbose bool, rps float64, burst int) (*Client, error) {
	s := &Client{
		client:   client,
		wsClient: wsClient,
		limiter:  rate.NewLimiter(rate.Limit(rps), burst),
		verbose:  verbose,
		auth:     &auth{client: client, id: id, secret: secret, tsg: tsg},
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
