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
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/tui"
)

func runBatch(ctx context.Context) error {
	if len(flagCommands) == 0 {
		return fmt.Errorf("--cmd is required for batch mode")
	}

	elements, err := gatherElements(flagElement, flagElementsPath)
	if err != nil {
		return err
	}

	f, err := resolveFlags(os.Stderr)
	if err != nil {
		return err
	}
	f.Elements = elements
	f.Commands = app.SplitCommands(flagCommands)

	if !app.Defaults.AllowWrites {
		if err := ioncli.CheckReadOnly(f.Commands); err != nil {
			return fmt.Errorf("%w; override with --allow-writes", err)
		}
	}

	if err := f.Validate(); err != nil {
		return err
	}
	opts := f.Options()
	if err := opts.Validate(); err != nil {
		return err
	}

	format, orient, dest, err := batchOutput()
	if err != nil {
		return err
	}

	client, err := app.NewClient(ctx, f, os.Stderr)
	if err != nil {
		return err
	}

	// So a run that outlives its credential can stop, rather than spend an
	// element timeout each on everything left.
	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	// The api package no longer prints refresh failures itself, so a batch run
	// has to say them out loud or a token dying mid-run looks like the devices
	// going quiet.
	go watchCredential(client.TokenErrors(), client.RefreshStopped, stopRun, os.Stderr)

	// The file is only created once the run is ready to produce something, so a
	// failure before this point leaves no empty report behind.
	out, where, err := output.Open(flagOut, dest, format, time.Now())
	if err != nil {
		return err
	}
	defer out.Close()
	if where != "stdout" {
		fmt.Fprintln(os.Stderr, "writing", format, "results to", where)
	}
	sinkOpts := []output.Option{output.WithOrientation(orient)}
	// A table only has to fit when it is being read on the way past. A file
	// holds the only copy of what it is given, so nothing is trimmed into one.
	if where == "stdout" {
		if cols, _, serr := term.GetSize(int(os.Stdout.Fd())); serr == nil && cols > 0 {
			sinkOpts = append(sinkOpts, output.WithWidth(cols))
		}
	}
	sink := output.NewSink(out, format, sinkOpts...)

	// Fixture mode swaps the client only here: the lookup and the session come
	// from the playback device, and everything either side of them is the real
	// run, including ioncli's autologin and pager handling.
	var runClient sase.Client = client
	if app.Fixture != nil {
		runClient = app.Fixture.SessionClient(f.PagerOff, f.PagerPrompts)
	}

	events := sase.RunStream(ctx, opts, sase.Targets(runClient, f.Elements))

	var total int
	var failed int

	if !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd())) {
		total, failed, err = tui.DrainLive(ctx, events, f.Commands, len(f.Elements), sink, flagElementsPath == "-")
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

// batchOutput resolves the format, row orientation and destination folder,
// with flags winning over the saved settings.
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

// dead separates a spent credential from a run that merely finished; both
// close the channel.
func watchCredential(ch <-chan error, dead func() bool, stop func(), w io.Writer) {
	for err := range ch {
		fmt.Fprintln(w, err)
	}
	if dead() {
		fmt.Fprintln(w, "sign-in expired and could not be renewed — stopping; run `iontk login` and try again")
		stop()
	}
}
