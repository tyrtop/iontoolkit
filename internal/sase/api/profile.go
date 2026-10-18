package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Profile is the operator the current token acts as. For an SSO-backed sign-in
// the name fields hold the subject rather than a person's name, so the readable
// identity comes from the token instead; what is useful here is proof the token
// works, plus the region and roles it resolved to.
type Profile struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Region   string   `json:"region"`
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"-"`
}

// Fetch decodes roles separately: they arrive as objects, not strings.
func (p *Profile) UnmarshalJSON(b []byte) error {
	type plain Profile
	var raw struct {
		plain
		Roles []struct {
			Name string `json:"name"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Profile(raw.plain)
	for _, r := range raw.Roles {
		if r.Name != "" {
			p.Roles = append(p.Roles, r.Name)
		}
	}
	return nil
}

// Profile fetches the operator profile. It doubles as a connection test: this
// is the call that proves a token is accepted by the SD-WAN API.
func (s *Client) Profile(ctx context.Context) (Profile, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return Profile{}, fmt.Errorf("profile: rate limit wait: %w", err)
	}

	url := "https://api.sase.paloaltonetworks.com/sdwan/v2.1/api/profile"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Profile{}, fmt.Errorf("build profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("profile: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Profile{}, fmt.Errorf("profile: status %s: %s", resp.Status, body)
	}

	var out Profile
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Profile{}, fmt.Errorf("profile: json decode: %w", err)
	}
	return out, nil
}
