package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReportTokenErrorsWritesEachFailureAndStopsWhenClosed(t *testing.T) {
	ch := make(chan error, 2)
	ch <- errors.New("token refresh: first")
	ch <- errors.New("token refresh: second")
	close(ch)

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { reportTokenErrors(ch, &buf); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reportTokenErrors did not return after the channel closed")
	}

	out := buf.String()
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}
