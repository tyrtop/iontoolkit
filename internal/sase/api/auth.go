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

const authURL = "https://auth.apps.paloaltonetworks.com/oauth2/access_token"

// TokenSource produces access tokens and says how long each one lasts, so that
// refreshLoop can schedule the next one. Implementations may be anything from a
// service account to a browser sign-in.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(form.Encode()))
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
	tok, ttl, err := s.src.Token(ctx)
	if err != nil {
		return 0, err
	}
	s.setToken(tok)
	if err := s.activate(ctx); err != nil {
		return 0, err
	}
	return ttl, nil
}

// retryAfterFailure is how long to wait before trying again after a refresh
// that could plausibly succeed later. Waiting out the whole token lifetime
// turns a momentary network problem into a quarter hour of 401s.
const retryAfterFailure = 30 * time.Second

// temporary is implemented by errors that know whether retrying could help. A
// TokenSource uses it to say that nothing will change without a human.
type temporary interface{ Temporary() bool }

func retryable(err error) bool {
	var t temporary
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return true
}

// TokenErrors reports refresh failures so the caller can decide what to say
// about them. The channel is buffered and sends are dropped rather than
// blocked, so a caller that never reads it costs nothing. It closes when
// refreshing stops for good, which is the signal that the credential needs
// human attention.
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
				// A credential that needs a human back at the keyboard will not
				// start working on its own, so stop instead of spinning. Signing
				// in again builds a new client with a new loop.
				if !retryable(err) {
					return
				}
				timer.Reset(retryAfterFailure)
				continue
			}
			ttl = next
			wait := ttl - margin
			if wait <= 0 {
				s.reportTokenError(fmt.Errorf("token lifetime %s is shorter than the refresh margin %s, so refreshing stopped", ttl, margin))
				return
			}
			timer.Reset(wait)
		}
	}
}

// Palo Alto requires an extra call to prime the token. Without this call, subsequent calls return 403 errors.
func (s *Client) activate(ctx context.Context) error {
	url := "https://api.sase.paloaltonetworks.com/sdwan/v2.1/api/profile"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("profile: status %s: %s", resp.Status, body)
	}
	return nil
}
