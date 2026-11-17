package api

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Client) ListElementsFrom(ctx context.Context, path string) ([]Element, error) {
	return listFrom[Element](ctx, s, "elements", path)
}

func listFrom[T any](ctx context.Context, s *Client, name, path string) ([]T, error) {
	if path == "" {
		return nil, fmt.Errorf("%s: the element list's file names no %s endpoint", name, name)
	}
	if err := checkRead(path); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	body, err := s.get(ctx, name, apiHost()+path)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Items []T `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}
	var bare []T
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("%s: json decode: %w", name, err)
	}
	return bare, nil
}
