package saseauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/logging"
)

const (
	AuthorizeURL = "https://auth.apps.paloaltonetworks.com/am/oauth2/authorize"
	TokenURL     = "https://auth.apps.paloaltonetworks.com/am/oauth2/access_token"
	ClientID     = "scm"
	RedirectURI  = "https://stratacloudmanager.paloaltonetworks.com/authentication/callback"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type Token struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	Scope        string
	ExpiresIn    time.Duration
}

type pkce struct {
	verifier  string
	challenge string
	state     string
}

func newPKCE() (pkce, error) {
	verifier, err := randomString(64)
	if err != nil {
		return pkce{}, fmt.Errorf("pkce verifier: %w", err)
	}
	state, err := randomString(24)
	if err != nil {
		return pkce{}, fmt.Errorf("pkce state: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	return pkce{
		verifier:  verifier,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		state:     state,
	}, nil
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func scopeFor(tsg string) string {
	if tsg == "" {
		return "openid profile email tsg_id:none"
	}
	return "openid profile email tsg_id:" + tsg
}

func (p pkce) authorizeURL(base, tsg string, silent bool) string {
	q := url.Values{
		"client_id":             {ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {RedirectURI},
		"scope":                 {scopeFor(tsg)},
		"state":                 {p.state},
		"code_challenge":        {p.challenge},
		"code_challenge_method": {"S256"},
	}
	if silent {
		q.Set("prompt", "none")
	}
	return base + "?" + q.Encode()
}

func CodeFromCallback(raw, want string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse callback url: %w", err)
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		if d := q.Get("error_description"); d != "" {
			return "", fmt.Errorf("authorization failed: %s: %s", e, d)
		}
		return "", fmt.Errorf("authorization failed: %s", e)
	}
	code := q.Get("code")
	if code == "" {
		return "", fmt.Errorf("callback url has no code parameter")
	}
	if want != "" && q.Get("state") != want {
		return "", fmt.Errorf("callback state mismatch")
	}
	return code, nil
}

func exchange(ctx context.Context, client *http.Client, tokenURL string, form url.Values) (*Token, error) {
	form.Set("client_id", ClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("token: status %s: %s", resp.Status, body)
	}

	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("token: json decode: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token: response has no access_token")
	}
	return &Token{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		IDToken:      out.IDToken,
		Scope:        out.Scope,
		ExpiresIn:    time.Duration(out.ExpiresIn) * time.Second,
	}, nil
}

func ExchangeCode(ctx context.Context, client *http.Client, tokenURL, code, verifier string) (*Token, error) {
	logging.Mask(code)
	logging.Mask(verifier)
	return exchange(ctx, client, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {RedirectURI},
	})
}

func Refresh(ctx context.Context, client *http.Client, tokenURL, refreshToken, tsg string) (*Token, error) {
	tok, err := exchange(ctx, client, tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {scopeFor(tsg)},
	})
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}
