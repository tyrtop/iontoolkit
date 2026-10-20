package saseauth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/logging"
)

type Seed struct {
	mu    sync.Mutex
	token string
	spent bool
}

func NewSeed(token string) *Seed {
	logging.Mask(token)
	return &Seed{token: token}
}

func (s *Seed) redeem(ctx context.Context, client *http.Client, tokenURL, tsg string, log *slog.Logger) (tok *Token, tried bool, err error) {
	if s == nil {
		log.Info("no account credential to narrow")
		return nil, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spent || s.token == "" {
		log.Info("account credential skipped", "reason", reason(s.spent))
		return nil, false, nil
	}

	log.Info("redeeming the account credential", "seed", s.token)
	tok, err = Refresh(ctx, client, tokenURL, s.token, tsg)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			s.spent = true
		}
		log.Warn("account credential refused", "err", err, "spent", s.spent)
		return nil, true, err
	}
	if tok.RefreshToken != s.token {
		s.spent = true
	}
	log.Info("account credential redeemed", "narrowed_to_this_tenant", s.spent)
	return tok, true, nil
}

func reason(spent bool) string {
	if spent {
		return "already spent by another tenant"
	}
	return "none is held"
}
