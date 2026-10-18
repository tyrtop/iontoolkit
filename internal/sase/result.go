package sase

import (
	"context"
	"fmt"
	"os"

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

	var outs []ioncli.CommandOutput
	var lastErr error

	for attempt := 1; attempt <= o.Attempts; attempt++ {
		outs, lastErr = attemptSession(ctx, client, o, target, eid, prompt)
		if lastErr == nil {
			break
		}
		if o.Verbose {
			fmt.Fprintf(os.Stderr, "element %s: attempt %d: %v\n", eid, attempt, lastErr)
		}
	}
	if lastErr != nil {
		return Result{}, fmt.Errorf("element %s: run: %w", eid, lastErr)
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
