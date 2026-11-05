package main

import (
	"strings"
	"testing"
)

func TestAWorkflowPrintsJSONOnlyWhenAskedForIt(t *testing.T) {
	for _, tt := range []struct {
		name, command, format, out, want, err string
	}{
		{"no flags", "workflow.ion-bw-test", "", "", "", ""},
		{"--format json", "workflow.ion-bw-test", "json", "", "json", ""},
		{"--format jsonl", "workflow.ion-bw-test", "jsonl", "", "jsonl", ""},
		{"--out alone", "workflow.ion-bw-test", "", "run.json", "json", ""},
		{"--format text", "workflow.ion-bw-test", "text", "", "", ""},
		{"--format csv", "workflow.ion-bw-test", "csv", "", "", "json or jsonl, not csv"},
		{"not a workflow", "fleet.search", "json", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flagFormat, flagOut = tt.format, tt.out
			t.Cleanup(func() { flagFormat, flagOut = "", "" })
			got, err := runFormat(tt.command)
			if got != tt.want || (err == nil) != (tt.err == "") || (err != nil && !strings.Contains(err.Error(), tt.err)) {
				t.Errorf("runFormat(%q) = %q, %v; want %q and an error with %q", tt.command, got, err, tt.want, tt.err)
			}
		})
	}
}
