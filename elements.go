package main

import (
	"context"
	"fmt"
	"os"
)

type Result struct {
	ElementID string `json:"element_id"`
	Name string `json:"name,omitempty"`
	HWID string `json:"hw_id,omitempty"`
	Model string `json:"model_name,omitempty"`
	Software string `json:"software_version,omitempty"`
	Connected bool `json:"connected"`
	Output string `json:"output,omitempty"`
	Error string `json:"error,omitempty"`
}

func runElement(ctx context.Context, scm *SCM, cfg Config, eid string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.ElementTimeout)
	defer cancel()

	el, err := scm.lookupElement(ctx, eid)
	if err != nil {
		return Result{}, err
	}

	prompt := el.Name + "#"

	var out string
	var lastErr error

	for attempt := 1; attempt <= cfg.Attempts; attempt++ {
		out, lastErr = trySession(ctx, cfg, eid, prompt)
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
		Name: el.Name,
		HWID: el.HWID,
		Model: el.Model,
		Software: el.Software,
		Connected: el.Connected,
		Output: clean(out, prompt, cfg.Command),
	}, nil
}

func trySession(ctx context.Context, cfg Config, eid, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.SessionTimeout)
	defer cancel()
	conn, err := dialToolkit(ctx, cfg.Token, eid)
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	return runOnce(ctx, conn, prompt, cfg.IONUsername, cfg.IONPassword, cfg.Command)
}

