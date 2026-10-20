package tui

import (
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

func (m tuiModel) withSpinner() (tuiModel, tea.Cmd) {
	m.spinner = ui.NewSpinner(ui.CurrentTheme().Spinner)
	return m, m.spinner.Tick
}
