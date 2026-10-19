package saseauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInteractiveRequired reports that no stored credential could produce a token
// and a human has to sign in. Callers surface it as a prompt rather than an
// error, then call SignIn.
var ErrInteractiveRequired = errors.New("interactive sign-in required")

// Source is an api.TokenSource backed by browser authentication. It prefers a
// stored refresh token, falls back to a silent browser attempt against a kept
// profile, and otherwise asks to be signed in. Token never opens a window: an
// unattended refresh must not block on one, so the decision to show a browser
// stays with the caller.
type Source struct {
	tsg  string
	opt  Options
	save func(refreshToken string) error

	mu       sync.Mutex
	refresh  string
	identity Identity
	lastTTL  time.Duration
}

// NewSource builds a Source for tsg. refreshToken may be empty. save, when not
// nil, is called with every new refresh token so it can be persisted; its error
// is returned to the caller that triggered the exchange.
func NewSource(tsg, refreshToken string, opt Options, save func(string) error) *Source {
	return &Source{tsg: tsg, opt: opt, save: save, refresh: refreshToken}
}

func (s *Source) RefreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

// accept records a freshly issued token and returns its access half.
// LastTTL is how long the most recent access token is good for.
func (s *Source) LastTTL() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTTL
}

// Identity is who the most recent token says you are. It is empty until a
// token has been obtained.
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
	// Why the stored credential was not good enough. Without it the only
	// error reported is the browser leg's, which says "no existing browser
	// session" for a tenant whose real problem is a rejected refresh token.
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

	// The account credential, narrowed to this tenant. Reached by a tenant
	// with nothing of its own and by one whose own credential the server has
	// since rejected -- skipping it in the second case is what sent a tenant
	// with a merely expired token all the way to the browser. Only one tenant
	// gets it; accept turns what comes back into that tenant's own.
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

// permanentError marks a failure that will not resolve without a human, so a
// refresh loop can stop rather than retry forever.
type permanentError struct{ err error }

func (e permanentError) Error() string   { return e.err.Error() }
func (e permanentError) Unwrap() error   { return e.err }
func (e permanentError) Temporary() bool { return false }

// SignIn runs the interactive flow, opening a browser window when no kept
// session can be reused, and stores the resulting refresh token.
func (s *Source) SignIn(ctx context.Context) error {
	tok, err := Login(ctx, s.tsg, s.opt)
	if err != nil {
		return err
	}
	_, _, err = s.accept(tok)
	return err
}

// Seed is the shared account credential this source falls back on, which is nil
// once the tenant has one of its own. Exposed so the wiring can be checked.
func (s *Source) Seed() *Seed { return s.opt.Seed }
