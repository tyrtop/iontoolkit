package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
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

func TestAltVHandsTheMouseToTheTerminalAndBack(t *testing.T) {
	m, conn := favModel(t)
	m.phase = phaseSession
	m.width, m.termHeight = 160, 30
	m.height = m.usableHeight()

	if got := m.mouseMode(); got != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode = %v on a fresh session, want iontk to have the wheel", got)
	}

	m, _ = pressSessionKey(t, m, altKey('v'))
	if got := m.mouseMode(); got != tea.MouseModeNone {
		t.Errorf("mouse mode = %v after alt+v, want the terminal to have the mouse back", got)
	}
	if !strings.Contains(m.status, "terminal") {
		t.Errorf("status = %q, want it to say who has the mouse", m.status)
	}
	if got := plain(m.View().Content); !strings.Contains(got, "alt+v wheel") {
		t.Errorf("the footer does not offer the wheel back:\n%s", got)
	}
	if strings.Contains(conn.sent(), "\x1bv") {
		t.Errorf("alt+v reached the device: %q", conn.sent())
	}

	m, _ = pressSessionKey(t, m, altKey('v'))
	if got := m.mouseMode(); got != tea.MouseModeCellMotion {
		t.Errorf("mouse mode = %v after a second alt+v, want the wheel back", got)
	}
	if got := plain(m.View().Content); !strings.Contains(got, "alt+v select") {
		t.Errorf("the footer does not offer selecting:\n%s", got)
	}
}

func TestTheMouseCanBeHandedBackOnADeadTab(t *testing.T) {
	m, conn := favModel(t)
	m.phase = phaseSession
	conn.Close(websocket.StatusAbnormalClosure, "torn")

	s := m.sessions().Active()
	waitForTUI(t, func() bool { return s.Err() != nil || s.Ended() })

	m, _ = pressSessionKey(t, m, altKey('v'))
	if got := m.mouseMode(); got != tea.MouseModeNone {
		t.Errorf("mouse mode = %v, want a dead tab to give the mouse up too", got)
	}
}
