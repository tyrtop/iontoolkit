package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func wheel(b tea.MouseButton) tea.Msg { return tea.MouseWheelMsg{Button: b} }

func TestWheelMovesTheElementCursor(t *testing.T) {
	m := pickModel(t)
	els := make([]api.Element, 0, 20)
	for i := range 20 {
		els = append(els, api.Element{ID: fmt.Sprint(i), Name: fmt.Sprintf("s0100-rtr-%02d", i)})
	}
	setTestElements(&m, els)

	next, _ := m.Update(wheel(tea.MouseWheelDown))
	down := next.(tuiModel)
	if down.table.Cursor() != wheelRows {
		t.Fatalf("cursor = %d after one notch down, want %d", down.table.Cursor(), wheelRows)
	}

	next, _ = down.Update(wheel(tea.MouseWheelUp))
	if c := next.(tuiModel).table.Cursor(); c != 0 {
		t.Errorf("cursor = %d after scrolling back up, want 0", c)
	}
}

func wheelResultsModel(t *testing.T) tuiModel {
	t.Helper()
	m := pickModel(t)
	m.width, m.height = 100, 30

	rs := make([]sase.Result, 0, 6)
	for i := range 6 {
		rs = append(rs, sase.Result{
			ElementID: fmt.Sprint(i),
			Name:      fmt.Sprintf("S0100-RTR-%02d", i),
			Model:     "ion 2000",
			Commands:  []ioncli.CommandOutput{{Command: "dump device status", Output: "up 5 days"}},
		})
	}
	return m.seedResultsForTest(rs, nil)
}

func TestWheelScrollsAnOpenResultInsteadOfTheRow(t *testing.T) {
	m := wheelResultsModel(t)
	m.resOpen = true

	next, _ := m.Update(wheel(tea.MouseWheelDown))
	got := next.(tuiModel)
	if got.resScroll != wheelRows {
		t.Errorf("resScroll = %d, want %d — the wheel must scroll the open pane", got.resScroll, wheelRows)
	}
	if got.resTable.Cursor() != 0 {
		t.Errorf("row cursor moved to %d; the open pane is what the pointer was over", got.resTable.Cursor())
	}

	// At the top it stops rather than going negative, which would render the
	// pane from before its first line.
	up, _ := m.Update(wheel(tea.MouseWheelUp))
	if c := up.(tuiModel).resScroll; c != 0 {
		t.Errorf("resScroll = %d at the top of the pane, want 0", c)
	}
}

func TestWheelMovesTheResultsRowWhenNothingIsOpen(t *testing.T) {
	m := wheelResultsModel(t)

	next, _ := m.Update(wheel(tea.MouseWheelDown))
	if c := next.(tuiModel).resTable.Cursor(); c != wheelRows {
		t.Errorf("results cursor = %d, want %d", c, wheelRows)
	}
}

func TestMouseReportingIsOffWhereNothingScrolls(t *testing.T) {
	cases := map[tuiPhase]tea.MouseMode{
		phasePicking: tea.MouseModeCellMotion,
		phaseResults: tea.MouseModeCellMotion,
		// Reporting here would cost the pane click-to-select and buy nothing
		// until scrollback lands.
		phaseSession: tea.MouseModeNone,
		phaseMenu:    tea.MouseModeNone,
	}
	for phase, want := range cases {
		m := tuiModel{phase: phase}
		if got := m.mouseMode(); got != want {
			t.Errorf("phase %d mouseMode() = %v, want %v", phase, got, want)
		}
	}
}
