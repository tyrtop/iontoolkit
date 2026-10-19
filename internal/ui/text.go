package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// LabelWidth is the gutter every key is padded into. A longer key overruns it
// and collides with its value.
const LabelWidth = 13

func Label(s string) string { return StyleDim.Render(fmt.Sprintf("%-*s", LabelWidth, s)) }

// Width is the printed width of a string, ignoring its escape codes.
func Width(s string) int { return ansi.StringWidth(s) }

func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}

// Age formats a cache age coarsely. Sub-second precision is noise for something
// measured in minutes, and "38m38.9s ago" reads like a stopwatch.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h, mins := int(d.Hours()), int(d.Minutes())%60
	if mins == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, mins)
}
