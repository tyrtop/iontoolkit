package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
)

func newSessionOpenCmd(use string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   "Open an interactive CLI session to one element",
		Long:    "Attaches your terminal to the element's CLI over a websocket. Ctrl-] disconnects.",
		Example: `  iontk session open 1234567890`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eid := flagElement
			if len(args) == 1 {
				eid = args[0]
			}
			return runShell(cmd.Context(), eid)
		},
	}
}

var shellCmd = deprecated(newSessionOpenCmd("shell"), "iontk session open")

func runShell(ctx context.Context, eid string) error {
	if flagSearch != "" {
		return fmt.Errorf("a shell takes one element; run `iontk fleet search %q` to find its ID", flagSearch)
	}
	if eid == "" {
		return fmt.Errorf("an element ID is required: iontk session open <element-id>")
	}

	f, err := resolveFlags(os.Stderr)
	if err != nil {
		return err
	}
	f.Elements = []string{eid}
	f.Concurrency = 1

	if err := f.Validate(); err != nil {
		return err
	}

	client, err := app.NewClient(ctx, app.OpenStore(), f, os.Stderr)
	if err != nil {
		return err
	}

	go func() {
		for err := range client.TokenErrors() {
			fmt.Fprintln(os.Stderr, err)
		}
		if client.RefreshStopped() {
			fmt.Fprintln(os.Stderr, "sign-in expired — this session continues, but reconnecting needs `iontk auth login`")
		}
	}()

	return interactiveCLI(ctx, client, f.Verbose, eid)
}
