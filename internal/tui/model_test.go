package tui

import (
	"context"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
)

func newTUIModel(ctx context.Context, store *config.Store, cfg *config.Config) tuiModel {
	rt := app.NewRuntime(store, cfg)
	_, _ = app.StartTenant(rt, Tenant)
	return modelOn(ctx, rt)
}
