package saseauth

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// Seed is the tenant-less refresh token every tenant without one of its own
// starts from. Tenants load in parallel and the server rotates a refresh token
// as it redeems it, so the second to present the same one is refused.
type Seed struct {
	mu    sync.Mutex
	token string
	spent bool
}

func NewSeed(token string) *Seed { return &Seed{token: token} }

// redeem narrows the seed to tsg. tried separates "refused" from "no seed".
// The lock spans the exchange; that is the point.
func (s *Seed) redeem(ctx context.Context, client *http.Client, tokenURL, tsg string) (tok *Token, tried bool, err error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spent || s.token == "" {
		return nil, false, nil
	}

	tok, err = Refresh(ctx, client, tokenURL, s.token, tsg)
	if err != nil {
		// Only a rejected grant condemns the token. A refused scope is this
		// tenant's problem, not the next one's.
		if strings.Contains(err.Error(), "invalid_grant") {
			s.spent = true
		}
		return nil, true, err
	}
	// A different token back means the old one is spent; the same one means
	// the server does not rotate and the seed still works.
	if tok.RefreshToken != s.token {
		s.spent = true
	}
	return tok, true, nil
}
