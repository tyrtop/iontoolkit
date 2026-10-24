package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

func NewRuntime(st *config.Store, cfg *config.Config) *runtime.Runtime {
	rt := runtime.New()
	rt.RegisterOption(runtime.Option{
		Name: "theme", Help: "the colour scheme", Kind: runtime.OptString,
		Get: func(c *config.Config) string {
			if c.Theme == "" {
				return ui.Default().Name
			}
			return c.Theme
		},
		Put: func(c *config.Config, v string) { c.Theme = v },
		Check: func(v string) (string, error) {
			t, ok := ui.ThemeByName(v)
			if !ok {
				return "", fmt.Errorf("no theme is called %q", v)
			}
			return t.Name, nil
		},
		After: func(_ *runtime.Runtime, v string) { ui.UseTheme(v) },
	})
	rt.RegisterOption(runtime.Option{
		Name: "zone", Help: "the time zone every printed time is in", Kind: runtime.OptString,
		Get: func(c *config.Config) string {
			if c.Zone == "" {
				return clock.UTC
			}
			return c.Zone
		},
		Put: func(c *config.Config, v string) { c.Zone = v },
		Check: func(v string) (string, error) {
			if strings.TrimSpace(v) == "" {
				return "", fmt.Errorf("a zone is UTC, Local or a name like America/Chicago")
			}
			return v, clock.Valid(v)
		},
		After: func(_ *runtime.Runtime, v string) { _ = clock.Use(v) },
	})
	rt.UseConfig(st, cfg)
	rt.PreferAdded(Fixture != nil)
	rt.UseConnector(runtime.Connector{
		Check: func(st *config.Store, t config.Tenant) error { return TenantFlags(st, t, "").ValidateCreds() },
		Connect: func(ctx context.Context, st *config.Store, t config.Tenant) (*api.Client, error) {
			return NewClient(ctx, st, TenantFlags(st, t, ""), io.Discard)
		},
		Explain: func(err error) string { return ui.Truncate(ShortAuthError(err), 90) },
	})
	rt.UseDevice(runtime.Device{
		Login: func(st *config.Store, t config.Tenant, user string) (string, string) {
			f := TenantFlags(st, t, user)
			return f.IONUsername, f.IONPassword
		},
		Options: func(st *config.Store, t config.Tenant, user string, cmds []string) (sase.Options, error) {
			f := TenantFlags(st, t, user)
			f.Commands = cmds
			return f.Options(), nil
		},
		Session: func(c *api.Client) sase.Client {
			if Fixture != nil {
				return Fixture.SessionClient(Defaults.PagerOff, Defaults.PagerPrompts)
			}
			if c == nil {
				return nil
			}
			return c
		},
		Dialer: func(c *api.Client) ionterm.Dialer {
			if Fixture != nil {
				return Fixture.Dialer()
			}
			return toolkitDialer{c}
		},
		Writes: func() bool { return Defaults.AllowWrites },
	})
	rt.UseResolver(ResolveTenant)
	ReloadViews(rt)
	registerCommands(rt)
	return rt
}

type toolkitDialer struct{ c *api.Client }

func (d toolkitDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.c.DialToolkit(ctx, eid, cols, rows)
}
