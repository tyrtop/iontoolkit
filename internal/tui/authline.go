package tui

import (
	"context"
	"fmt"
	"time"
	"tyrtop.com/iontk/internal/runtime"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

const (
	sessionWarn   = time.Hour
	sessionUrgent = 15 * time.Minute
)

func (m tuiModel) sessionLeft() (time.Duration, bool) {
	if !m.hasTenant() || m.authMode() != authBrowser {
		return 0, false
	}
	if id := m.runtime().Identity(); id.TSG == m.tenant().TSGID {
		if left, ok := id.SessionLeft(); ok {
			return left, true
		}
	}
	return app.SessionLeft(m.store(), m.tenant().TSGID)
}

func (m tuiModel) authLine() string {
	if !m.hasTenant() {
		return ui.StyleDim.Render("no tenant selected")
	}
	if m.authMode() == authTenant {
		return m.serviceAccountLine()
	}

	label := authBrowser.Label()

	if m.runtime().TokenDead() != "" {
		return ui.StyleErr.Render(label + " · sign-in needed")
	}

	left, ok := m.sessionLeft()
	switch {
	case !ok:
		return ui.StyleErr.Render(label + " · sign-in needed")
	case left <= 0:
		return ui.StyleErr.Render(label + " · expired")
	case left < sessionUrgent:
		return ui.StyleHot.Render(label + " · " + ui.Age(left) + " left")
	case left < sessionWarn:
		return ui.StyleWarn.Render(label + " · " + ui.Age(left) + " left")
	}
	return ui.StyleOK.Render(label)
}

func (m tuiModel) serviceAccountLine() string {
	who := m.tenant().ClientID
	if who == "" {
		who = "client id not saved"
	}

	n := m.tenantElementCount()
	count := fmt.Sprintf("%d elements cached", n)
	if n == 1 {
		count = "1 element cached"
	}
	if total := len(m.rows()); total > n {
		count = fmt.Sprintf("%d of %d elements cached", n, total)
	}

	head := authTenant.Label() + " · "
	name := ui.Truncate(m.tenant().Name, 24)
	tail := " · " + count

	if m.width > 0 {
		who = ui.Truncate(who, max(8, m.width-ui.Width(head+name+tail)-3))
	}

	return ui.StyleDim.Render(head) + ui.StyleElement.Render(name) +
		ui.StyleDim.Render(" · "+who+tail)
}

func (m tuiModel) tenantElementCount() int {
	if len(m.rows()) == 0 {
		return m.cacheCount()
	}
	n := 0
	for _, r := range m.rows() {
		if r.TSG == m.tenant().TSGID {
			n++
		}
	}
	return n
}

type tokenRefreshedMsg struct {
	err error
}

func refreshToken(ctx context.Context, rt *runtime.Runtime) tea.Cmd {
	return func() tea.Msg {
		return tokenRefreshedMsg{err: rt.Exec(&runtime.Call{Ctx: ctx}, "auth.refresh")}
	}
}

func (m tuiModel) startTokenRefresh() (tea.Model, tea.Cmd) {
	if !m.hasTenant() {
		m.status = "no tenant selected"
		return m, nil
	}
	m.status = "refreshing token"
	return m, tea.Batch(m.spinner.Tick, refreshToken(m.ctx, m.runtime()))
}
