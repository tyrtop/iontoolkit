package ui

import (
	"fmt"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const LabelWidth = 14

func Label(s string) string { return StyleDim.Render(fmt.Sprintf("%-*s", LabelWidth, s)) }

func Width(s string) int { return ansi.StringWidth(s) }

func Plain(s string) string { return ansi.Strip(s) }

func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}

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

func Title(s string) string {
	out := []rune(s)
	up := true
	for i, r := range out {
		switch {
		case r == ' ' || r == '-':
			up = true
		case up:
			out[i] = unicode.ToUpper(r)
			up = false
		}
	}
	return string(out)
}
