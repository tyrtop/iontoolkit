package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
)

var shellCmd = &cobra.Command{
	Use:     "shell",
	Short:   "Open an interactive CLI session to one element",
	Long:    "Attaches your terminal to the element's CLI over a websocket. Ctrl-] disconnects.",
	Example: `  iontk shell --element 1234567890`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runShell(cmd.Context(), flagElement)
	},
}

func runShell(ctx context.Context, eid string) error {
	if eid == "" {
		return fmt.Errorf("--element is required")
	}

	f := app.BaseFlags()
	f.Elements = []string{eid}
	f.Concurrency = 1

	if err := f.Validate(); err != nil {
		return err
	}

	client, err := app.NewClient(ctx, f, os.Stderr)
	if err != nil {
		return err
	}
	return interactiveCLI(ctx, client, f.Verbose, eid)
}
