package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TokenSource interface {
	Token(ctx context.Context) (string, time.Duration, error)
}

type serviceAccount struct {
	client *http.Client
	id     string
	secret string
	tsg    string
}

func (a serviceAccount) Token(ctx context.Context) (string, time.Duration, error) {
	return MintToken(ctx, a.client, a.id, a.secret, a.tsg)
}

func MintToken(ctx context.Context, client *http.Client, id, secret, tsg string) (string, time.Duration, error) {
	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"tsg_id:" + tsg},
	}

	u, err := controller.URL("service_token", nil)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("build with auth request: %w", err)
	}
	req.SetBasicAuth(id, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", browserUA)

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("auth: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("auth: status %s: %s", resp.Status, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("auth: json decode: %w", err)
	}
	return out.AccessToken, time.Duration(out.ExpiresIn) * time.Second, nil
}

func (s *Client) mint(ctx context.Context) (time.Duration, error) {
	s.mintMu.Lock()
	defer s.mintMu.Unlock()
	return s.mintLocked(ctx)
}

func (s *Client) mintLocked(ctx context.Context) (time.Duration, error) {
	tok, ttl, err := s.src.Token(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.activate(ctx, tok); err != nil {
		return 0, err
	}
	s.setToken(tok)
	s.setExpiry(ttl)
	return ttl, nil
}

func (s *Client) EnsureToken(ctx context.Context, need time.Duration) error {
	if s.src == nil || s.TokenGood(need) {
		return nil
	}
	s.mintMu.Lock()
	defer s.mintMu.Unlock()
	if s.TokenGood(need) {
		return nil
	}
	if _, err := s.mintLocked(ctx); err != nil {
		return fmt.Errorf("token check: %w", err)
	}
	return nil
}

const retryAfterFailure = 30 * time.Second

type temporary interface{ Temporary() bool }

func retryable(err error) bool {
	var t temporary
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return true
}

func (s *Client) TokenErrors() <-chan error { return s.tokenErrs }

func (s *Client) reportTokenError(err error) {
	if s.tokenErrs == nil {
		return
	}
	select {
	case s.tokenErrs <- err:
	default:
	}
}

func (s *Client) refreshLoop(ctx context.Context, ttl, margin time.Duration) {
	if s.tokenErrs != nil {
		defer close(s.tokenErrs)
	}

	timer := time.NewTimer(ttl - margin)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next, err := s.mint(ctx)
			if err != nil {
				s.reportTokenError(fmt.Errorf("token refresh: %w", err))
				if !retryable(err) {
					s.stopRefreshing()
					return
				}
				timer.Reset(retryAfterFailure)
				continue
			}
			ttl = next
			wait := ttl - margin
			if wait <= 0 {
				s.reportTokenError(fmt.Errorf("token lifetime %s is shorter than the refresh margin %s, so refreshing stopped", ttl, margin))
				s.stopRefreshing()
				return
			}
			timer.Reset(wait)
		}
	}
}

// Palo Alto needs an extra call to prime the token, or everything after it 403s
func (s *Client) activate(ctx context.Context, tok string) error {
	url, err := sdwanURL(controller, "profile", nil)
	if err != nil {
		return err
	}
	_, err = s.callAs(ctx, tok, "profile", http.MethodGet, url, nil, false)
	return err
}
