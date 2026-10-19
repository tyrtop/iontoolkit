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

// dialerFor is how a session reaches an element. Fixture mode substitutes a
// playback device here and nowhere else, so the pane, autologin and the
// emulator are all still the production ones.
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
		// ensureClients already tried; reaching here means that tenant could
		// not authenticate, and tenantErr has said why.
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
	// Remembered so a credential failure can replay exactly this request after
	// signing in.
	m.lastTargets = targets
	if m.needsSignIn(tsgsOf(targets)) {
		return m.signInThenSessions(targets)
	}
	// Any tenant without a client is authenticated first and the request
	// replayed. This has to come before the single-target path below, which
	// would otherwise report a disconnected tenant instead of connecting it.
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

	// Opening several at once stays on the list: the tabs fill in behind, and
	// jumping to whichever finished last hid the rest of them.
	m.connecting = ""
	m.rebuildRows()
	return m, tea.Batch(cmds...)
}

// The tab ring is the element list, then the shells, then the data panes.
// Sessions keep their numbers as data tabs come and go, which they would not
// if the two kinds interleaved by the order they were opened.
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
	// hjkl's own pair for the tab ring, since the lists move on hjkl too.
	case "alt+right", "alt+l":
		return m.selectTab(m.currentTab() + 1), true
	case "alt+left", "alt+h":
		return m.selectTab(m.currentTab() - 1), true
	case "alt+w", "alt+k":
		// A data pane has no connection to detach, so both keys close it.
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

	// Before the session checks below: a favourite is worth reaching on a tab
	// whose connection has died, if only to copy it into a fresh one.
	if n, ok := altDigit(key); ok {
		return m.useFavourite(n)
	}

	s := m.sessions.Active()
	if s == nil {
		return m, nil
	}

	// Before the checks below, like the favourites: what a tab last said is
	// the reason anyone is looking at a dead one.
	if key == "alt+y" {
		return m.copySession()
	}

	// A redial is a new CLI session: enable and config context are gone, so
	// it is never automatic for a shell someone is sitting in front of. It
	// has to cover a tab that failed as well as one that went quiet -- a torn
	// connection is the common way to lose a session, and alt+r used to do
	// nothing at all on one.
	if key == "alt+r" {
		if s.Err() != nil || s.Hung() || s.Ended() {
			return m.redialActive()
		}
		// Swallowed rather than typed at the device: the footer offers this
		// key, so sending it as an escape sequence is the one thing it must
		// not do.
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

	// Anything typed belongs at the live bottom: the device is about to answer
	// it, and a pane held above that would not show what it said.
	m.sesScroll = 0

	// Counted before the key is forwarded: sending it clears the line, and
	// the device echoes it away.
	var save tea.Cmd
	if key == "enter" {
		save = m.recordSessionLine()
	}
	s.Type(uv.KeyPressEvent(uv.Key(tea.Key(msg))))
	return m, save
}

// onScrollKey holds the pane above its live bottom. The device has no
// scrollback of its own, so these are iontk's keys and have to be ones a CLI
// would not want: shift and alt chords, never a bare key.
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

// reopenLastTab brings back the most recent tab: the detached session itself
// when it is still alive, otherwise a fresh session to the same element. A
// shell that was left with "exit", or that died while detached, has nothing to
// reattach to, so dialling again is the only thing that can honour alt+t.
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

// redialActive replaces a hung tab with a fresh session on the same element.
func (m tuiModel) redialActive() (tea.Model, tea.Cmd) {
	s := m.sessions.Active()
	if s == nil {
		return m, nil
	}
	eid, name := s.ElementID, s.Name
	tsg := m.tsgForElement(eid)
	if tsg == "" {
		// An element the list no longer holds -- a reattached tab after a
		// cache drop -- would otherwise redial with no tenant and report that
		// nothing could be reached.
		tsg = m.tenant.TSGID
	}

	// The dead tab stays until the new session is actually up. It is the only
	// record of what the session said and how it failed, and closing it first
	// left the user looking at another ION's pane with nothing to say a dial
	// was even running. onSessionOpened swaps them over.
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

// onSessionOpened finishes a redial by taking the dead tab away, now that
// there is a live one on the same element to put in its place.
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
	render := func(i int, label string) string {
		return ui.StyleTab(i, i == cur).Render(label)
	}

	// No numbers any more: alt+N is a favourite command, and a number on a
	// tab is an invitation to press the chord that no longer selects it.
	parts := []string{render(0, fmt.Sprintf(" elements (%s) ", m.mode))}
	for i, s := range m.sessions.All() {
		mark := ""
		switch {
		case s.Err() != nil:
			mark = ui.MarkCross.String() + " "
		case s.Hung():
			mark = "~ "
		}
		parts = append(parts, render(i+1, fmt.Sprintf(" %s%s ", mark, s.Name)))
	}
	for i, t := range m.data {
		n := 1 + m.sessions.Count() + i
		mark := ""
		if t.failed() {
			mark = ui.MarkCross.String() + " "
		}
		parts = append(parts, render(n, fmt.Sprintf(" %s%s · data ", mark, t.name)))
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

	// Above the palette as well as above the footer, so the frame keeps its
	// height whichever is showing.
	if m.stats {
		b.WriteString(m.statsPane() + "\n")
	}

	if m.slash != slashOff {
		b.WriteString(m.viewSlash())
		return b.String(), nil
	}

	const hint = "ctrl+] elements · alt+←→/hl tab · alt+1-9 favourite · alt+f save · " + copyKeyHint +
		" scrollback · alt+w detach · alt+k close · shift+pgup scroll"

	status := m.status
	switch {
	// While a redial is running, the footer says that rather than offering
	// the key that started it again.
	case m.redialing == s.ElementID:
	case s.Err() != nil:
		// The key is on the error too: a torn connection is the usual way to
		// lose a session, and the error alone said nothing about what to do.
		status = s.Err().Error() + " — alt+r opens a new session on this ION"
	case s.Hung():
		status = "connection stopped answering — alt+r opens a new session on this ION"
	}

	// The marker goes in front of whatever else the footer was saying, and
	// does not replace it: a hung session is still worth reading while someone
	// is scrolled back through what it last said.
	if view.Offset > 0 {
		marker := fmt.Sprintf("scrolled back %d of %d lines — alt+end for live", view.Offset, view.Total)
		if status != "" {
			marker += " · "
		}
		status = marker + status
	}

	// Truncated plain, before the footer styles it: a status or hint that takes
	// a second row moves the whole frame.
	width := max(10, m.width-1)
	b.WriteString(ui.Footer(ui.Truncate(status, width), ui.Truncate(hint, width)))

	// No cursor unless the pane is live. Pointing it at frozen output, at a
	// session that has stopped answering, or at a pane with the slot picker
	// over it says something is being typed there.
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

// signInThenSessions reopens the browser and, once signed in, opens the
// sessions that were interrupted. With nothing to replay it just reports.
func (m tuiModel) signInThenSessions(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		m.signInResume = true
		return m.startSignIn()
	}
	m.resumeSessions = targets
	return m.startSignIn()
}

// needsSignIn reports whether the credential is already known to be dead, so
// an action can re-auth before attempting a dial that would only fail.
//
// It asks the tenants the action touches; the browsed tenant need not be one of
// them.
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
		// Unrecorded but in the list: only a browser sign-in found it.
		return true
	}
	return false
}
