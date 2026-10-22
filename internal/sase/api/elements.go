package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func (s *Client) ListElements(ctx context.Context) ([]Element, error) {
	return s.ListElementsFrom(ctx, ElementsPath)
}

func (s *Client) ListElementsFrom(ctx context.Context, path string) ([]Element, error) {
	if path == "" {
		path = ElementsPath
	}
	if err := checkRead(path); err != nil {
		return nil, fmt.Errorf("elements: %w", err)
	}
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("elements: rate limit wait: %w", err)
	}

	url := apiHost + path

	if s.verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("elements: build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elements: response: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elements: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elements: status %s: %s", resp.Status, body)
	}

	var wrapped struct {
		Items []Element `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}

	var bare []Element
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("elements: json decode: %w", err)
	}
	return bare, nil
}
