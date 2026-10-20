package saseauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrInteractiveRequired = errors.New("interactive sign-in required")

type Source struct {
	tsg  string
	opt  Options
	save func(refreshToken string) error

	mu       sync.Mutex
	refresh  string
	identity Identity
	lastTTL  time.Duration
}

func NewSource(tsg, refreshToken string, opt Options, save func(string) error) *Source {
	return &Source{tsg: tsg, opt: opt, save: save, refresh: refreshToken}
}

func (s *Source) RefreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

func (s *Source) LastTTL() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTTL
}

func (s *Source) Identity() Identity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity
}

func (s *Source) accept(tok *Token) (string, time.Duration, error) {
	s.mu.Lock()
	s.refresh = tok.RefreshToken
	s.lastTTL = tok.ExpiresIn
	if id := identityFrom(tok); id.Subject != "" || id.Email != "" || id.TSG != "" {
		s.identity = id
	}
	save := s.save
	s.mu.Unlock()

	if save != nil && tok.RefreshToken != "" {
		if err := save(tok.RefreshToken); err != nil {
			return tok.AccessToken, tok.ExpiresIn, fmt.Errorf("save refresh token: %w", err)
		}
	}
	return tok.AccessToken, tok.ExpiresIn, nil
}

func (s *Source) Token(ctx context.Context) (string, time.Duration, error) {
	var stored error

	if rt := s.RefreshToken(); rt != "" {
		tok, err := Refresh(ctx, s.opt.httpClient(), s.opt.token(), rt, s.tsg)
		if err == nil {
			return s.accept(tok)
		}
		if ctx.Err() != nil {
			return "", 0, err
		}
		stored = fmt.Errorf("stored refresh token rejected: %w", err)
	}

	if tok, tried, err := s.opt.Seed.redeem(ctx, s.opt.httpClient(), s.opt.token(), s.tsg); tried {
		if err == nil {
			return s.accept(tok)
		}
		if ctx.Err() != nil {
			return "", 0, err
		}
		if stored == nil {
			stored = fmt.Errorf("account credential rejected: %w", err)
		}
	}

	tok, err := LoginSilent(ctx, s.tsg, s.opt)
	if err == nil {
		return s.accept(tok)
	}
	if ctx.Err() != nil {
		return "", 0, err
	}
	if stored != nil {
		return "", 0, permanentError{fmt.Errorf("%w: %v; %v", ErrInteractiveRequired, stored, err)}
	}
	return "", 0, permanentError{fmt.Errorf("%w: %v", ErrInteractiveRequired, err)}
}

type permanentError struct{ err error }

func (e permanentError) Error() string   { return e.err.Error() }
func (e permanentError) Unwrap() error   { return e.err }
func (e permanentError) Temporary() bool { return false }

func (s *Source) SignIn(ctx context.Context) error {
	tok, err := Login(ctx, s.tsg, s.opt)
	if err != nil {
		return err
	}
	_, _, err = s.accept(tok)
	return err
}

func (s *Source) Seed() *Seed { return s.opt.Seed }
