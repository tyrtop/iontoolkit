package saseauth

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

type Seed struct {
	mu    sync.Mutex
	token string
	spent bool
}

func NewSeed(token string) *Seed { return &Seed{token: token} }

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
		if strings.Contains(err.Error(), "invalid_grant") {
			s.spent = true
		}
		return nil, true, err
	}
	if tok.RefreshToken != s.token {
		s.spent = true
	}
	return tok, true, nil
}
