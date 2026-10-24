package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/tui"
)

type selection struct {
	elements []string
	rows     []runtime.Element
	rt       *runtime.Runtime
	stdin    bool
}

func (s selection) crossTenant() bool { return len(s.rows) > 0 }

func flagSelection() (selection, error) {
	els, err := gatherElements(flagElement, flagElementsPath)
	if err != nil {
		return selection{}, err
	}
	return selection{elements: els, stdin: flagElementsPath == "-"}, nil
}

func runBatch(ctx context.Context, sel selection) error {
	if len(flagCommands) == 0 {
		return fmt.Errorf("--cmd is required for batch mode")
	}
	rt := sel.rt
	if rt == nil {
		rt = cliRuntime()
	}

	var f app.Flags
	var err error
	if sel.crossTenant() {
		f = app.BaseFlags()
	} else if f, err = resolveFlags(os.Stderr); err != nil {
		return err
	}
	f.Elements = sel.elements
	f.Commands = app.SplitCommands(flagCommands)

	if err := rt.CheckCommands(f.Commands); err != nil {
		return fmt.Errorf("%w; override with --allow-writes or set allow_writes", err)
	}

	if sel.crossTenant() {
		err = f.ValidateRun()
	} else {
		err = f.Validate()
	}
	if err != nil {
		return err
	}

	format, orient, dest, err := batchOutput()
	if err != nil {
		return err
	}

	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	var events <-chan sase.Event
	var clients []*api.Client
	if sel.crossTenant() {
		if events, err = rt.StartBatch(ctx, f.Commands, sel.rows); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range sel.rows {
			if c := rt.Clients().Get(r.TSG); c != nil && !seen[r.TSG] {
				seen[r.TSG] = true
				clients = append(clients, c)
			}
		}
	} else {
		opts := f.Options()
		client, cerr := app.NewClient(ctx, app.OpenStore(), f, os.Stderr)
		if cerr != nil {
			return cerr
		}
		targets := sase.Targets(client, f.Elements)
		if err := opts.ValidateFor(targets); err != nil {
			return err
		}
		if app.Fixture != nil {
			playback := app.Fixture.SessionClient(f.PagerOff, f.PagerPrompts)
			for i := range targets {
				targets[i].Client = playback
			}
		}
		events = rt.RunBatch(ctx, opts, targets)
		clients = []*api.Client{client}
	}

	for _, c := range clients {
		go watchCredential(c.TokenErrors(), c.RefreshStopped, stopRun, os.Stderr)
	}

	out, where, err := output.Open(flagOut, dest, format, time.Now())
	if err != nil {
		return err
	}
	defer out.Close()
	if where != "stdout" {
		fmt.Fprintln(os.Stderr, "writing", format, "results to", where)
	}
	sinkOpts := []output.Option{output.WithOrientation(orient)}
	if where == "stdout" {
		if cols, _, serr := term.GetSize(int(os.Stdout.Fd())); serr == nil && cols > 0 {
			sinkOpts = append(sinkOpts, output.WithWidth(cols))
		}
	}
	sink := output.NewSink(out, format, sinkOpts...)

	var total int
	var failed int

	if !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		total, failed, err = tui.DrainLive(ctx, events, f.Commands, len(f.Elements), sink, sel.stdin)
	} else {
		total, failed, err = drainPlain(events, len(f.Elements), sink)
	}
	if err != nil {
		return err
	}

	if err := sink.Close(); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", where, err)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d elements failed", failed, total)
	}
	return nil
}

func batchOutput() (format, orient, dir string, err error) {
	format, orient, dir = output.FormatJSON, output.OrientION, ""
	if st := app.OpenStore(); st != nil {
		if cfg, cerr := st.Config(); cerr == nil {
			format, orient, dir = cfg.BatchFormat, cfg.BatchOrient, cfg.BatchOutDir()
		}
	}
	if flagFormat != "" {
		format = flagFormat
	}
	if flagOrient != "" {
		orient = flagOrient
	}
	if format, err = output.NormalizeFormat(format); err != nil {
		return "", "", "", err
	}
	if orient, err = output.NormalizeOrientation(orient); err != nil {
		return "", "", "", err
	}
	return format, orient, dir, nil
}

func drainPlain(events <-chan sase.Event, total int, sink output.Sink) (int, int, error) {
	failed := 0
	for ev := range events {
		if !ev.Done {
			continue
		}
		if err := sink.Add(ev.Index, ev.Result); err != nil {
			return total, failed, fmt.Errorf("write result: %w", err)
		}
		if ev.Result.Error != "" {
			failed++
			fmt.Fprintln(os.Stderr, ev.Result.Error)
		}
	}
	return total, failed, nil
}

func watchCredential(ch <-chan error, dead func() bool, stop func(), w io.Writer) {
	for err := range ch {
		fmt.Fprintln(w, err)
	}
	if dead() {
		fmt.Fprintln(w, "sign-in expired and could not be renewed — stopping; run `iontk auth login` and try again")
		stop()
	}
}
