package tui

import (
	"context"
	"fmt"
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
		return m.serviceAccountLine()
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

// serviceAccountLine says which service account is attached and what it has.
// Which one matters: the tenant name does not name the credential, and a
// service account reaches exactly one tenant, so the figure above it is not
// necessarily this account's.
func (m tuiModel) serviceAccountLine() string {
	who := m.tenant.ClientID
	if who == "" {
		// Records saved before the client ID was kept, and runs whose
		// credentials came from the environment.
		who = "client id not saved"
	}

	n := m.tenantElementCount()
	count := fmt.Sprintf("%d elements cached", n)
	if n == 1 {
		count = "1 element cached"
	}
	// More than one tenant is in the working set, so the cache line above is
	// the total and this is only this account's share of it.
	if total := len(m.all); total > n {
		count = fmt.Sprintf("%d of %d elements cached", n, total)
	}

	head := authTenant.Label() + " · "
	name := ui.Truncate(m.tenant.Name, 24)
	tail := " · " + count

	// The client id is the long part, and this has to stay one line: a wrapped
	// menu line pushes everything below it down a row. A width of zero is a
	// model that has not been told the terminal size yet.
	if m.width > 0 {
		who = ui.Truncate(who, max(8, m.width-ui.Width(head+name+tail)-3))
	}

	return ui.StyleDim.Render(head) + ui.StyleElement.Render(name) +
		ui.StyleDim.Render(" · "+who+tail)
}

// tenantElementCount is how many elements the current tenant has, counted from
// the loaded list rather than re-read from the cache file on every frame.
// Before anything has loaded, the launch-time count is this tenant's.
func (m tuiModel) tenantElementCount() int {
	if len(m.all) == 0 {
		return m.cacheCount
	}
	n := 0
	for _, r := range m.all {
		if r.TSG == m.tenant.TSGID {
			n++
		}
	}
	return n
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
