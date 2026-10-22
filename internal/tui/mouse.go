package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m tuiModel) mouseMode() tea.MouseMode {
	switch m.phase {
	case phaseSession:
		if m.mouseOff {
			return tea.MouseModeNone
		}
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

func (m tuiModel) toggleMouse() (tea.Model, tea.Cmd) {
	m.mouseOff = !m.mouseOff
	if m.mouseOff {
		m.status = "the mouse is your terminal's — drag to select, alt+v takes it back for the wheel"
	} else {
		m.status = "the mouse is iontk's — the wheel scrolls, alt+v hands it back to select"
	}
	return m, nil
}

func (m tuiModel) mouseHint() string {
	if m.mouseOff {
		return "alt+v wheel"
	}
	return "alt+v select"
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
