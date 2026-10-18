package ui

import "strings"

func Footer(status, hint string) string {
	line := ""
	if status != "" {
		line = StyleWarn.Render(status)
	}
	return line + "\n" + StyleDim.Render(hint)
}

func ClipFrame(s string, height int) string {
	if height <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}
