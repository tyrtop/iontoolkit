package tui

import (
	tea "charm.land/bubbletea/v2"
)

// One notch moves three rows, which is what a terminal's own scrollback does.
const wheelRows = 3

// mouseMode is on only where something scrolls. Mouse reporting takes
// click-to-select away from the terminal, and a session pane has no scroll
// offset to move until scrollback lands.
func (m tuiModel) mouseMode() tea.MouseMode {
	switch m.phase {
	case phasePicking, phaseResults:
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

func (m tuiModel) onWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	up := msg.Button == tea.MouseWheelUp
	if !up && msg.Button != tea.MouseWheelDown {
		return m, nil
	}

	switch m.phase {
	case phasePicking:
		if up {
			m.table.MoveUp(wheelRows)
		} else {
			m.table.MoveDown(wheelRows)
		}

	case phaseResults:
		// An open detail pane is what the pointer is over, so the wheel scrolls
		// it instead of moving the row underneath.
		if m.resOpen {
			if up {
				m.resScroll = max(0, m.resScroll-wheelRows)
			} else {
				m.resScroll += wheelRows
			}
			return m, nil
		}
		if up {
			m.resTable.MoveUp(wheelRows)
		} else {
			m.resTable.MoveDown(wheelRows)
		}
	}
	return m, nil
}
