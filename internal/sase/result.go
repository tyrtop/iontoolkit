package sase

import (
	"context"
	"fmt"

	"tyrtop.com/iontk/internal/sase/ioncli"
)

type Result struct {
	ElementID string                 `json:"element_id"`
	Name      string                 `json:"name,omitempty"`
	HWID      string                 `json:"hw_id,omitempty"`
	Model     string                 `json:"model_name,omitempty"`
	Software  string                 `json:"software_version,omitempty"`
	Connected bool                   `json:"connected"`
	Error     string                 `json:"error,omitempty"`
	Commands  []ioncli.CommandOutput `json:"commands,omitempty"`
}

func runElement(ctx context.Context, client Client, o Options, target Target) (Result, error) {
	eid := target.ElementID
	ctx, cancel := context.WithTimeout(ctx, o.ElementTimeout)
	defer cancel()

	el, err := client.LookupElement(ctx, eid)
	if err != nil {
		return Result{}, err
	}

	prompt := el.Name + "#"

	res := Result{
		ElementID: eid,
		Name:      el.Name,
		HWID:      el.HWID,
		Model:     el.Model,
		Software:  el.Software,
		Connected: el.Connected,
	}
	res.Commands, err = attemptSession(ctx, client, o, target, eid, prompt)
	if err != nil {
		return res, fmt.Errorf("element %s: run: %w", eid, err)
	}
	return res, nil
}

func attemptSession(ctx context.Context, client Client, o Options, target Target, eid, prompt string) ([]ioncli.CommandOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, o.SessionTimeout)
	defer cancel()
	user, pass := target.creds(o)
	return client.ToolkitSession(ctx, eid, prompt, user, pass, o.Commands)
}
