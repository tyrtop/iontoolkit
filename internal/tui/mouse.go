package tui

import (
	tea "charm.land/bubbletea/v2"
)

// One notch moves three rows, which is what a terminal's own scrollback does.
const wheelRows = 3

// mouseMode is on only where something scrolls. Mouse reporting takes
// click-to-select away from the terminal, which is why the pages that scroll
// nothing leave it off.
func (m tuiModel) mouseMode() tea.MouseMode {
	switch m.phase {
	case phasePicking, phaseResults, phaseSession, phaseData:
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

	case phaseSession:
		s := m.sessions.Active()
		if s == nil {
			return m, nil
		}
		_, rows := m.paneSize()
		if up {
			m.sesScroll = min(m.sesScroll+wheelRows, s.MaxScroll(rows))
		} else {
			m.sesScroll = max(0, m.sesScroll-wheelRows)
		}

	// A data pane scrolls down the page, not back through history, so up
	// means up here where a session's wheel means the other way.
	case phaseData:
		tab, ok := m.activeData()
		if !ok {
			return m, nil
		}
		_, rows := m.dataPaneSize()
		if up {
			tab.scroll -= wheelRows
		} else {
			tab.scroll += wheelRows
		}
		tab.scroll = clampScroll(tab.scroll, len(m.dataPage(tab).lines), rows)
		m.data[m.dataActive] = tab
	}
	return m, nil
}
