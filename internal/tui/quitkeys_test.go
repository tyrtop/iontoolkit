package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func quits(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func pressKey(t *testing.T, m tuiModel, key tea.Key) (tuiModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg(key))
	return next.(tuiModel), cmd
}

var keyQ = tea.Key{Code: 'q', Text: "q"}
var keyEsc = tea.Key{Code: tea.KeyEsc}

func TestQClosesThePageLikeEsc(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) tuiModel
		want  tuiPhase
	}{
		{"element list", func(t *testing.T) tuiModel {
			m := pickModel(t)
			m.phase = phasePicking
			return m
		}, phaseMenu},
		{"results", func(t *testing.T) tuiModel { return resultsModel(t) }, phasePicking},
		{"mid-run", func(t *testing.T) tuiModel {
			m := resultsModel(t)
			m.phase = phaseRunning
			return m
		}, phaseResults},
		{"macros", func(t *testing.T) tuiModel {
			next, _ := pickModel(t).openMacros()
			return next.(tuiModel)
		}, phaseMenu},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, qcmd := pressKey(t, tt.setup(t), keyQ)
			if quits(t, qcmd) {
				t.Fatalf("q quit the app from %s, want it to close the page", tt.name)
			}
			if q.phase != tt.want {
				t.Errorf("q left phase %v, want %v", q.phase, tt.want)
			}

			esc, _ := pressKey(t, tt.setup(t), keyEsc)
			if esc.phase != q.phase {
				t.Errorf("q went to %v and esc to %v, want the same page", q.phase, esc.phase)
			}
		})
	}
}

func TestQQuitsFromTheMainMenu(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseMenu

	if _, cmd := pressKey(t, m, keyQ); !quits(t, cmd) {
		t.Error("q on the main menu did not quit")
	}
}

func TestQClosesWhatIsOpenBeforeLeavingTheResults(t *testing.T) {
	m := resultsModel(t)

	open, _ := pressKey(t, m, tea.Key{Code: tea.KeyEnter})
	if !open.resOpen {
		t.Fatal("enter did not open a result")
	}
	closed, cmd := pressKey(t, open, keyQ)
	if quits(t, cmd) {
		t.Fatal("q quit with a result open")
	}
	if closed.resOpen || closed.phase != phaseResults {
		t.Errorf("q with a detail open: resOpen=%v phase=%v, want the detail closed and the page kept",
			closed.resOpen, closed.phase)
	}

	filtered := m
	filtered.resFilter.SetValue("0100")
	filtered.applyResultFilter()
	cleared, _ := pressKey(t, filtered, keyQ)
	if cleared.resFilter.Value() != "" {
		t.Errorf("filter = %q after q, want it cleared", cleared.resFilter.Value())
	}
	if cleared.phase != phaseResults {
		t.Errorf("q cleared the filter and left the page as well (phase %v)", cleared.phase)
	}
}

func TestQIsALetterWhileFiltering(t *testing.T) {
	t.Run("element list", func(t *testing.T) {
		m := pickModel(t)
		m.phase = phasePicking
		m, _ = pressKey(t, m, tea.Key{Code: '/', Text: "/"})
		if !m.filterOn {
			t.Fatal("/ did not open the filter")
		}
		m, cmd := pressKey(t, m, keyQ)
		if quits(t, cmd) {
			t.Fatal("q quit while the filter was being typed")
		}
		if got := m.filter.Value(); got != "q" {
			t.Errorf("filter = %q, want the q typed into it", got)
		}
		if m.phase != phasePicking {
			t.Errorf("phase = %v, want to stay on the list", m.phase)
		}
	})

	t.Run("results", func(t *testing.T) {
		m := resultsModel(t)
		m, _ = pressKey(t, m, tea.Key{Code: '/', Text: "/"})
		if !m.resFilterOn {
			t.Fatal("/ did not open the filter")
		}
		m, cmd := pressKey(t, m, keyQ)
		if quits(t, cmd) {
			t.Fatal("q quit while the filter was being typed")
		}
		if got := m.resFilter.Value(); got != "q" {
			t.Errorf("filter = %q, want the q typed into it", got)
		}
	})
}
