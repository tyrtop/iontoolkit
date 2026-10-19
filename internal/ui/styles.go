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
	// StyleRow is the highlight on the row a list is on, and on the line an
	// open result is scrolled to. Defined here rather than taken from the
	// table widget's default, which is a 256-colour pink a terminal theme
	// cannot remap.
	StyleRow = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	// StyleGhost is a line that is not there yet: the next command a macro
	// would take, or the macro that would be made, before anyone has typed
	// anything.
	StyleGhost = lipgloss.NewStyle().Faint(true).Italic(true)

	MarkCheck = StyleOK.SetString("✓")
	MarkCross = StyleErr.SetString("✗")
)
