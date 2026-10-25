package main

import "testing"

func TestARepeatableFlagIsReadOnceThoughTheFlagsAreReadTwice(t *testing.T) {
	t.Cleanup(func() { flagCommands = nil })
	argv := []string{"batch", "run", "--cmd", "dump device status", "-c", "dump spoke-ha status"}
	if err := prepare(argv); err != nil {
		t.Fatal(err)
	}
	if err := rootCmd.PersistentFlags().Parse(argv); err != nil {
		t.Fatal(err)
	}
	if len(flagCommands) != 2 || flagCommands[0] != "dump device status" || flagCommands[1] != "dump spoke-ha status" {
		t.Errorf("--cmd = %q, want each command once", flagCommands)
	}
}
