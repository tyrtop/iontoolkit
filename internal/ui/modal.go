package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ModalWidth is the inside width of a modal, wide enough for a failure reason
// without crowding a narrow terminal.
const ModalWidth = 48

// Modal frames a few lines in a border. Lines are padded before styling and
// measured with ansi.StringWidth, since a styled string's escape bytes would
// otherwise count towards the width and pull the right edge out of line.
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
		BorderForeground(lipgloss.Color("3")).
		Padding(0, 1).
		Render(b.String())

	// Indented rather than centred: a box that moves with the terminal width is
	// harder to find again than one that is always in the same place.
	out := make([]string, 0, len(lines)+2)
	for _, line := range strings.Split(box, "\n") {
		out = append(out, "  "+line)
	}
	return strings.Join(out, "\n")
}

// ModalHeight is how many rows Modal will take for n lines, so a caller can
// keep the frame height constant.
func ModalHeight(n int) int { return n + 2 }
