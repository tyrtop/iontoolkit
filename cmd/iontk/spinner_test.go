package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

func TestALinePrintedUnderTheSpinnerStartsOnACleanLine(t *testing.T) {
	start := time.Now()
	waits := []runtime.Wait{{What: "S5037-RTR-01: debug bw-test src-interface 3", Since: start, Until: start.Add(5 * time.Minute)}}
	var screen bytes.Buffer
	s := newSpinner(&screen, func() []runtime.Wait { return waits }, []string{"*"}, 120, "workflow ion-bw-test")
	s.start = start

	s.draw(0, start.Add(65*time.Second))
	if got := ui.Plain(screen.String()); !strings.Contains(got, "* S5037-RTR-01: debug bw-test src-interface 3 · 1m5s, up to 5m") {
		t.Errorf("the spinner drew %q", got)
	}
	s.println(&screen, "downlink 149.48 Mbps")
	if !strings.HasSuffix(screen.String(), "\r\x1b[Kdownlink 149.48 Mbps\n") {
		t.Errorf("the line went out as %q, want the spinner wiped off it first", screen.String())
	}

	waits = nil
	s.draw(1, start.Add(2*time.Second))
	if got := ui.Plain(screen.String()); !strings.HasSuffix(got, "* workflow ion-bw-test · 2s") {
		t.Errorf("with nothing to wait on, the spinner drew %q", got)
	}
}
