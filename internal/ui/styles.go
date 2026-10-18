package ui

import "charm.land/lipgloss/v2"

var (
	StyleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	StyleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	StyleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	StyleCmd     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	StyleElement = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	StyleDim     = lipgloss.NewStyle().Faint(true)
	// The nearest thing to orange in the 0-15 range, for a session about to go.
	StyleHot = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	MarkCheck = StyleOK.SetString("✓")
	MarkCross = StyleErr.SetString("✗")
)
