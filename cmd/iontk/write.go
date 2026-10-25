package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
)

var (
	flagWriteSet   []string
	flagWriteMatch []string
	flagWriteDoit  bool
	flagWrites     string
)

var writeCmd = &cobra.Command{
	Use:   "write <what> --set field=value --match record",
	Short: "Change records in the portal across a list of elements",
	Long: `Reads each element's records of one kind, picks the ones --match names,
and changes the fields --set gives. What can be changed, and where, comes from
writes.yaml in the config directory on top of the ones iontk ships.

Every run is a dry run unless --doit is given: each change is printed as
old → new and nothing is sent. --doit sends them, when the write is marked
verified.
Every request sent is logged to writes.log in the config directory.

--match takes the record's match field ("1" is the interface named 1), any
field=value ("used_for=lan"), or "*" for every record; repeat it to pick more.`,
	Example: `  iontk write interface -f stores.ids --match 1 --set description="uplink to core"
  iontk write interface -e 1234567890 --match used_for=lan --set mtu=1500 --doit`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWrite(cmd.Context(), args[0], os.Stdout)
	},
}

func init() {
	f := writeCmd.Flags()
	f.StringArrayVar(&flagWriteSet, "set", nil, "field=value to change; repeatable")
	f.StringArrayVar(&flagWriteMatch, "match", nil, `which records to change: a name, field=value, or "*"; repeatable`)
	f.BoolVar(&flagWriteDoit, "doit", false, "send the changes; without it the run is a dry run that only shows them")
	f.StringVar(&flagWrites, "writes", "", "YAML file of writes, instead of writes.yaml in the config directory")
}

func runWrite(ctx context.Context, what string, out io.Writer) error {
	if len(flagWriteMatch) == 0 {
		return fmt.Errorf(`--match is required: name the records to change, or "*" for all of them`)
	}
	if len(flagWriteSet) == 0 {
		return fmt.Errorf("--set is required: field=value, repeatable")
	}
	els, err := gatherElements(flagElement, flagElementsPath)
	if err != nil {
		return err
	}
	if len(els) == 0 {
		return fmt.Errorf("no elements: give --element or --elements-path")
	}
	if flagWrites != "" {
		app.WritesFile = flagWrites
		app.ReloadWrites(cliRuntime())
	}
	args, rest := writeCall(what, els, flagWriteMatch, flagWriteSet)
	return execPrinting(ctx, out, "write", args, rest, flagWriteDoit)
}

func writeCall(what string, els, match, sets []string) (map[string]string, []string) {
	return map[string]string{
		"what":     what,
		"elements": strings.Join(els, ","),
		"match":    strings.Join(match, ";"),
	}, sets
}
