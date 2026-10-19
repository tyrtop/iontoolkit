package tui

import (
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

// withSpinner rebuilds the spinner from the theme, which owns its shape as
// well as its colour.
func (m tuiModel) withSpinner() (tuiModel, tea.Cmd) {
	m.spinner = ui.NewSpinner(ui.CurrentTheme().Spinner)
	return m, m.spinner.Tick
}
