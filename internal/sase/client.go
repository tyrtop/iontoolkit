package sase

import (
	"context"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

type Client interface {
	LookupElement(ctx context.Context, eid string) (api.Element, error)
	ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error)
}

var _ Client = (*api.Client)(nil)
