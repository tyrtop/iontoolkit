package main

import (
	"context"
	"fmt"
	"net/http"
)

type Result struct {
	ElementID string `json:"element_id"`
	Name string `json:"name, omitempty"`
	HWID string `json:"hw_id, omitempty"`
	Model string `json:"model_name, omitempty"`
	Software string `json:"software_version,omitempty"`
	Connected bool `json:"connected"`
	Output string `json:"output,omitempty"`
	Error string `json:"error, omitempty"`
}

func runElement(ctx context.Context, client *http.Client, cfg Config, eid string) (Result, error) {
	el, err := lookupElement(ctx, client, cfg, eid)
	if err != nil {
		return Result{}, err
	}

	conn, err := dialToolkit(ctx, cfg.Token, eid)
	if err != nil {
		return Result{}, fmt.Errorf("element %s: dial: %w", eid, err)
	}
	defer conn.CloseNow()


	prompt := el.Name + "#"
	out, err := runOnce(ctx, conn, prompt, cfg.Command)
	if err != nil {
		return Result{}, fmt.Errorf("element %s: run error: %w", eid, err)
	}

	return Result{
		ElementID: eid,
		Name: el.Name,
		HWID: el.HWID,
		Model: el.Model,
		Software: el.Software, 
		Connected: el.Connected,
		Output: clean(out, prompt),
	}, nil
}



