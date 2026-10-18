package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

const (
	defaultSessionCols = 200
	defaultSessionRows = 50
)

var tabColors = []string{"7", "4", "6", "5", "12", "14", "13"}

func styleTabAt(i int, active bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(tabColors[i%len(tabColors)]))
	if active {
		return s.Reverse(true).Bold(true)
	}
	return s
}

type toolkitDialer struct{ c *api.Client }

func (d toolkitDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.c.DialToolkit(ctx, eid, cols, rows)
}

type sessionDirtyMsg string
type sessionOpenedMsg string
type sessionErrMsg string

func waitForSession(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		eid, ok := <-ch
		if !ok {
			return nil
		}
		return sessionDirtyMsg(eid)
	}
}

func openSessionCmd(ctx context.Context, mgr *ionterm.Manager, d ionterm.Dialer, eid, name string, cols, rows int, creds ionterm.Credentials) tea.Cmd {
	return func() tea.Msg {
		if _, err := mgr.Open(ctx, d, eid, name, cols, rows, creds); err != nil {
			return sessionErrMsg(err.Error())
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
	return max(20, m.width), max(3, m.height-4-m.slashHeight())
}

func (m tuiModel) openSession(eid, name string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "not connected to a tenant yet"
		return m, nil
	}
	cols, rows := m.sessionGeometry()
	m.connecting = name
	m.status = ""
	m.phase = phaseSession
	return m, tea.Batch(
		m.spinner.Tick,
		openSessionCmd(m.ctx, m.sessions, toolkitDialer{m.client}, eid, name, cols, rows, m.sessionCreds()),
	)
}

func (m tuiModel) sessionCreds() ionterm.Credentials {
	f := tenantFlags(m.store, m.tenant, m.ionUser)
	return ionterm.Credentials{User: f.IONUsername, Pass: f.IONPassword}
}

func (m tuiModel) openSessions(targets [][2]string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "not connected to a tenant yet"
		return m, nil
	}

	cols, rows := m.sessionGeometry()
	creds := m.sessionCreds()
	d := toolkitDialer{m.client}

	cmds := []tea.Cmd{m.spinner.Tick}
	for _, t := range targets {
		cmds = append(cmds, openSessionCmd(m.ctx, m.sessions, d, t[0], t[1], cols, rows, creds))
	}

	m.connecting = fmt.Sprintf("%d sessions", len(targets))
	if len(targets) == 1 {
		m.connecting = targets[0][1]
	}
	m.status = ""
	m.phase = phaseSession
	return m, tea.Batch(cmds...)
}

func (m tuiModel) tabCount() int { return 1 + m.sessions.Count() }

func (m tuiModel) currentTab() int {
	if m.phase != phaseSession {
		return 0
	}
	return 1 + m.sessions.ActiveIndex()
}

func (m tuiModel) selectTab(i int) tuiModel {
	n := m.tabCount()
	i = ((i % n) + n) % n
	if i == 0 {
		m.phase = phasePicking
		return m
	}
	m.sessions.Select(i - 1)
	m.phase = phaseSession
	return m
}

func (m tuiModel) onTabKey(key string) (tuiModel, bool) {
	switch key {
	case "alt+right":
		return m.selectTab(m.currentTab() + 1), true
	case "alt+left":
		return m.selectTab(m.currentTab() - 1), true
	case "alt+w":
		if m.sessions.Count() == 0 {
			return m, true
		}
		if s, ok := m.sessions.Detach(); ok {
			m.status = "detached " + s.Name + " — alt+t to reattach"
		}
		if m.sessions.Count() == 0 {
			m.phase = phasePicking
		}
		return m, true
	case "alt+k":
		if m.sessions.Count() == 0 {
			return m, true
		}
		m.sessions.CloseActive()
		if m.sessions.Count() == 0 {
			m.phase = phasePicking
		}
		return m, true
	}
	if len(key) == 5 && strings.HasPrefix(key, "alt+") && key[4] >= '1' && key[4] <= '9' {
		return m.selectTab(int(key[4] - '1')), true
	}
	return m, false
}

func (m tuiModel) onSessionKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.slash != slashOff {
		return m.onSlashKey(msg, key)
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
	}

	s := m.sessions.Active()
	if s == nil || s.Err() != nil {
		return m, nil
	}

	if key == "/" && s.LineEmpty() {
		return m.openSlash()
	}

	s.Type(uv.KeyPressEvent(uv.Key(tea.Key(msg))))
	return m, nil
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
	return m.openSession(eid, name)
}

func (m tuiModel) tabBar() string {
	cur := m.currentTab()
	render := func(i int, label string) string {
		return styleTabAt(i, i == cur).Render(label)
	}

	parts := []string{render(0, fmt.Sprintf(" 1 elements (%s) ", m.mode))}
	for i, s := range m.sessions.All() {
		mark := ""
		if s.Err() != nil {
			mark = markCross.String() + " "
		}
		parts = append(parts, render(i+1, fmt.Sprintf(" %d %s%s ", i+2, mark, s.Name)))
	}
	bar := strings.Join(parts, " ")
	if n := m.sessions.DetachedCount(); n > 0 {
		bar += styleDim.Render(fmt.Sprintf("  +%d detached", n))
	}
	return bar
}

func (m tuiModel) renderSession() (string, *tea.Cursor) {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")

	s := m.sessions.Active()
	if s == nil {
		if m.connecting != "" {
			b.WriteString(fmt.Sprintf("\n  %s connecting to %s\n", m.spinner.View(), m.connecting))
		} else {
			b.WriteString("\n" + styleDim.Render("  no sessions open") + "\n")
		}
		if m.status != "" {
			b.WriteString(styleErr.Render("  "+m.status) + "\n")
		}
		b.WriteString("\n" + styleDim.Render("ctrl+] elements"))
		return b.String(), nil
	}

	cols, rows := m.paneSize()
	view := s.View(cols, rows)
	b.WriteString(view.Content + "\n")

	if m.slash != slashOff {
		b.WriteString(m.viewSlash())
		return b.String(), nil
	}

	const hint = "ctrl+] elements · alt+←/→ tab · alt+w detach · alt+t reattach · alt+k close"

	if err := s.Err(); err != nil {
		b.WriteString(footer(err.Error(), hint))
		return b.String(), nil
	}

	b.WriteString(footer(m.status, hint))
	return b.String(), tea.NewCursor(view.CursorX, view.CursorY+1)
}

func (m tuiModel) viewSession() string {
	content, _ := m.renderSession()
	return content
}
