package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
)

func cliRuntime() *runtime.Runtime {
	st := app.OpenStore()
	var cfg *config.Config
	if st != nil {
		cfg, _ = st.Config()
	}
	rt := app.NewRuntime(st, cfg)
	runAsFromEnv(rt)
	if cfg != nil {
		if t, ok := app.ResolveTenant(cfg, flagTenant); ok {
			rt.UseTenant(t)
			rt.SetIONUser(t.DefaultUser())
		}
	}
	var once sync.Once
	var p runtime.Portal
	var tenant runtime.Tenant
	var perr error
	rt.UsePortal(func(ctx context.Context) (runtime.Portal, runtime.Tenant, error) {
		once.Do(func() {
			f, err := resolveFlags(os.Stderr)
			if err == nil {
				err = f.ValidateCreds()
			}
			if err != nil {
				perr = err
				return
			}
			c, err := app.NewClient(ctx, st, f, os.Stderr)
			p, tenant, perr = c, runtime.Tenant{TSG: f.TSGID}, err
		})
		return p, tenant, perr
	})
	return rt
}

func execPrinting(ctx context.Context, out io.Writer, name string, args map[string]string, rest []string, doit bool) error {
	return cliRuntime().Exec(&runtime.Call{
		Ctx: ctx, Args: args, Rest: rest, DoIt: doit,
		Out: func(line string) { fmt.Fprintln(out, line) },
	}, name)
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
