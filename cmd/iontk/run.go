package main

import (
	"github.com/spf13/cobra"
)

func newBatchRunCmd(use string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: "Run commands across one or more elements, print JSON, exit",
		Long: `Logs into each element, runs the commands in order, and writes a JSON
array to stdout — one object per element, carrying element metadata and a
"commands" array of command/output pairs. Elements that failed carry an
"error" instead.

--search picks the elements by a query instead of by ID, and asks before it
runs. Intended for read-only commands, though a retry only re-dials:
commands run once per element, however many dials it took to get there.`,
		Example: `  iontk batch run --element 1234567890 --cmd "dump interface status 1"
  iontk batch run -f sites.txt -c "dump device status" -n 25
  iontk batch run --search "s5024 ion 1200" -c uptime
  cat ids.txt | iontk batch run -f - -c uptime`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagSearch != "" {
				return runSearch(cmd.Context())
			}
			sel, err := flagSelection()
			if err != nil {
				return err
			}
			return runBatch(cmd.Context(), sel)
		},
	}
}

var runCmd = deprecated(newBatchRunCmd("run"), "iontk batch run")
