package app

import (
	"context"
	"fmt"
	"io"
	"os"
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
	runAsFromEnv(rt)
	rt.PreferAdded(Fixture != nil)
	rt.UseConnector(runtime.Connector{
		Check: func(st *config.Store, t config.Tenant) error { return TenantFlags(st, t, "").ValidateCreds() },
		Connect: func(ctx context.Context, st *config.Store, t config.Tenant) (*api.Client, error) {
			return NewClient(ctx, st, TenantFlags(st, t, ""), io.Discard)
		},
		Explain: func(err error) string { return ui.Truncate(ShortAuthError(err), 90) },
		Refused: func(err error) bool { return IsAuthFailure(err.Error()) },
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
	registerCommands(rt)
	Reload(rt)
	return rt
}

func Reload(rt *runtime.Runtime) List {
	ReloadViews(rt)
	l := LoadList(rt.Store())
	UseList(rt, l)
	ReloadWrites(rt)
	ReloadCommands(rt)
	return l
}

func reloadCommand() runtime.Command {
	return runtime.Command{
		Name: "reload", Help: "read every YAML file again: data views, the element list, writes, workflows and actions",
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			l := Reload(rt)
			c.Result = l
			var problems []string
			for _, p := range []string{rt.ViewsErr(), l.Err, writesProblem(rt)} {
				if p != "" {
					problems = append(problems, p)
				}
			}
			problems = append(problems, rt.Warnings()...)
			if len(problems) > 0 {
				return fmt.Errorf("%s", strings.Join(problems, "; "))
			}
			c.Say("reloaded")
			return nil
		},
	}
}

func writesProblem(rt *runtime.Runtime) string {
	_, problem := rt.Writes()
	return problem
}

func runAsFromEnv(rt *runtime.Runtime) {
	u := os.Getenv("ION_USER")
	cfg := rt.Config()
	if u == "" || cfg == nil {
		return
	}
	for _, t := range cfg.Tenants {
		rt.SetRunAs(t.TSGID, u)
	}
}

type toolkitDialer struct{ c *api.Client }

func (d toolkitDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.c.DialToolkit(ctx, eid, cols, rows)
}
