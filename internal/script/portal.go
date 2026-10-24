package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/tmpl"
)

type Getter interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
}

type Write struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

type Planned interface {
	PlannedWrites() []Write
}

type Portal struct {
	Ctx    context.Context
	Client interface {
		Getter
		write.Sender
	}
	Guard write.Guard
	Vars  map[string]string
	What  string

	DoIt    bool
	Planned *[]Write
}

func (p Portal) Get() func(string, map[string]string) (any, error) {
	return func(path string, extra map[string]string) (any, error) {
		full, err := p.fill(path, extra)
		if err != nil {
			return nil, err
		}
		b, err := p.Client.GetJSON(p.Ctx, full)
		if err != nil {
			return nil, err
		}
		return decode(full, b)
	}
}

func (p Portal) Send() func(string, string, any, map[string]string) (any, error) {
	return func(method, path string, body any, extra map[string]string) (any, error) {
		full, err := p.fill(path, extra)
		if err != nil {
			return nil, err
		}
		if !p.DoIt {
			return p.record(method, full, body), nil
		}
		var raw []byte
		if body != nil {
			if raw, err = json.Marshal(body); err != nil {
				return nil, fmt.Errorf("encode %s body: %w", full, err)
			}
		}
		b, err := p.Guard.Send(p.Ctx, p.Client, method, full, raw, p.What)
		if err != nil {
			return nil, err
		}
		if p.Planned != nil {
			*p.Planned = append(*p.Planned, Write{Method: method, Path: full, Body: body})
		}
		return decode(full, b)
	}
}

func (p Portal) record(method, path string, body any) any {
	if p.Planned != nil {
		*p.Planned = append(*p.Planned, Write{Method: method, Path: path, Body: body})
	}
	id := fmt.Sprintf("dry-run-%d", len(p.plannedSoFar()))
	answer := map[string]any{"id": id}
	if m, ok := body.(map[string]any); ok {
		for k, v := range m {
			answer[k] = v
		}
		if _, has := m["id"]; !has {
			answer["id"] = id
		}
	}
	return answer
}

func (p Portal) plannedSoFar() []Write {
	if p.Planned == nil {
		return nil
	}
	return *p.Planned
}

func (p Portal) fill(path string, extra map[string]string) (string, error) {
	full := Fill(path, p.Vars, extra)
	if err := CheckPath(full); err != nil {
		return "", err
	}
	return full, nil
}

func Fill(path string, vars, extra map[string]string) string {
	for _, from := range []map[string]string{vars, extra} {
		for k, v := range from {
			path = strings.ReplaceAll(path, "{"+k+"}", v)
		}
	}
	return path
}

func CheckPath(path string) error {
	if i := strings.Index(path, "{"); i >= 0 {
		return fmt.Errorf("%s: nothing fills %s", path, path[i:min(i+20, len(path))])
	}
	return tmpl.CheckPath(path)
}

func decode(path string, b []byte) (any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var body any
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return body, nil
}
