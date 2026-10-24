package write

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Sender interface {
	Send(ctx context.Context, method, path string, body []byte) ([]byte, error)
}

const LogName = "writes.log"

type Guard struct {
	Tenant string
	Log    string

	now func() time.Time
}

type entry struct {
	At      string   `json:"at"`
	Tenant  string   `json:"tenant,omitempty"`
	What    string   `json:"what,omitempty"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Changes []Change `json:"changes,omitempty"`
	Body    any      `json:"body,omitempty"`
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
}

func (g Guard) Ready(w Spec) error {
	if !w.Verified {
		return unverified(w)
	}
	return nil
}

func unverified(w Spec) error {
	return fmt.Errorf("%s is not verified — no HAR has shown the portal sending it, so iontk shows the change but will not send it; set verified: true in %s once one has", w.Name, FileName)
}

func (g Guard) Send(ctx context.Context, s Sender, method, path string, body []byte, what string) ([]byte, error) {
	var raw any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &raw)
	}
	return g.send(ctx, s, entry{What: what, Method: method, Path: path, Body: raw}, body)
}

func (g Guard) Apply(ctx context.Context, s Sender, w Spec, p Plan) ([]byte, error) {
	if p.Empty() {
		return nil, fmt.Errorf("%s: nothing to change", p.Label)
	}
	if !w.Verified {
		return nil, unverified(w)
	}
	body, err := p.Body()
	if err != nil {
		return nil, fmt.Errorf("%s: encode: %w", p.Label, err)
	}
	return g.send(ctx, s, entry{What: w.Name + " " + p.Label, Method: p.Method, Path: p.Path, Changes: p.Changes}, body)
}

func (g Guard) send(ctx context.Context, s Sender, e entry, body []byte) ([]byte, error) {
	answer, err := s.Send(ctx, e.Method, e.Path, body)
	e.OK = err == nil
	if err != nil {
		e.Error = err.Error()
	}
	if lerr := g.record(e); lerr != nil && err == nil {
		return answer, fmt.Errorf("sent, but the write log failed: %w", lerr)
	}
	return answer, err
}

func (g Guard) record(e entry) error {
	if g.Log == "" {
		return nil
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	e.At = now().UTC().Format(time.RFC3339)
	e.Tenant = g.Tenant
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(g.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
