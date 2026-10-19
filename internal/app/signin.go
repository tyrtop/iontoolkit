package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
)

// SignIn is the account-level browser sign-in: it stores the tenant-less
// credential and leaves a session in the browser profile, which is what lets
// each tenant be scoped afterwards with no further window.
//
// It lives here rather than in either front end because the TUI and the CLI
// have to sign in the same way. Two sign-in paths would be two sets of keys to
// keep straight.
func SignIn(ctx context.Context, st *config.Store, w io.Writer) (saseauth.Discovery, error) {
	opt := BrowserOptions(&http.Client{Timeout: Defaults.HTTPTimeout}, st)
	d, err := saseauth.Discover(ctx, opt, "")
	if err != nil {
		return d, fmt.Errorf("browser sign-in: %w", err)
	}
	if d.RefreshToken != "" {
		if err := st.Secrets.Set(config.AccountRefreshTokenKey(), d.RefreshToken); err != nil {
			return d, fmt.Errorf("save the account credential: %w", err)
		}
	}
	// The TUI's switcher reads this list, so a sign-in that left it behind
	// would be a sign-in the TUI cannot see.
	known := make([]config.KnownTenant, 0, len(d.Tenants))
	for _, t := range d.Tenants {
		known = append(known, config.KnownTenant{TSGID: t.ID, Name: t.Name})
	}
	if err := st.SaveTenantCache(&config.TenantCache{
		FetchedAt: time.Now(), Account: d.Identity.Display(), Tenants: known,
	}); err != nil {
		return d, err
	}
	fmt.Fprintf(w, "signed in as %s — %d tenants visible\n", d.Identity.Display(), len(d.Tenants))
	return d, nil
}

// WarmTenants gives each tenant a refresh token of its own, which is the only
// credential that lasts: a per-tenant token is rotated and re-stored on every
// use, while the account one is spent by whichever tenant narrows it first and
// cannot be both that tenant's credential and the next tenant's seed.
//
// One at a time, scoped against the browser session a SignIn has just left:
// that session is one profile and cannot be driven in parallel.
func WarmTenants(ctx context.Context, st *config.Store, tenants []config.Tenant, w io.Writer) error {
	httpClient := &http.Client{Timeout: Defaults.HTTPTimeout}
	var failed int

	for _, t := range tenants {
		if !t.UsesBrowser() {
			fmt.Fprintf(w, "  %-28s service account, nothing to sign in\n", t.Name)
			continue
		}
		// Started with no stored token and no seed: whatever was stored for
		// this tenant is why we are here, and the account credential is left
		// unspent for a run that has nothing else.
		src := saseauth.NewSource(t.TSGID, "", BrowserOptions(httpClient, st),
			func(rt string) error { return st.Secrets.Set(config.RefreshTokenKey(t.TSGID), rt) })
		if _, _, err := src.Token(ctx); err != nil {
			failed++
			fmt.Fprintf(w, "  %-28s %s\n", t.Name, ShortAuthError(err))
			continue
		}
		fmt.Fprintf(w, "  %-28s ready\n", t.Name)
	}

	if failed > 0 && failed == len(tenants) {
		return fmt.Errorf("no tenant could be signed in")
	}
	if failed > 0 {
		// Not an error: the tenants that did work are usable, and a tenant
		// the account cannot reach will not start working on a retry.
		fmt.Fprintf(w, "%d of %d tenants could not be signed in; this account may not have access to them\n",
			failed, len(tenants))
	}
	return nil
}

// SessionLeft is how long a tenant's stored credential can keep minting
// tokens. Reading the expiry out of the token costs nothing and spends
// nothing, so both front ends can say "expired" without a round trip.
func SessionLeft(st *config.Store, tsg string) (time.Duration, bool) {
	if st == nil {
		return 0, false
	}
	stored, err := st.Secrets.Get(config.RefreshTokenKey(tsg))
	if err != nil || stored == "" {
		return 0, false
	}
	exp, ok := saseauth.SessionExpiry(stored)
	if !ok {
		return 0, false
	}
	return time.Until(exp), true
}
