package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const authURL = "https://auth.apps.paloaltonetworks.com/oauth2/access_token"

type auth struct {
	client *http.Client
	id     string
	secret string
	tsg    string
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
	tok, ttl, err := MintToken(ctx, s.auth.client, s.auth.id, s.auth.secret, s.auth.tsg)
	if err != nil {
		return 0, err
	}
	s.setToken(tok)
	if err := s.activate(ctx); err != nil {
		return 0, err
	}
	return ttl, nil
}

func (s *Client) refreshLoop(ctx context.Context, ttl, margin time.Duration) {
	timer := time.NewTimer(ttl - margin)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next, err := s.mint(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "token refresh:", err)
				next = ttl
			}
			ttl = next
			wait := ttl - margin
			if wait <= 0 {
				fmt.Fprintf(os.Stderr, "token lifetime %s is shorter than refresh margin %s, stopping refresh\n", ttl, margin)
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
