package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

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

	format, dest, err := batchOutput()
	if err != nil {
		return err
	}

	client, err := newClient(ctx, f, os.Stderr)
	if err != nil {
		return err
	}

	// The api package no longer prints refresh failures itself, so a batch run
	// has to say them out loud or a token dying mid-run looks like the devices
	// going quiet.
	go reportTokenErrors(client.TokenErrors(), os.Stderr)

	// The file is only created once the run is ready to produce something, so a
	// failure before this point leaves no empty report behind.
	out, where, err := openOutput(flagOut, dest, format, time.Now())
	if err != nil {
		return err
	}
	defer out.Close()
	if where != "stdout" {
		fmt.Fprintln(os.Stderr, "writing", format, "results to", where)
	}
	sink := newResultSink(out, format)

	events := sase.RunStream(ctx, opts, sase.Targets(client, f.Elements))

	var total int
	var failed int

	if !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		total, failed, err = drainLive(ctx, events, f.Commands, len(f.Elements), sink)
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

// batchOutput resolves the format and destination folder, with flags winning
// over the saved settings.
func batchOutput() (format, dir string, err error) {
	format, dir = formatJSON, ""
	if st := openStore(); st != nil {
		if cfg, cerr := st.Config(); cerr == nil {
			format, dir = cfg.BatchFormat, cfg.BatchOutDir()
		}
	}
	if flagFormat != "" {
		format = flagFormat
	}
	format, err = normalizeFormat(format)
	return format, dir, err
}

func drainPlain(events <-chan sase.Event, total int, sink resultSink) (int, int, error) {
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

// reportTokenErrors writes refresh failures as they happen. The channel closes
// when refreshing stops for good, which ends the loop.
func reportTokenErrors(ch <-chan error, w io.Writer) {
	for err := range ch {
		fmt.Fprintln(w, err)
	}
}
