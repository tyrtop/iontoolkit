package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func runBatch(ctx context.Context) error {
	if len(flagCommands) == 0 {
		return fmt.Errorf("--cmd is required for batch mode")
	}

	elements, err := gatherElements(flagElement, flagElementsPath)
	if err != nil {
		return err
	}

	f := baseFlags()
	f.Elements = elements
	f.Commands = splitCommands(flagCommands)

	if !flagAllowWrites {
		if err := ioncli.CheckReadOnly(f.Commands); err != nil {
			return fmt.Errorf("%w; override with --allow-writes", err)
		}
	}

	if err := f.Validate(); err != nil {
		return err
	}
	opts := f.options()
	if err := opts.Validate(); err != nil {
		return err
	}

	client, err := newClient(ctx, f, os.Stderr)
	if err != nil {
		return err
	}

	events := sase.RunStream(ctx, client, opts, f.Elements)

	var results []sase.Result
	var failed int

	if !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		results, failed, err = drainLive(ctx, events, f.Commands, len(f.Elements))
		if err != nil {
			return err
		}
	} else {
		results, failed = drainPlain(events, len(f.Elements))
	}

	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		return fmt.Errorf("encode results: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d elements failed", failed, len(results))
	}
	return nil
}

func drainPlain(events <-chan sase.Event, total int) ([]sase.Result, int) {
	results := make([]sase.Result, total)
	failed := 0
	for ev := range events {
		if !ev.Done {
			continue
		}
		results[ev.Index] = ev.Result
		if ev.Result.Error != "" {
			failed++
			fmt.Fprintln(os.Stderr, ev.Result.Error)
		}
	}
	return results, failed
}
