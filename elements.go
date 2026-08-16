package main

import (
	"context"
	"fmt"
	"os"
)

type CommandOutput struct {
	Command string `json:"command"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Result struct {
	ElementID string          `json:"element_id"`
	Name      string          `json:"name,omitempty"`
	HWID      string          `json:"hw_id,omitempty"`
	Model     string          `json:"model_name,omitempty"`
	Software  string          `json:"software_version,omitempty"`
	Connected bool            `json:"connected"`
	Error     string          `json:"error,omitempty"`
	Commands  []CommandOutput `json:"commands,omitempty"`
}

func runElement(ctx context.Context, scm *SCM, cfg Config, eid string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.ElementTimeout)
	defer cancel()

	el, err := scm.lookupElement(ctx, eid)
	if err != nil {
		return Result{}, err
	}

	prompt := el.Name + "#"

	var outs []CommandOutput
	var lastErr error

	for attempt := 1; attempt <= cfg.Attempts; attempt++ {
		outs, lastErr = trySession(ctx, cfg, eid, prompt)
		if lastErr == nil {
			break
		}
		if cfg.Verbose {
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

func trySession(ctx context.Context, cfg Config, eid, prompt string) ([]CommandOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.SessionTimeout)
	defer cancel()
	conn, err := dialToolkit(ctx, cfg.Token, eid)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	return runOnce(ctx, conn, prompt, cfg.IONUsername, cfg.IONPassword, cfg.Commands)
}
