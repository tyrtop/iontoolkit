package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/tui"
)

type selection struct {
	elements []string
	rows     []runtime.Element
	rt       *runtime.Runtime
	stdin    bool
}

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
	if len(sel.elements) == 0 {
		return fmt.Errorf("no elements: give --element, --elements-path or --search")
	}
	rt := sel.rt
	if rt == nil {
		rt = cliRuntime()
	}
	noteTenant()
	cmds := runtime.SplitCommands(flagCommands...)
	format, orient, dest, err := batchOutput(rt)
	if err != nil {
		return err
	}

	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	rows := sel.rows
	if len(rows) == 0 {
		rows = rt.KnownRows(ctx, sel.elements, func(l string) { fmt.Fprintln(os.Stderr, l) })
	}
	events, err := rt.StartBatch(ctx, cmds, rows)
	if errors.Is(err, ioncli.ErrNotReadOnly) {
		return fmt.Errorf("%w; override with --allow-writes or set allow_writes", err)
	}
	if err != nil {
		return err
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
		total, failed, err = tui.DrainLive(ctx, events, cmds, len(rows), sink, sel.stdin)
	} else {
		total, failed, err = drainPlain(events, len(rows), sink)
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
	if dead := rt.TokenDead(); dead != "" {
		fmt.Fprintf(os.Stderr, "%s — run `iontk auth login` and try again\n", dead)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d elements failed", failed, total)
	}
	return nil
}

func batchOutput(rt *runtime.Runtime) (format, orient, dir string, err error) {
	format, orient, dir = output.FormatJSON, output.OrientION, ""
	if cfg := rt.Config(); cfg != nil {
		format, orient, dir = cfg.BatchFormat, cfg.BatchOrient, cfg.BatchOutDir()
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
