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
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/tui"
)

// selection is what a batch run acts on. A --search resolves its own targets,
// each carrying the client and device login of the tenant it came from,
// because one search spans the whole working set; everything else hands over
// element IDs and lets one client serve them all.
type selection struct {
	elements []string
	targets  []sase.Target
	clients  []*api.Client
	// stdin says the IDs arrived down a pipe, which changes what the progress
	// display may do with the terminal.
	stdin bool
}

func (s selection) crossTenant() bool { return len(s.targets) > 0 }

// flagSelection is the ordinary --element / -f target list.
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

	var f app.Flags
	var err error
	if sel.crossTenant() {
		// Nothing left to resolve: every target already holds the credential
		// it will sign in with.
		f = app.BaseFlags()
	} else if f, err = resolveFlags(os.Stderr); err != nil {
		return err
	}
	f.Elements = sel.elements
	f.Commands = app.SplitCommands(flagCommands)

	if !app.Defaults.AllowWrites {
		if err := ioncli.CheckReadOnly(f.Commands); err != nil {
			return fmt.Errorf("%w; override with --allow-writes", err)
		}
	}

	if sel.crossTenant() {
		err = f.ValidateRun()
	} else {
		err = f.Validate()
	}
	if err != nil {
		return err
	}
	opts := f.Options()

	format, orient, dest, err := batchOutput()
	if err != nil {
		return err
	}

	targets, clients := sel.targets, sel.clients
	if !sel.crossTenant() {
		client, cerr := app.NewClient(ctx, app.OpenStore(), f, os.Stderr)
		if cerr != nil {
			return cerr
		}
		targets = sase.Targets(client, f.Elements)
		clients = []*api.Client{client}
	}
	if err := opts.ValidateFor(targets); err != nil {
		return err
	}

	// So a run that outlives its credential can stop, rather than spend an
	// element timeout each on everything left.
	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	// The api package no longer prints refresh failures itself, so a batch run
	// has to say them out loud or a token dying mid-run looks like the devices
	// going quiet.
	for _, c := range clients {
		go watchCredential(c.TokenErrors(), c.RefreshStopped, stopRun, os.Stderr)
	}

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
	if app.Fixture != nil {
		playback := app.Fixture.SessionClient(f.PagerOff, f.PagerPrompts)
		for i := range targets {
			targets[i].Client = playback
		}
	}

	events := sase.RunStream(ctx, opts, targets)

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
