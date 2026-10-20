package saseauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/logging"
)

var ErrInteractiveRequired = errors.New("interactive sign-in required")

var ErrNoCredential = errors.New("no stored sign-in for this tenant")

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
	logging.Mask(refreshToken)
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

func (s *Source) accept(tok *Token, log *slog.Logger) (string, time.Duration, error) {
	logging.Mask(tok.AccessToken)
	logging.Mask(tok.RefreshToken)
	logging.Mask(tok.IDToken)

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
			log.Error("could not save the refresh token", "err", err)
			return tok.AccessToken, tok.ExpiresIn, fmt.Errorf("save refresh token: %w", err)
		}
		log.Info("refresh token saved for this tenant", "refresh_token", tok.RefreshToken)
	}
	log.Info("access token accepted", "ttl", tok.ExpiresIn.String(), "scope", tok.Scope)
	return tok.AccessToken, tok.ExpiresIn, nil
}

func (s *Source) Token(ctx context.Context) (string, time.Duration, error) {
	log := s.log()
	var stored, account error

	if rt := s.RefreshToken(); rt != "" {
		log.Info("trying the stored refresh token", "refresh_token", rt)
		tok, err := Refresh(ctx, s.opt.httpClient(), s.opt.token(), rt, s.tsg)
		if err == nil {
			log.Info("stored refresh token accepted", "rotated", tok.RefreshToken != rt)
			return s.accept(tok, log)
		}
		if ctx.Err() != nil {
			return "", 0, err
		}
		log.Warn("stored refresh token rejected", "err", err)
		stored = fmt.Errorf("stored refresh token rejected: %w", err)
	} else {
		log.Info("no stored refresh token for this tenant")
	}

	if tok, tried, err := s.opt.Seed.redeem(ctx, s.opt.httpClient(), s.opt.token(), s.tsg, log); tried {
		if err == nil {
			return s.accept(tok, log)
		}
		if ctx.Err() != nil {
			return "", 0, err
		}
		account = fmt.Errorf("account credential rejected: %w", err)
	}

	tok, err := LoginSilent(ctx, s.tsg, s.opt)
	if err == nil {
		log.Info("silent browser sign-in worked")
		return s.accept(tok, log)
	}
	if ctx.Err() != nil {
		return "", 0, err
	}
	log.Warn("no credential worked; an interactive sign-in is needed", "silent", err)
	if stored != nil {
		return "", 0, permanentError{fmt.Errorf("%w: %v; %w", ErrInteractiveRequired, stored, err)}
	}
	if account != nil {
		return "", 0, permanentError{fmt.Errorf("%w: %w; %v; %w",
			ErrInteractiveRequired, ErrNoCredential, account, err)}
	}
	return "", 0, permanentError{fmt.Errorf("%w: %w; %w", ErrInteractiveRequired, ErrNoCredential, err)}
}

type permanentError struct{ err error }

func (e permanentError) Error() string   { return e.err.Error() }
func (e permanentError) Unwrap() error   { return e.err }
func (e permanentError) Temporary() bool { return false }

func (s *Source) SignIn(ctx context.Context) error {
	log := s.log()
	log.Info("interactive sign-in asked for")

	tok, err := Login(ctx, s.tsg, s.opt)
	if err != nil {
		log.Warn("interactive sign-in failed", "err", err)
		return err
	}
	_, _, err = s.accept(tok, log)
	return err
}

func (s *Source) log() *slog.Logger { return s.opt.log().With("tsg", s.tsg) }

func (s *Source) Seed() *Seed { return s.opt.Seed }
