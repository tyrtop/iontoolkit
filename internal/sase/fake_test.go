package sase

import (
	"context"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

type fakeClient struct {
	lookup  func(ctx context.Context, eid string) (api.Element, error)
	session func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error)
}

func (f *fakeClient) LookupElement(ctx context.Context, eid string) (api.Element, error) {
	return f.lookup(ctx, eid)
}

func (f *fakeClient) ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
	return f.session(ctx, eid, prompt, user, pass, cmds)
}

func testOpts() Options {
	return Options{
		Commands:       []string{"uptime"},
		Username:       "u",
		Password:       "p",
		Concurrency:    10,
		ElementTimeout: 60 * time.Second,
		SessionTimeout: 20 * time.Second,
		Attempts:       1,
	}
}
