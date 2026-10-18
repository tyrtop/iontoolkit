package ui

import (
	"regexp"
	"testing"
	"time"
)

var testANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return testANSI.ReplaceAllString(s, "") }

func TestAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{900 * time.Millisecond, "0s"},
		{45 * time.Second, "45s"},
		{59*time.Second + 999*time.Millisecond, "59s"},
		{time.Minute, "1m"},
		{38*time.Minute + 39*time.Second, "38m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{2 * time.Hour, "2h"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "25h"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := Age(tt.d); got != tt.want {
				t.Errorf("Age(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestTruncateIsAnsiAndWidthAware(t *testing.T) {
	styled := StyleElement.Render("readonly") + StyleDim.Render("  (default)") + StyleOK.Render("  saved")

	got := Truncate(styled, 40)
	if plain(got) != plain(styled) {
		t.Errorf("a styled string that fits was altered:\n got %q\nwant %q", plain(got), plain(styled))
	}

	cut := Truncate(styled, 10)
	if w := len([]rune(plain(cut))); w > 10 {
		t.Errorf("truncated visible width = %d, want at most 10", w)
	}

	if got := Truncate("ünïcødé-name", 6); len([]rune(plain(got))) > 6 {
		t.Errorf("multi-byte truncate gave %q", got)
	}
	if got := Truncate("short", 0); got != "" {
		t.Errorf("zero width gave %q", got)
	}
}
