package tui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

const statsRows = 2

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

func (m tuiModel) toggleStats() (tea.Model, tea.Cmd) {
	m.stats = !m.stats
	m.rebuildRows()
	m.rebuildResultRows()
	if !m.stats || m.statsTicking {
		return m, nil
	}
	m.statsTicking = true
	return m, statsTick()
}

func (m tuiModel) statsPane() string {
	if !m.stats {
		return ""
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	sessions := fmt.Sprintf("sessions %d", m.sessions().Count())
	if n := m.sessions().DetachedCount(); n > 0 {
		sessions += fmt.Sprintf(" +%d detached", n)
	}

	parts := []string{
		sessions,
		fmt.Sprintf("elements %d/%d", len(m.matched()), len(m.rows())),
	}
	if c := m.pool().Get(m.tenant().TSGID); c != nil {
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
