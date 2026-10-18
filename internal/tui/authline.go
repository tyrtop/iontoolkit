package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

// How long is left before a browser session is worth warning about.
const (
	sessionWarn   = time.Hour
	sessionUrgent = 15 * time.Minute
)

// sessionLeft is how long the current tenant can keep minting tokens without a
// browser. It prefers what this run actually saw, and otherwise reads the
// stored refresh token, so the figure is right from launch.
func (m tuiModel) sessionLeft() (time.Duration, bool) {
	if !m.hasTenant || m.authMode != authBrowser {
		return 0, false
	}
	if left, ok := m.ident.SessionLeft(); ok && m.ident.TSG == m.tenant.TSGID {
		return left, true
	}
	if m.store == nil {
		return 0, false
	}
	stored, err := m.store.Secrets.Get(config.RefreshTokenKey(m.tenant.TSGID))
	if err != nil || stored == "" {
		return 0, false
	}
	exp, ok := saseauth.SessionExpiry(stored)
	if !ok {
		return 0, false
	}
	return time.Until(exp), true
}

// authLine says which credential is in use and, for a browser account, how much
// of its session is left. The colour is the whole point: it has to be obvious
// at a glance whether a sign-in is about to be needed.
func (m tuiModel) authLine() string {
	if !m.hasTenant {
		return ui.StyleDim.Render("no tenant selected")
	}
	if m.authMode == authTenant {
		return ui.StyleDim.Render(authTenant.Label())
	}

	label := authBrowser.Label()

	if m.tokenDead != "" {
		return ui.StyleErr.Render(label + " · sign-in needed")
	}

	left, ok := m.sessionLeft()
	switch {
	case !ok:
		// Nothing stored and nothing minted: a sign-in is the next step.
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

// tokenRefreshedMsg is the outcome of the menu's token refresh.
type tokenRefreshedMsg struct {
	ident saseauth.Identity
	ttl   time.Duration
	err   error
}

// refreshToken re-mints without a browser, using the stored refresh token. It
// is the quiet counterpart to signing in: when the stored credential is spent
// it reports that rather than opening a window on its own.
func refreshToken(ctx context.Context, f app.Flags, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		src := app.BrowserSource(f, httpClientFor(f), st)
		if _, ttl, err := src.Token(ctx); err != nil {
			return tokenRefreshedMsg{err: err}
		} else {
			return tokenRefreshedMsg{ident: src.Identity(), ttl: ttl}
		}
	}
}

func (m tuiModel) startTokenRefresh() (tea.Model, tea.Cmd) {
	if !m.hasTenant {
		m.status = "no tenant selected"
		return m, nil
	}
	f := app.TenantFlags(m.store, m.tenant, m.ionUser)
	m.flags = f
	m.status = "refreshing token"
	return m, tea.Batch(m.spinner.Tick, refreshToken(m.ctx, f, m.store))
}

// signedIn reports whether a browser credential exists to work from: either a
// discovered tenant list, or a refresh token for the current tenant. A tenant
// record alone is not enough, since it can outlive the credential.
func (m tuiModel) signedIn() bool {
	if c, err := m.store.TenantCache(); err == nil && c != nil && len(c.Tenants) > 0 {
		return true
	}
	if !m.hasTenant || m.store == nil {
		return false
	}
	v, err := m.store.Secrets.Get(config.RefreshTokenKey(m.tenant.TSGID))
	return err == nil && v != ""
}
