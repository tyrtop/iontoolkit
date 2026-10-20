package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const ModalWidth = 48

func Modal(lines []string, width int) string {
	inner := min(ModalWidth, max(20, width-6))

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
		if pad := inner - ansi.StringWidth(line); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderColor()).
		Padding(0, 1).
		Render(b.String())

	out := make([]string, 0, len(lines)+2)
	for _, line := range strings.Split(box, "\n") {
		out = append(out, "  "+line)
	}
	return strings.Join(out, "\n")
}

func ModalHeight(n int) int { return n + 2 }
