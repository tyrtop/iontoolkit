package main

import (
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run commands across one or more elements, print JSON, exit",
	Long: `Logs into each element, runs the commands in order, and writes a JSON
array to stdout — one object per element, carrying element metadata and a
"commands" array of command/output pairs. Elements that failed carry an
"error" instead.

Identical to passing --cmd to iontk directly. Intended for read-only
commands, though a retry only re-dials: commands run once per element,
however many dials it took to get there.`,
	Example: `  iontk run --element 1234567890 --cmd "dump interface status 1"
  iontk run -f sites.txt -c "dump device status" -n 25
  cat ids.txt | iontk run -f - -c uptime`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBatch(cmd.Context())
	},
}
