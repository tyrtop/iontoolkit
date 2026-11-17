package api

import "context"

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Client) ListSitesFrom(ctx context.Context, path string) ([]Site, error) {
	return listFrom[Site](ctx, s, "sites", path)
}
