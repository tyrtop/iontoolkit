package api

import (
	"context"
	_ "embed"

	"tyrtop.com/iontk/internal/endpoint"
)

var (
	//go:embed controller.yaml
	controllerYAML []byte
	//go:embed batch.yaml
	batchYAML []byte
	//go:embed inspect.yaml
	inspectYAML []byte
	//go:embed session.yaml
	sessionYAML []byte

	controller = endpoint.MustParse("controller", controllerYAML)
	batch      = endpoint.MustParse("batch", batchYAML)
	inspect    = endpoint.MustParse("inspect", inspectYAML)
	session    = endpoint.MustParse("session", sessionYAML)
)

type vars = map[string]string

func apiHost() string { return controller.Host() }

func sdwanURL(set *endpoint.Set, name string, v vars) (string, error) {
	e, err := set.SDWAN(name)
	if err != nil {
		return "", err
	}
	p, err := e.Fill(v)
	if err != nil {
		return "", err
	}
	return apiHost() + p, nil
}

func (s *Client) getNamed(ctx context.Context, what string, set *endpoint.Set, name string, v vars) ([]byte, string, error) {
	url, err := sdwanURL(set, name, v)
	if err != nil {
		return nil, "", err
	}
	body, err := s.get(ctx, what, url)
	return body, url, err
}
