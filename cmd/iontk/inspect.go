package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var flagInspectRaw bool

var inspectCmd = deprecated(&cobra.Command{
	Use:   "inspect [element-id]",
	Short: "Print what the portal knows about one element",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eid := flagElement
		if len(args) == 1 {
			eid = args[0]
		}
		return runInspect(cmd.Context(), eid)
	},
}, "iontk element inspect --id")

func runInspect(ctx context.Context, eid string) error {
	if eid == "" {
		return fmt.Errorf("an element ID is required: iontk element inspect --id <element-id>")
	}
	args := map[string]string{"id": eid}
	if flagInspectRaw {
		args["raw"] = "yes"
	}
	return execPrinting(ctx, os.Stdout, "element.inspect", args, nil, false)
}
