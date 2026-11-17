package tui

import (
	"context"
	"errors"
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
		cmd, args := "session.open", map[string]string{"elements": t.eid}
		if t.retry {
			cmd, args = "session.retry", map[string]string{"element": t.eid}
		}
		if err := rt.Exec(&runtime.Call{Ctx: ctx, Args: args}, cmd); errors.Is(err, runtime.ErrStopped) {
			return nil
		} else if err != nil {
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
	retry := map[string]bool{}
	for _, t := range targets {
		retry[t.eid] = t.retry
	}
	cmds := []tea.Cmd{m.spinner.Tick, waitForDial(m.runtime().DialReports())}
	for _, r := range rows {
		m.runtime().MarkDialing(r.ID, r.Label())
		cmds = append(cmds, openSessionCmd(m.ctx, m.runtime(), sessionTarget{eid: r.ID, name: r.Label(), tsg: r.TSG, retry: retry[r.ID]}))
	}
	if len(targets) == 1 {
		m.frontDial = rows[0].ID
		m.phase = phaseSession
	}
	m.rebuildRows()
	return m, tea.Batch(cmds...)
}

func (m tuiModel) onTabKey(key string) (tuiModel, bool) {
	switch key {
	case "alt+right", "alt+l":
		return m.stepTab(1), true
	case "alt+left", "alt+h":
		return m.stepTab(-1), true
	case "alt+w", "alt+k":
		if m.phase == phaseData {
			return m.closeDataTab(), true
		}
		tabs := m.tabs()
		t := tabs[m.frontTab(tabs)]
		if t.kind != tabShell && t.kind != tabDialing {
			return m, true
		}
		cmd := "session.close"
		if key == "alt+w" {
			cmd = "session.detach"
		}
		var said []string
		call := &runtime.Call{Args: map[string]string{"element": t.eid}, Out: func(l string) { said = append(said, l) }}
		if err := m.runtime().Exec(call, cmd); err != nil {
			m.status = err.Error()
			return m, true
		}
		if key == "alt+w" && len(said) > 0 {
			m.status = said[0] + " — alt+t to reattach"
		}
		if m.frontDial == t.eid {
			m.frontDial = ""
		}
		return m.afterTabGone(), true
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

	s := m.frontSession()
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
	s := m.frontSession()
	if s == nil {
		return m, nil
	}
	if err := runtime.Retryable(s); err != nil {
		m.status = err.Error() + " — alt+k closes it"
		return m, nil
	}
	eid, name := s.ElementID, s.Name
	tsg := m.tsgForElement(eid)
	if tsg == "" {
		tsg = m.tenant().TSGID
	}

	m.status = ""
	next, cmd := m.openSessions([]sessionTarget{{eid: eid, name: name, tsg: tsg, retry: true}})
	nm, ok := next.(tuiModel)
	if !ok || cmd == nil {
		return next, cmd
	}
	nm.redialing = eid
	nm.status = "redialling " + name
	return nm, cmd
}

func (m tuiModel) afterTabGone() tuiModel {
	tabs := m.tabs()
	if m.frontTab(tabs) > 0 {
		return m
	}
	for _, t := range tabs {
		if t.kind == tabShell || t.kind == tabDialing {
			return m.showTab(t)
		}
	}
	m.phase = phasePicking
	return m
}

func (m tuiModel) onSessionOpened(eid string) tuiModel {
	for _, s := range m.sessions().Detached() {
		if s.ElementID == eid && m.sessions().Find(eid) == nil {
			m.status = s.Name + " is up and detached — alt+t to reattach"
		}
	}
	if m.frontDial == eid {
		m.frontDial = ""
	}
	m.dialDone(eid)
	if m.redialing == eid {
		m.redialing = ""
		m.status = ""
		m.sesScroll = 0
	}
	m.rebuildRows()
	return m
}

func (m tuiModel) renderSession() (string, *tea.Cursor) {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.connectBar() + "\n")

	s := m.frontSession()
	if _, dialing := m.dialingFront(); dialing || s == nil {
		if !dialing {
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
	b.WriteString(m.paintSelection(s.ElementID, view, cols) + "\n")

	if m.stats {
		b.WriteString(m.statsPane() + "\n")
	}

	if m.slash != slashOff {
		b.WriteString(m.viewSlash())
		return b.String(), nil
	}

	hint := "ctrl+] elements · alt+←→/hl tab · alt+f favourites · " +
		exportKeyHint + " · alt+w detach · alt+k close · shift+pgup scroll"

	status := m.status
	switch {
	case m.redialing == s.ElementID:
	case s.Err() != nil:
		status = s.Err().Error() + " — alt+r opens a new session on this ION"
	case s.Hung():
		status = "connection stopped responding — alt+r opens a new session on this ION"
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

func (m tuiModel) nextTab() (tea.Model, tea.Cmd) { return m.stepTab(1), nil }

func (m tuiModel) prevTab() (tea.Model, tea.Cmd) { return m.stepTab(-1), nil }

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
		if m.frontSession() == nil {
			return m, nil
		}
		return run(m)
	}
}
