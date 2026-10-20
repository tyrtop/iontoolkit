package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func lineCount(s string) int { return len(strings.Split(strings.TrimRight(s, "\n"), "\n")) }

func TestStatsOverlayKeepsTheFrameHeight(t *testing.T) {
	cases := map[string]func(t *testing.T) (tuiModel, func(tuiModel) string){
		"element list": func(t *testing.T) (tuiModel, func(tuiModel) string) {
			m := pickModel(t)
			m.width, m.height = 100, 30
			m.rebuildRows()
			return m, func(m tuiModel) string { return m.viewPicking() }
		},
		"results": func(t *testing.T) (tuiModel, func(tuiModel) string) {
			return resultsModel(t), func(m tuiModel) string { return m.viewResults() }
		},
		"open result": func(t *testing.T) (tuiModel, func(tuiModel) string) {
			m := resultsModel(t)
			m.resOpen = true
			return m, func(m tuiModel) string { return m.viewResults() }
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			m, view := build(t)
			off := lineCount(view(m))
			on, _ := m.toggleStats()
			if got := lineCount(view(on.(tuiModel))); got != off {
				t.Errorf("%d lines with the overlay, %d without", got, off)
			}
		})
	}
}

func TestStatsOverlayIsExactlyItsReservedRows(t *testing.T) {
	m := pickModel(t)
	m.width, m.height = 100, 30
	on, _ := m.toggleStats()
	got := on.(tuiModel)

	if got.statsRows() != statsRows {
		t.Fatalf("statsRows() = %d with the overlay on, want %d", got.statsRows(), statsRows)
	}
	if n := lineCount(got.statsPane()); n != statsRows {
		t.Errorf("the pane drew %d lines, want %d — a growing number must not move the frame", n, statsRows)
	}
}

func TestStatsOverlayReportsWhatIsMoving(t *testing.T) {
	m := pickModel(t)
	m.width, m.height = 120, 30
	on, _ := m.toggleStats()

	pane := plain(on.(tuiModel).statsPane())
	for _, want := range []string{"sessions", "elements", "goroutines", "heap", "120x30"} {
		if !strings.Contains(pane, want) {
			t.Errorf("pane is missing %q:\n%s", want, pane)
		}
	}
}

func TestAltITogglesStatsEverywhere(t *testing.T) {
	for _, phase := range []tuiPhase{phasePicking, phaseResults, phaseSession, phaseMenu} {
		m := pickModel(t)
		m.phase = phase

		on, _ := m.onKey(tea.KeyPressMsg(tea.Key{Code: 'i', Mod: tea.ModAlt}))
		if !on.(tuiModel).stats {
			t.Errorf("phase %d: alt+i did not turn the overlay on", phase)
			continue
		}
		off, _ := on.(tuiModel).onKey(tea.KeyPressMsg(tea.Key{Code: 'i', Mod: tea.ModAlt}))
		if off.(tuiModel).stats {
			t.Errorf("phase %d: alt+i did not turn the overlay back off", phase)
		}
	}
}

func TestStatsRepaintStopsWithTheOverlay(t *testing.T) {
	m := pickModel(t)

	on, cmd := m.toggleStats()
	if cmd == nil {
		t.Fatal("turning the overlay on started no repaint, so the numbers would freeze")
	}
	again, cmd := on.(tuiModel).toggleStats()
	if cmd != nil {
		t.Error("toggling off scheduled another repaint")
	}
	third, cmd := again.(tuiModel).toggleStats()
	if cmd != nil {
		t.Error("toggling back on started a second repaint while the first was still running")
	}

	off, _ := third.(tuiModel).toggleStats()
	done, cmd := off.(tuiModel).Update(statsTickMsg(time.Time{}))
	if cmd != nil {
		t.Error("the tick rescheduled itself with the overlay off")
	}
	if done.(tuiModel).statsTicking {
		t.Error("statsTicking stayed set, so the overlay can never repaint again")
	}
}
