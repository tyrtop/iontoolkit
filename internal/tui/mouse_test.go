package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func shellPane(t *testing.T) tuiModel {
	t.Helper()
	m, set := scrolledPane(t)
	m = m.onSessionOpened(set.Tenants[0].Elements[0].ID)
	m.phase = phaseSession
	return m
}

func rowOf(t *testing.T, m tuiModel, text string) int {
	t.Helper()
	for i, ln := range strings.Split(plain(m.View().Content), "\n") {
		if strings.Contains(ln, text) {
			return i
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, plain(m.View().Content))
	return 0
}

func mouse(m tuiModel, msg tea.Msg) (tuiModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(tuiModel), cmd
}

func clipboard(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("nothing was sent to the clipboard")
	}
	msg := cmd()
	if got := fmt.Sprintf("%T", msg); got != "tea.setClipboardMsg" {
		t.Fatalf("the command sent %s, want an OSC 52 clipboard write", got)
	}
	return fmt.Sprint(msg)
}

func TestALiveShellIsDrawnInsideTheTabs(t *testing.T) {
	m := shellPane(t)
	got := plain(m.View().Content)
	for _, want := range []string{"elements (", "store-0003-rtr-01", "L060", "ctrl+] elements"} {
		if !strings.Contains(got, want) {
			t.Errorf("the session screen is missing %q:\n%s", want, got)
		}
	}
}

func TestOnlyAShellAsksForTheMouse(t *testing.T) {
	m := shellPane(t)
	for _, phase := range []tuiPhase{phasePicking, phaseResults, phaseMenu, phaseSettings, phaseMacros, phaseData} {
		m.phase = phase
		if got := m.View().MouseMode; got != tea.MouseModeNone {
			t.Errorf("phase %d asks for mouse mode %v, want the terminal to keep the mouse", phase, got)
		}
	}
	m.phase = phaseSession
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("a shell asks for mouse mode %v, want cell motion so the wheel and a drag reach it", got)
	}
}

func TestAWheelNotchScrollsTheShell(t *testing.T) {
	m := shellPane(t)

	m, _ = mouse(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.sesScroll != wheelStep {
		t.Fatalf("sesScroll = %d after a notch up, want %d", m.sesScroll, wheelStep)
	}
	for range 20 {
		m, _ = mouse(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	if m.sesScroll != 0 {
		t.Errorf("sesScroll = %d after winding down, want the live bottom", m.sesScroll)
	}
}

func TestADragCopiesTheLinesItCrosses(t *testing.T) {
	m := shellPane(t)
	from, to := rowOf(t, m, "L050"), rowOf(t, m, "L052")

	m, _ = mouse(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: from})
	m, _ = mouse(m, tea.MouseMotionMsg{Button: tea.MouseLeft, X: 10, Y: to})
	if !strings.Contains(m.View().Content, "\x1b[7m") {
		t.Error("the dragged text is not highlighted")
	}
	m, cmd := mouse(m, tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 10, Y: to})

	want := "L050  eth50 is up\nL051  eth51 is up\nL052  eth52"
	if got := clipboard(t, cmd); got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
	if m.status != "copied 3 lines" {
		t.Errorf("status = %q, want it to say what was copied", m.status)
	}
}

func TestADragKeepsItsTextWhenTheShellScrolls(t *testing.T) {
	m := shellPane(t)
	row := rowOf(t, m, "L055")

	m, _ = mouse(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: row})
	m, _ = mouse(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m, cmd := mouse(m, tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 0, Y: row})

	if got := clipboard(t, cmd); got != "L052  eth52 is up\nL053  eth53 is up\nL054  eth54 is up\nL055" {
		t.Errorf("copied %q, want from L052 down to where the drag began", got)
	}
}

func TestAClickWithoutADragCopiesNothing(t *testing.T) {
	m := shellPane(t)
	row := rowOf(t, m, "L050")

	m, _ = mouse(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: row})
	m, cmd := mouse(m, tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 2, Y: row})
	if cmd != nil {
		t.Errorf("a click sent %v, want nothing copied", cmd())
	}
	if strings.Contains(m.View().Content, "\x1b[7m") {
		t.Error("a click left a highlight")
	}
}

func TestAKeyClearsTheSelection(t *testing.T) {
	m := shellPane(t)
	from, to := rowOf(t, m, "L050"), rowOf(t, m, "L052")
	m, _ = mouse(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: from})
	m, _ = mouse(m, tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 5, Y: to})

	m = press(m, tea.Key{Code: 'a', Text: "a"})
	if strings.Contains(m.View().Content, "\x1b[7m") {
		t.Error("the highlight is still there after a key reached the shell")
	}
}
