package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
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
	cmd, _ := rt.Command(name)
	if cmd.Slow {
		noteTenant()
	}
	format, err := runFormat(name)
	if err != nil {
		return err
	}
	lines := out
	if format != "" {
		lines = os.Stderr
	}
	var spin *spinner
	if cmd.Slow && !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		spin = startSpinner(rt, strings.ReplaceAll(name, ".", " "))
	}
	err = rt.Exec(&runtime.Call{
		Ctx: ctx, Args: args, Rest: rest, DoIt: doit,
		Out: func(line string) {
			if spin != nil {
				spin.println(lines, line)
				return
			}
			fmt.Fprintln(lines, line)
		},
	}, name)
	if spin != nil {
		spin.stop()
	}
	if format == "" {
		return err
	}
	to := flagOut
	if to == "" {
		to = "-"
	}
	saved := rt.Exec(&runtime.Call{Ctx: ctx, Args: map[string]string{"format": format, "to": to},
		Out: func(line string) { fmt.Fprintln(os.Stderr, line) }}, "runs.save")
	if err != nil {
		return err
	}
	return saved
}

func runFormat(name string) (string, error) {
	if !strings.HasPrefix(name, app.WorkflowPrefix) || (flagFormat == "" && flagOut == "") {
		return "", nil
	}
	if f, _ := output.NormalizeFormat(flagFormat); f == output.FormatText {
		return "", nil
	}
	return runtime.RunFormat(flagFormat)
}
