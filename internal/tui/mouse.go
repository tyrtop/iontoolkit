package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m tuiModel) mouseMode() tea.MouseMode {
	switch m.phase {
	case phaseSession:
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

const wheelStep = 3

func (m tuiModel) onWheel(up bool) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseSession:
		s := m.sessions.Active()
		if s == nil {
			return m, nil
		}
		key := "alt+down"
		if up {
			key = "alt+up"
		}
		next := m
		for range wheelStep {
			next, _ = next.onScrollKey(s, key)
		}
		return next, nil
	}
	return m, nil
}
