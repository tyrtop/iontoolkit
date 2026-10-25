package app

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/script"
)

const EndpointPrefix = "endpoint."

type EndpointCall struct {
	Answer any
	Writes []script.Write
}

func (r EndpointCall) PlannedWrites() []script.Write { return r.Writes }

func (r EndpointCall) MarshalJSON() ([]byte, error) { return json.Marshal(r.Answer) }

func callEndpoint(rt *runtime.Runtime, c *runtime.Call, what string, e endpoint.Endpoint, tsg, where string) error {
	res := EndpointCall{}
	defer func() { c.Result = res }()
	path, err := e.Fill(c.Args)
	if err != nil {
		return err
	}
	p, tenant, err := rt.PortalFor(c.Ctx, tsg)
	if err != nil {
		return err
	}
	if e.Method == http.MethodGet {
		res.Answer, err = getJSON(c, p, path)
		return err
	}
	var body any
	if b := c.Args["body"]; b != "" {
		if err := json.Unmarshal([]byte(b), &body); err != nil {
			return fmt.Errorf("%s body: %w", e.Name, err)
		}
	}
	res.Writes = []script.Write{{Method: e.Method, Path: path, Body: body}}
	if !c.DoIt {
		c.Say("plan %s %s%s", e.Method, path, where)
		res.Answer = body
		return nil
	}
	c.Say("send %s %s%s", e.Method, path, where)
	var raw []byte
	if body != nil {
		if raw, err = json.Marshal(body); err != nil {
			return fmt.Errorf("%s body: %w", e.Name, err)
		}
	}
	answer, err := guardFor(rt, tenant).Send(c.Ctx, p, e.Method, path, raw, what)
	if err != nil {
		return err
	}
	if len(answer) > 0 {
		if err := json.Unmarshal(answer, &res.Answer); err != nil {
			return fmt.Errorf("%s answer: %w", e.Name, err)
		}
	}
	return nil
}

func getJSON(c *runtime.Call, p runtime.Portal, path string) (any, error) {
	b, err := p.GetJSON(c.Ctx, path)
	if err != nil {
		return nil, err
	}
	var out any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("%s: json decode: %w", path, err)
		}
	}
	return out, nil
}
