package api

import (
	"context"
	"encoding/json"
	"fmt"
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
	body, _, err := s.getNamed(ctx, "profile", controller, "profile", nil)
	if err != nil {
		return Profile{}, err
	}
	var out Profile
	if err := json.Unmarshal(body, &out); err != nil {
		return Profile{}, fmt.Errorf("profile: json decode: %w", err)
	}
	return out, nil
}
