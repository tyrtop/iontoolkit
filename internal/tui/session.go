package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

const (
	defaultSessionCols = 200
	defaultSessionRows = 50
)

type toolkitDialer struct{ c *api.Client }

func (d toolkitDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.c.DialToolkit(ctx, eid, cols, rows)
}

func (m tuiModel) dialerFor(c *api.Client) ionterm.Dialer {
	if app.Fixture != nil {
		return app.Fixture.Dialer()
	}
	return toolkitDialer{c}
}

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

func openSessionCmd(ctx context.Context, mgr *ionterm.Manager, d ionterm.Dialer, eid, name, tsg string, cols, rows int, creds ionterm.Credentials) tea.Cmd {
	return func() tea.Msg {
		if _, err := mgr.Open(ctx, d, eid, name, cols, rows, creds); err != nil {
			return sessionErrMsg{
				target: sessionTarget{eid: eid, name: name, tsg: tsg},
				err:    err.Error(),
			}
		}
		return sessionOpenedMsg(eid)
	}
}

func (m tuiModel) sessionGeometry() (int, int) {
	cols, rows := m.cfg.SessionCols, m.cfg.SessionRows
	if cols <= 0 {
		cols = defaultSessionCols
	}
	if rows <= 0 {
		rows = defaultSessionRows
	}
	return cols, rows
}

func (m tuiModel) paneSize() (int, int) {
	return max(20, m.width), max(3, m.height-5-m.slashHeight()-m.statsRows())
}

func (m tuiModel) openSession(eid, name, tsg string) (tea.Model, tea.Cmd) {
	return m.openSessions([]sessionTarget{{eid: eid, name: name, tsg: tsg}})
}

func (m tuiModel) openOneSession(t sessionTarget) (tea.Model, tea.Cmd) {
	c := m.tenantClient(t.tsg)
	if c == nil {
		if m.status == "" {
			m.status = "could not reach " + m.tenantName(t.tsg)
		}
		return m, nil
	}
	cols, rows := m.sessionGeometry()
	m.connecting = t.name
	m.status = ""
	m.phase = phaseSession
	reportDialsTo(c, m.dialReports)
	m.markDialing(t.eid)
	return m, tea.Batch(
		m.spinner.Tick,
		waitForDial(m.dialReports),
		openSessionCmd(m.ctx, m.sessions, m.dialerFor(c), t.eid, t.name, t.tsg, cols, rows, m.credsFor(t.tsg)),
	)
}

func (m tuiModel) openSessions(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}
	m.lastTargets = targets
	if m.needsSignIn(tsgsOf(targets)) {
		return m.signInThenSessions(targets)
	}
	if conn := m.ensureClients(tsgsOf(targets)); len(conn) > 0 {
		m.resumeSessions = targets
		m.connectWait = len(conn)
		m.connecting = fmt.Sprintf("%d sessions", len(targets))
		if len(targets) == 1 {
			m.connecting = targets[0].name
		}
		m.status = ""
		m.phase = phaseSession
		return m, tea.Batch(append(conn, m.spinner.Tick)...)
	}

	if len(targets) == 1 {
		return m.openOneSession(targets[0])
	}

	cols, rows := m.sessionGeometry()

	cmds := []tea.Cmd{m.spinner.Tick, waitForDial(m.dialReports)}
	var unreachable []string
	for _, t := range targets {
		c := m.tenantClient(t.tsg)
		if c == nil {
			unreachable = append(unreachable, t.name)
			continue
		}
		reportDialsTo(c, m.dialReports)
		m.markDialing(t.eid)
		cmds = append(cmds, openSessionCmd(m.ctx, m.sessions, m.dialerFor(c),
			t.eid, t.name, t.tsg, cols, rows, m.credsFor(t.tsg)))
	}
	if len(cmds) == 2 {
		m.status = "could not reach " + strings.Join(unreachable, ", ")
		return m, nil
	}
	if len(unreachable) > 0 {
		m.status = "skipped " + strings.Join(unreachable, ", ") + ": tenant unreachable"
	}

	m.connecting = ""
	m.rebuildRows()
	return m, tea.Batch(cmds...)
}

func (m tuiModel) tabCount() int { return 1 + m.sessions.Count() + len(m.data) }

func (m tuiModel) currentTab() int {
	switch m.phase {
	case phaseSession:
		return 1 + m.sessions.ActiveIndex()
	case phaseData:
		return 1 + m.sessions.Count() + m.dataActive
	}
	return 0
}

func (m tuiModel) selectTab(i int) tuiModel {
	n := m.tabCount()
	i = ((i % n) + n) % n
	if i == 0 {
		m.phase = phasePicking
		return m
	}
	if i <= m.sessions.Count() {
		m.sessions.Select(i - 1)
		m.phase = phaseSession
		m.sesScroll = 0
		return m
	}
	m.dataActive = i - 1 - m.sessions.Count()
	m.phase = phaseData
	return m
}

func (m tuiModel) onTabKey(key string) (tuiModel, bool) {
	switch key {
	case "alt+right", "alt+l":
		return m.selectTab(m.currentTab() + 1), true
	case "alt+left", "alt+h":
		return m.selectTab(m.currentTab() - 1), true
	case "alt+w", "alt+k":
		if m.phase == phaseData {
			return m.closeDataTab(), true
		}
		if m.sessions.Count() == 0 {
			return m, true
		}
		if key == "alt+w" {
			if s, ok := m.sessions.Detach(); ok {
				m.status = "detached " + s.Name + " — alt+t to reattach"
			}
		} else {
			m.sessions.CloseActive()
		}
		if m.sessions.Count() == 0 {
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

	if next, handled := m.onTabKey(key); handled {
		return next, nil
	}

	if key == "alt+t" {
		return m.reopenLastTab()
	}

	switch key {
	case "ctrl+]":
		m.phase = phasePicking
		return m, nil
	case "alt+f":
		return m.openFavourites()
	}

	if n, ok := altDigit(key); ok {
		return m.useFavourite(n)
	}

	s := m.sessions.Active()
	if s == nil {
		return m, nil
	}

	if key == "alt+e" {
		return m.exportSession()
	}

	if key == "alt+r" {
		if s.Err() != nil || s.Hung() || s.Ended() {
			return m.redialActive()
		}
		m.status = "this session is still answering — alt+k closes it"
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
	if _, ok := m.sessions.Reattach(); ok {
		m.status = ""
		m.phase = phaseSession
		return m, waitForSession(m.sessions.Dirty())
	}

	eid, name, ok := m.sessions.LastClosed()
	if !ok {
		m.status = "nothing to reattach"
		return m, nil
	}
	return m.openSession(eid, name, m.tsgForElement(eid))
}

func (m tuiModel) redialActive() (tea.Model, tea.Cmd) {
	s := m.sessions.Active()
	if s == nil {
		return m, nil
	}
	eid, name := s.ElementID, s.Name
	tsg := m.tsgForElement(eid)
	if tsg == "" {
		tsg = m.tenant.TSGID
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
		m.sessions.CloseDeadOn(eid)
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

	parts := []string{render(ui.TabList, 0, 0, fmt.Sprintf(" elements (%s) ", m.mode))}
	for i, s := range m.sessions.All() {
		mark := ""
		switch {
		case s.Err() != nil:
			mark = ui.MarkCross.String() + " "
		case s.Hung():
			mark = "~ "
		}
		parts = append(parts, render(ui.TabShell, i+1, i, fmt.Sprintf(" %s%s ", mark, s.Name)))
	}
	for i, t := range m.data {
		n := 1 + m.sessions.Count() + i
		mark := ""
		if t.failed() {
			mark = ui.MarkCross.String() + " "
		}
		parts = append(parts, render(ui.TabData, n, i, fmt.Sprintf(" %s%s · data ", mark, t.name)))
	}
	bar := strings.Join(parts, " ")
	if n := m.sessions.DetachedCount(); n > 0 {
		bar += ui.StyleDim.Render(fmt.Sprintf("  +%d detached", n))
	}
	return bar
}

func (m tuiModel) renderSession() (string, *tea.Cursor) {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.connectBar() + "\n")

	s := m.sessions.Active()
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

	const hint = "ctrl+] elements · alt+←→/hl tab · alt+1-9 favourite · alt+f save · " + exportKeyHint +
		" scrollback · alt+w detach · alt+k close · shift+pgup scroll"

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

func (m tuiModel) viewSession() string {
	content, _ := m.renderSession()
	return content
}

func (m tuiModel) signInThenSessions(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		m.signInResume = true
		return m.startSignIn()
	}
	m.resumeSessions = targets
	return m.startSignIn()
}

func (m tuiModel) needsSignIn(tsgs []string) bool {
	if m.tokenDead == "" {
		return false
	}
	if len(tsgs) == 0 {
		return m.tenant.UsesBrowser()
	}
	for _, tsg := range tsgs {
		if t, ok := m.tenantOf(tsg); ok {
			if t.UsesBrowser() {
				return true
			}
			continue
		}
		return true
	}
	return false
}
