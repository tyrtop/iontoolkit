package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
)

func TestWatchCredentialWritesEachFailureAndStopsWhenClosed(t *testing.T) {
	ch := make(chan error, 2)
	ch <- errors.New("token refresh: first")
	ch <- errors.New("token refresh: second")
	close(ch)

	var buf bytes.Buffer
	done := make(chan struct{})
	stopped := false
	go func() {
		watchCredential(ch, func() bool { return false }, func() { stopped = true }, &buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchCredential did not return after the channel closed")
	}

	out := buf.String()
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if stopped {
		t.Error("the run was stopped although the credential could still be renewed")
	}
}

func TestWatchCredentialStopsTheRunWhenTheCredentialIsSpent(t *testing.T) {
	ch := make(chan error, 1)
	ch <- errors.New("token refresh: interactive sign-in required")
	close(ch)

	var buf bytes.Buffer
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		watchCredential(ch, func() bool { return true }, func() { close(stopped) }, &buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchCredential did not return")
	}

	select {
	case <-stopped:
	default:
		t.Error("the run was not stopped although the credential cannot be renewed")
	}
	if out := buf.String(); !strings.Contains(out, "sign-in expired") {
		t.Errorf("output does not say why the run stopped:\n%s", out)
	}
}

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

	format, orient, _, err := batchOutput()
	if err != nil {
		t.Fatalf("batchOutput() error = %v", err)
	}
	if format != output.FormatCSV || orient != output.OrientCommand {
		t.Errorf("batchOutput() = %q/%q, want the saved settings", format, orient)
	}

	flagFormat, flagOrient = "table", "ion"
	format, orient, _, err = batchOutput()
	if err != nil {
		t.Fatalf("batchOutput() error = %v", err)
	}
	if format != output.FormatTable || orient != output.OrientION {
		t.Errorf("batchOutput() = %q/%q, want the flags to win", format, orient)
	}

	flagOrient = "sideways"
	if _, _, _, err := batchOutput(); err == nil {
		t.Error("batchOutput() error = nil, want an unknown orientation to fail")
	}
}
