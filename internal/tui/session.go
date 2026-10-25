package tui

import (
	"context"
	"fmt"
	"strings"
	"tyrtop.com/iontk/internal/runtime"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/ui"
)

type sessionDirtyMsg string
type sessionOpenedMsg string

type sessionErrMsg struct {
	target sessionTarget
	err    string
}

func waitForSession(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		eid, ok := <-ch
		if !ok {
			return nil
		}
		return sessionDirtyMsg(eid)
	}
}

func openSessionCmd(ctx context.Context, rt *runtime.Runtime, t sessionTarget) tea.Cmd {
	return func() tea.Msg {
		err := rt.Exec(&runtime.Call{Ctx: ctx, Args: map[string]string{"elements": t.eid}}, "session.open")
		if err != nil {
			return sessionErrMsg{target: t, err: err.Error()}
		}
		return sessionOpenedMsg(t.eid)
	}
}

func (m tuiModel) sessionGeometry() (int, int) { return m.runtime().Geometry() }

func (m tuiModel) paneSize() (int, int) {
	return max(20, m.width), max(3, m.height-5-m.slashHeight()-m.statsRows())
}

func (m tuiModel) openSession(eid, name, tsg string) (tea.Model, tea.Cmd) {
	return m.openSessions([]sessionTarget{{eid: eid, name: name, tsg: tsg}})
}

func (m tuiModel) openSessions(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}
	m.lastTargets = targets
	if m.runtime().NeedsSignIn(tsgsOf(targets)) {
		return m.signInThenSessions(targets)
	}
	rows, note := m.runtime().Reach(rowsOf(targets))
	m.status = note
	if len(rows) == 0 {
		return m, nil
	}
	cmds := []tea.Cmd{m.spinner.Tick, waitForDial(m.runtime().DialReports())}
	for _, r := range rows {
		m.runtime().MarkDialing(r.ID)
		cmds = append(cmds, openSessionCmd(m.ctx, m.runtime(), sessionTarget{eid: r.ID, name: r.Label(), tsg: r.TSG}))
	}
	if len(targets) == 1 {
		m.connecting = rows[0].Label()
		m.phase = phaseSession
	} else {
		m.connecting = ""
		m.rebuildRows()
	}
	return m, tea.Batch(cmds...)
}

func (m tuiModel) front() string {
	switch m.phase {
	case phaseSession:
		return runtime.FrontSession
	case phaseData:
		return runtime.FrontData
	}
	return runtime.FrontList
}

func (m tuiModel) showFront(front string) tuiModel {
	switch front {
	case runtime.FrontSession:
		m.phase, m.sesScroll = phaseSession, 0
	case runtime.FrontData:
		m.phase = phaseData
	default:
		m.phase = phasePicking
	}
	return m
}

func (m tuiModel) currentTab() int {
	_, cur := m.runtime().Tabs(m.front())
	return cur
}

func (m tuiModel) stepTab(cmd string) tuiModel {
	c := &runtime.Call{Args: map[string]string{"from": m.front()}}
	if err := m.runtime().Exec(c, cmd); err != nil {
		m.status = err.Error()
		return m
	}
	front, _ := c.Result.(string)
	return m.showFront(front)
}

func (m tuiModel) onTabKey(key string) (tuiModel, bool) {
	switch key {
	case "alt+right", "alt+l":
		return m.stepTab("tab.next"), true
	case "alt+left", "alt+h":
		return m.stepTab("tab.prev"), true
	case "alt+w", "alt+k":
		if m.phase == phaseData {
			return m.closeDataTab(), true
		}
		if m.sessions().Count() == 0 {
			return m, true
		}
		if key == "alt+w" {
			var said []string
			_ = m.runtime().Exec(&runtime.Call{Out: func(l string) { said = append(said, l) }}, "session.detach")
			if len(said) > 0 {
				m.status = said[0] + " — alt+t to reattach"
			}
		} else {
			_ = m.runtime().Run("session.close", nil)
		}
		if m.sessions().Count() == 0 {
			m.phase = phasePicking
		}
		return m, true
	}
	return m, false
}

func (m tuiModel) onSessionKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.slash != slashOff {
		return m.onSlashKey(msg, key)
	}
	if m.favPick {
		return m.onFavouriteKey(key)
	}

	if next, cmd, ran := m.bindingOn(scrSession, key); ran {
		return next, cmd
	}

	s := m.sessions().Active()
	if s == nil {
		return m, nil
	}

	if s.Err() != nil || s.Hung() {
		return m, nil
	}

	if key == "/" && s.LineEmpty() {
		return m.openSlash()
	}

	if next, handled := m.onScrollKey(s, key); handled {
		return next, nil
	}

	m.sesScroll = 0

	var save tea.Cmd
	if key == "enter" {
		save = m.recordSessionLine()
	}
	s.Type(uv.KeyPressEvent(uv.Key(tea.Key(msg))))
	return m, save
}

func (m tuiModel) onScrollKey(s *ionterm.Session, key string) (tuiModel, bool) {
	_, rows := m.paneSize()
	switch key {
	case "shift+pgup":
		m.sesScroll += max(1, rows-1)
	case "shift+pgdown":
		m.sesScroll = max(0, m.sesScroll-max(1, rows-1))
	case "alt+up":
		m.sesScroll++
	case "alt+down":
		m.sesScroll = max(0, m.sesScroll-1)
	case "alt+end":
		m.sesScroll = 0
	default:
		return m, false
	}
	m.sesScroll = min(m.sesScroll, s.MaxScroll(rows))
	return m, true
}

func (m tuiModel) reopenLastTab() (tea.Model, tea.Cmd) {
	s, eid, err := m.runtime().Reopen()
	switch {
	case err != nil:
		m.status = err.Error()
		return m, nil
	case s != nil:
		m.status = ""
		m.phase = phaseSession
		return m, waitForSession(m.sessions().Dirty())
	}
	_, name, _ := m.sessions().LastClosed()
	return m.openSession(eid, name, m.tsgForElement(eid))
}

func (m tuiModel) redialActive() (tea.Model, tea.Cmd) {
	s := m.sessions().Active()
	if s == nil {
		return m, nil
	}
	eid, name := s.ElementID, s.Name
	tsg := m.tsgForElement(eid)
	if tsg == "" {
		tsg = m.tenant().TSGID
	}

	m.status = ""
	next, cmd := m.openSession(eid, name, tsg)
	nm, ok := next.(tuiModel)
	if !ok || cmd == nil {
		return next, cmd
	}
	nm.redialing = eid
	nm.status = "redialling " + name
	return nm, cmd
}

func (m tuiModel) onSessionOpened(eid string) tuiModel {
	m.connecting = ""
	m.dialDone(eid)
	if m.redialing == eid {
		m.redialing = ""
		m.status = ""
		m.sessions().CloseDeadOn(eid)
		m.sesScroll = 0
	}
	m.rebuildRows()
	return m
}

func (m tuiModel) tabBar() string {
	cur := m.currentTab()
	render := func(kind ui.TabKind, ring, nth int, label string) string {
		return ui.StyleTab(kind, ring, nth, ring == cur).Render(label)
	}

	parts := []string{render(ui.TabList, 0, 0, fmt.Sprintf(" elements (%s) ", m.mode()))}
	for i, s := range m.sessions().All() {
		mark := ""
		switch {
		case s.Err() != nil:
			mark = ui.MarkCross.String() + " "
		case s.Hung():
			mark = "~ "
		}
		parts = append(parts, render(ui.TabShell, i+1, i, fmt.Sprintf(" %s%s ", mark, s.Name)))
	}
	for i, t := range m.dataTabs() {
		n := 1 + m.sessions().Count() + i
		mark := ""
		if t.Failed() {
			mark = ui.MarkCross.String() + " "
		}
		parts = append(parts, render(ui.TabData, n, i, fmt.Sprintf(" %s%s · data ", mark, t.Name)))
	}
	bar := strings.Join(parts, " ")
	if n := m.sessions().DetachedCount(); n > 0 {
		bar += ui.StyleDim.Render(fmt.Sprintf("  +%d detached", n))
	}
	return bar
}

func (m tuiModel) renderSession() (string, *tea.Cursor) {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.connectBar() + "\n")

	s := m.sessions().Active()
	if s == nil {
		if m.connecting != "" {
			b.WriteString(fmt.Sprintf("\n  %s connecting to %s\n", m.spinner.View(), m.connecting))
		} else {
			b.WriteString("\n" + ui.StyleDim.Render("  no sessions open") + "\n")
		}
		if m.status != "" {
			b.WriteString(ui.StyleErr.Render("  "+m.status) + "\n")
		}
		b.WriteString("\n" + ui.StyleDim.Render("ctrl+] elements"))
		return b.String(), nil
	}

	cols, rows := m.paneSize()
	view := s.ScrolledView(cols, rows, m.sesScroll)
	b.WriteString(view.Content + "\n")

	if m.stats {
		b.WriteString(m.statsPane() + "\n")
	}

	if m.slash != slashOff {
		b.WriteString(m.viewSlash())
		return b.String(), nil
	}

	hint := "ctrl+] elements · " + m.mouseHint() + " · alt+←→/hl tab · alt+f favourites · " +
		exportKeyHint + " · alt+w detach · alt+k close · shift+pgup scroll"

	status := m.status
	switch {
	case m.redialing == s.ElementID:
	case s.Err() != nil:
		status = s.Err().Error() + " — alt+r opens a new session on this ION"
	case s.Hung():
		status = "connection stopped answering — alt+r opens a new session on this ION"
	}

	if view.Offset > 0 {
		marker := fmt.Sprintf("scrolled back %d of %d lines — alt+end for live", view.Offset, view.Total)
		if status != "" {
			marker += " · "
		}
		status = marker + status
	}

	width := max(10, m.width-1)
	b.WriteString(ui.Footer(ui.Truncate(status, width), ui.Truncate(hint, width)))

	if m.favPick {
		return m.overlayFavourites(b.String()), nil
	}
	if view.Offset > 0 || s.Err() != nil || s.Hung() {
		return b.String(), nil
	}
	return b.String(), tea.NewCursor(view.CursorX, view.CursorY+2)
}

func (m tuiModel) signInThenSessions(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		m.signInResume = true
		return m.startSignIn()
	}
	m.resumeSessions = targets
	return m.startSignIn()
}

func (m tuiModel) nextTab() (tea.Model, tea.Cmd) { return m.stepTab("tab.next"), nil }

func (m tuiModel) prevTab() (tea.Model, tea.Cmd) { return m.stepTab("tab.prev"), nil }

func (m tuiModel) detachTab() (tea.Model, tea.Cmd) {
	next, _ := m.onTabKey("alt+w")
	return next, nil
}

func (m tuiModel) closeTab() (tea.Model, tea.Cmd) {
	next, _ := m.onTabKey("alt+k")
	return next, nil
}

func (m tuiModel) sessionBack() (tea.Model, tea.Cmd) {
	m.phase = phasePicking
	return m, nil
}

func onActiveSession(run func(tuiModel) (tea.Model, tea.Cmd)) func(tuiModel) (tea.Model, tea.Cmd) {
	return func(m tuiModel) (tea.Model, tea.Cmd) {
		if m.sessions().Active() == nil {
			return m, nil
		}
		return run(m)
	}
}

func (m tuiModel) redialIfDead() (tea.Model, tea.Cmd) {
	s := m.sessions().Active()
	if s.Err() != nil || s.Hung() || s.Ended() {
		return m.redialActive()
	}
	m.status = "this session is still answering — alt+k closes it"
	return m, nil
}
