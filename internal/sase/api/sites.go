package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Client) ListSites(ctx context.Context) ([]Site, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("sites: rate limit wait: %w", err)
	}

	url := scmBase + "/sites"

	if s.verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sites: build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sites: response: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sites: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sites: status %s: %s", resp.Status, body)
	}

	var wrapped struct {
		Items []Site `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}

	var bare []Site
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("sites: json decode: %w", err)
	}
	return bare, nil
}
