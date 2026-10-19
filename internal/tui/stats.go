package tui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

// statsRows is how much room the overlay takes when it is on: a rule and a
// line of numbers. Fixed, so a number growing a digit never moves the pane.
const statsRows = 2

// The numbers move on their own, so the overlay has to repaint without a
// keypress. Once a second, and only while it is on.
type statsTickMsg time.Time

func statsTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return statsTickMsg(t) })
}

func (m tuiModel) statsRows() int {
	if !m.stats {
		return 0
	}
	return statsRows
}

// toggleStats turns the overlay on or off, starting the repaint only when there
// is not already one running.
func (m tuiModel) toggleStats() (tea.Model, tea.Cmd) {
	m.stats = !m.stats
	// Both tables are sized where their rows are built, not per render, so the
	// pane the overlay takes its lines from has to be resized here.
	m.rebuildRows()
	m.rebuildResultRows()
	if !m.stats || m.statsTicking {
		return m, nil
	}
	m.statsTicking = true
	return m, statsTick()
}

// statsPane is exactly statsRows lines, with no trailing newline.
func (m tuiModel) statsPane() string {
	if !m.stats {
		return ""
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	sessions := fmt.Sprintf("sessions %d", m.sessions.Count())
	if n := m.sessions.DetachedCount(); n > 0 {
		sessions += fmt.Sprintf(" +%d detached", n)
	}

	parts := []string{
		sessions,
		fmt.Sprintf("elements %d/%d", len(m.matches), len(m.all)),
	}
	// A static SCM_TOKEN has no lifetime to report, so this row comes and goes
	// with the credential rather than with the overlay.
	if c := m.pool.get(m.tenant.TSGID); c != nil {
		if left, ok := c.TokenLeft(); ok {
			parts = append(parts, "token "+ui.Age(left))
		}
	}
	parts = append(parts,
		fmt.Sprintf("goroutines %d", runtime.NumGoroutine()),
		"heap "+mib(ms.HeapAlloc),
		fmt.Sprintf("%dx%d", m.width, m.height),
	)

	w := max(10, m.width)
	rule := ui.StyleDim.Render(strings.Repeat("─", w))
	line := ui.StyleDim.Render(strings.Join(parts, " · "))
	return rule + "\n" + ui.Truncate(line, w)
}

func mib(b uint64) string {
	return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
}
