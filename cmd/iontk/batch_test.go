package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
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
