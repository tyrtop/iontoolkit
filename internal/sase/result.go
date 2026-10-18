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

	// Re-running a session that connected would repeat commands that may
	// already have taken effect. The retry that matters is the dial, which the
	// client does for itself.
	outs, err := attemptSession(ctx, client, o, target, eid, prompt)
	if err != nil {
		return Result{}, fmt.Errorf("element %s: run: %w", eid, err)
	}
	return Result{
		ElementID: eid,
		Name:      el.Name,
		HWID:      el.HWID,
		Model:     el.Model,
		Software:  el.Software,
		Connected: el.Connected,
		Commands:  outs,
	}, nil
}

func attemptSession(ctx context.Context, client Client, o Options, target Target, eid, prompt string) ([]ioncli.CommandOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, o.SessionTimeout)
	defer cancel()
	user, pass := target.creds(o)
	return client.ToolkitSession(ctx, eid, prompt, user, pass, o.Commands)
}
