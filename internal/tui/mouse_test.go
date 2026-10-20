package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAWheelNotchScrollsTheSessionPane(t *testing.T) {
	m, _ := scrolledPane(t)
	m.phase = phaseSession
	if m.sesScroll != 0 {
		t.Fatalf("the pane starts held at %d", m.sesScroll)
	}

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = next.(tuiModel)
	if m.sesScroll == 0 {
		t.Fatal("a notch up did not scroll the session pane")
	}
	held := m.sesScroll

	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(tuiModel)
	if m.sesScroll >= held {
		t.Errorf("a notch down left the offset at %d, want below %d", m.sesScroll, held)
	}

	for range 20 {
		next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		m = next.(tuiModel)
	}
	if m.sesScroll != 0 {
		t.Errorf("the pane is held at %d after winding down, want the live bottom", m.sesScroll)
	}
}
