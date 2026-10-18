package tui

import (
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

func (m tuiModel) withSpinner(name string) (tuiModel, tea.Cmd) {
	m.spinnerName = ui.SpinnerName(name)
	m.spinner = ui.NewSpinner(m.spinnerName)
	return m, m.spinner.Tick
}
