package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Reporting costs the terminal's own select-and-copy and buys only the wheel,
// which works anyway: the alt screen sends arrow keys for a notch.
func (m tuiModel) mouseMode() tea.MouseMode { return tea.MouseModeNone }
