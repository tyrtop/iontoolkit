package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Profile struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Region   string   `json:"region"`
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"-"`
}

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
