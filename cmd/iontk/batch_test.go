package main

import (
	"testing"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
)

func TestBatchOutputPrefersFlagsOverSettings(t *testing.T) {
	app.Debug = true
	t.Cleanup(func() {
		app.Debug = false
		flagFormat, flagOrient = "", ""
	})

	st := app.OpenStore()
	if st == nil {
		t.Fatal("OpenStore() = nil, want the debug store")
	}
	if err := st.SaveConfig(&config.Config{
		BatchFormat: output.FormatCSV,
		BatchOrient: output.OrientCommand,
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	theRuntime = nil
	rt := cliRuntime()
	format, orient, _, err := batchOutput(rt)
	if err != nil {
		t.Fatalf("batchOutput() error = %v", err)
	}
	if format != output.FormatCSV || orient != output.OrientCommand {
		t.Errorf("batchOutput() = %q/%q, want the saved settings", format, orient)
	}

	flagFormat, flagOrient = "table", "ion"
	format, orient, _, err = batchOutput(rt)
	if err != nil {
		t.Fatalf("batchOutput() error = %v", err)
	}
	if format != output.FormatTable || orient != output.OrientION {
		t.Errorf("batchOutput() = %q/%q, want the flags to win", format, orient)
	}

	flagOrient = "sideways"
	if _, _, _, err := batchOutput(rt); err == nil {
		t.Error("batchOutput() error = nil, want an unknown orientation to fail")
	}
}
