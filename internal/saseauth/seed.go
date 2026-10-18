package saseauth

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// Seed is the tenant-less refresh token a browser sign-in leaves behind, shared
// by every tenant that has no credential of its own. It exists because that one
// token cannot be spent by several tenants at once: the authorization server
// rotates a refresh token as it redeems it, so the second tenant to present the
// same one is refused. Tenants load in parallel, which turned one shared token
// into a row of invalid_grant failures and sent every tenant but one off to the
// browser.
type Seed struct {
	mu    sync.Mutex
	token string
	spent bool
}

func NewSeed(token string) *Seed { return &Seed{token: token} }

// redeem narrows the seed to tsg. tried reports whether there was anything to
// redeem, so a caller can tell "the seed was refused" from "there was no seed".
// Only one tenant redeems at a time: the lock is held across the exchange,
// which is the point.
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
		// A rejected grant means the token itself is gone, so no other tenant
		// should present it. Any other failure — a scope this account cannot
		// have, a network blip — says nothing about the token, which the next
		// tenant is still entitled to try.
		if strings.Contains(err.Error(), "invalid_grant") {
			s.spent = true
		}
		return nil, true, err
	}
	// A server that rotated the token handed back a different one, and what it
	// replaced will not be accepted again. One that returned the same token
	// leaves the seed good for the next tenant.
	if tok.RefreshToken != s.token {
		s.spent = true
	}
	return tok, true, nil
}
