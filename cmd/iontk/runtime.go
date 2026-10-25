package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
)

var (
	tenantNotes []string
	theRuntime  *runtime.Runtime
)

func cliRuntime() *runtime.Runtime {
	if theRuntime != nil {
		return theRuntime
	}
	st := app.OpenStore()
	var cfg *config.Config
	if st != nil {
		cfg, _ = st.Config()
	}
	theRuntime = app.NewRuntime(st, cfg)
	tenantNotes, _ = app.StartTenant(theRuntime, flagTenant)
	return theRuntime
}

func noteTenant() {
	for _, l := range tenantNotes {
		fmt.Fprintln(os.Stderr, l)
	}
	tenantNotes = nil
}

func execPrinting(ctx context.Context, out io.Writer, name string, args map[string]string, rest []string, doit bool) error {
	rt := cliRuntime()
	if c, ok := rt.Command(name); ok && c.Slow {
		noteTenant()
	}
	return rt.Exec(&runtime.Call{
		Ctx: ctx, Args: args, Rest: rest, DoIt: doit,
		Out: func(line string) { fmt.Fprintln(out, line) },
	}, name)
}
