package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/fixture"
)

// An element whose answer to one command is longer than any pane, which is the
// only state where scrolling back shows anything.
func longResponseFixture(lines int) string {
	var b strings.Builder
	b.WriteString(`
tenants:
  - name: Acme Co
    tsg_id: "000000000"
    users:
      - name: admin
        password: ion
    elements:
      - name: store-0003-rtr-01
        model: ion 1200
        connected: true
        responses:
          "dump interface status": |
`)
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "            L%03d  eth%d is up\n", i, i)
	}
	return b.String()
}

// scrolledPane is a real session against the playback device, with the pane
// shorter than the emulator so there is output both above the window and in
// the scrollback.
func scrolledPane(t *testing.T) (tuiModel, *fixture.Set) {
	t.Helper()
	m, set := fixtureTUIWith(t, longResponseFixture(60))
	m.cfg.SessionCols, m.cfg.SessionRows = 80, 20
	m.width, m.height = 100, 20
	t.Cleanup(m.sessions.CloseAll)

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openSession(el.ID, el.Name, m.tenant.TSGID)
	m = next.(tuiModel)
	runCmd(t, cmd)
	waitForTUI(t, func() bool {
		s := m.sessions.Find(el.ID)
		return s != nil && strings.Contains(s.Render(), el.Name+"#")
	})

	s := m.sessions.Find(el.ID)
	s.SendText("dump interface status\r")
	waitForTUI(t, func() bool { return strings.Contains(s.Render(), "L060") })
	waitForTUI(t, func() bool { return s.Scrollback() > 0 })
	return m, set
}

func press(m tuiModel, key tea.Key) tuiModel {
	next, _ := m.Update(tea.KeyPressMsg(key))
	return next.(tuiModel)
}

var (
	keyPgUp   = tea.Key{Code: tea.KeyPgUp, Mod: tea.ModShift}
	keyPgDown = tea.Key{Code: tea.KeyPgDown, Mod: tea.ModShift}
	keyLive   = tea.Key{Code: tea.KeyEnd, Mod: tea.ModAlt}
)

func TestSessionPaneScrollsBackThroughWhatTheDeviceSaid(t *testing.T) {
	m, _ := scrolledPane(t)

	live := plain(m.viewSession())
	if !strings.Contains(live, "L060") {
		t.Fatalf("the live pane is not at the end of the output:\n%s", live)
	}
	if strings.Contains(live, "L001") {
		t.Fatalf("the whole answer fits the pane, so there is nothing to scroll:\n%s", live)
	}

	m = press(m, keyPgUp)
	if m.sesScroll == 0 {
		t.Fatal("shift+pgup did not lift the pane off the live bottom")
	}
	back := plain(m.viewSession())
	if strings.Contains(back, "L060") {
		t.Errorf("a page back and the last line is still showing:\n%s", back)
	}

	// Far enough to reach the first line the device sent, which only the
	// scrollback holds.
	for range 10 {
		m = press(m, keyPgUp)
	}
	top := plain(m.viewSession())
	if !strings.Contains(top, "L001") {
		t.Errorf("scrolling back never reached the first line:\n%s", top)
	}

	// And the offset stops where the output does, so coming back is one page.
	held := m.sesScroll
	m = press(m, keyPgUp)
	if m.sesScroll != held {
		t.Errorf("offset ran past the top: %d then %d", held, m.sesScroll)
	}
	m = press(m, keyPgDown)
	if m.sesScroll >= held {
		t.Errorf("a page down from the top left the offset at %d, want below %d", m.sesScroll, held)
	}
}

func TestScrolledPaneSaysItIsNotLive(t *testing.T) {
	m, _ := scrolledPane(t)

	if got := plain(m.viewSession()); strings.Contains(got, "scrolled back") {
		t.Errorf("the live pane claims to be scrolled back:\n%s", got)
	}

	m = press(m, keyPgUp)
	got := plain(m.viewSession())
	// Without this a pane that stopped moving reads as a session that hung.
	if !strings.Contains(got, "scrolled back") || !strings.Contains(got, "alt+end") {
		t.Errorf("a scrolled pane does not say so, or does not say how to get back:\n%s", got)
	}

	if _, cur := m.renderSession(); cur != nil {
		t.Error("a cursor was placed in a scrolled pane")
	}
	if _, cur := press(m, keyLive).renderSession(); cur == nil {
		t.Error("no cursor after returning to the live bottom")
	}
}

func TestScrollingKeepsTheFrameHeight(t *testing.T) {
	m, _ := scrolledPane(t)

	want := len(strings.Split(m.viewSession(), "\n"))
	for i, k := range []tea.Key{keyPgUp, keyPgUp, keyPgDown, keyLive} {
		m = press(m, k)
		if got := len(strings.Split(m.viewSession(), "\n")); got != want {
			t.Errorf("key %d left the frame %d lines tall, want %d", i, got, want)
		}
	}
}

func TestTypingReturnsThePaneToLive(t *testing.T) {
	m, _ := scrolledPane(t)

	m = press(m, keyPgUp)
	if m.sesScroll == 0 {
		t.Fatal("shift+pgup did not move the pane")
	}

	// A key goes to the device, which is about to answer it at the bottom.
	m = press(m, tea.Key{Code: 'd', Text: "d"})
	if m.sesScroll != 0 {
		t.Errorf("sesScroll = %d after typing, want the pane back at the live bottom", m.sesScroll)
	}
}

// iontk never asks for the mouse, on any page: reporting would take the
// terminal's own select-and-copy away, and the keys scroll every pane. The
// wheel keeps working because terminals send arrow keys for a notch in the
// alt screen.
func TestNoPageAsksForTheMouse(t *testing.T) {
	m, _ := scrolledPane(t)
	for _, phase := range []tuiPhase{phaseSession, phasePicking, phaseResults, phaseData, phaseMenu} {
		m.phase = phase
		if got := m.mouseMode(); got != tea.MouseModeNone {
			t.Errorf("phase %d mouseMode() = %v, want the terminal to keep the mouse", phase, got)
		}
	}
}

func TestSwitchingTabsReturnsToLive(t *testing.T) {
	m, _ := scrolledPane(t)

	m = press(m, keyPgUp)
	if m.sesScroll == 0 {
		t.Fatal("shift+pgup did not move the pane")
	}
	if got := m.selectTab(m.currentTab()).sesScroll; got != 0 {
		t.Errorf("sesScroll = %d after a tab switch, want 0", got)
	}
}
