package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/saseauth"
)

func Discover(ctx context.Context, st *config.Store) (saseauth.Discovery, error) {
	return saseauth.Discover(ctx, BrowserOptions(&http.Client{Timeout: Defaults.HTTPTimeout}, st))
}

func SaveDiscovery(st *config.Store, d saseauth.Discovery) error {
	if d.RefreshToken != "" {
		if err := st.Secrets.Set(config.AccountRefreshTokenKey(), d.RefreshToken); err != nil {
			return fmt.Errorf("save the account credential: %w", err)
		}
	}
	known := make([]config.KnownTenant, 0, len(d.Tenants))
	for _, t := range d.Tenants {
		known = append(known, config.KnownTenant{TSGID: t.ID, Name: t.Name})
	}
	return st.SaveTenantCache(&config.TenantCache{
		FetchedAt: time.Now(), Account: d.Identity.Display(), Tenants: known,
	})
}

type TenantWarm struct {
	Tenant  config.Tenant
	Skipped bool
	Err     error
}

// the refresh token rotates on redeem and narrows to one tenant
func WarmEach(ctx context.Context, st *config.Store, tenants []config.Tenant) []TenantWarm {
	httpClient := &http.Client{Timeout: Defaults.HTTPTimeout}
	out := make([]TenantWarm, 0, len(tenants))
	logging.L().Info("warming every tenant", "tenants", len(tenants))
	for _, t := range tenants {
		if !t.UsesBrowser() {
			out = append(out, TenantWarm{Tenant: t, Skipped: true})
			continue
		}
		_, _, err := tenantSource(st, httpClient, t.TSGID).Token(ctx)
		logging.L().Info("tenant warmed", "tsg", t.TSGID, "tenant", t.Name, "err", err)
		out = append(out, TenantWarm{Tenant: t, Err: err})
		if ctx.Err() != nil {
			return out
		}
	}
	return out
}

func WarmReport(results []TenantWarm, w io.Writer) error {
	var failed, browser int

	for _, r := range results {
		if r.Skipped {
			fmt.Fprintf(w, "  %-28s service account, nothing to sign in\n", r.Tenant.Name)
			continue
		}
		browser++
		if r.Err != nil {
			failed++
			fmt.Fprintf(w, "  %-28s %s\n", r.Tenant.Name, ShortAuthError(r.Err))
			continue
		}
		fmt.Fprintf(w, "  %-28s ready\n", r.Tenant.Name)
	}

	if failed > 0 && failed == browser {
		return fmt.Errorf("no tenant could be signed in: every one failed the same way, " +
			"so it is the browser sign-in that did not take and not access to any one tenant")
	}
	if failed > 0 {
		fmt.Fprintf(w, "%d of %d tenants could not be signed in; this account may not have access to them\n",
			failed, len(results))
	}
	return nil
}

func tenantSource(st *config.Store, httpClient *http.Client, tsg string) *saseauth.Source {
	return BrowserSource(Flags{TSGID: tsg, BrowserAuth: true}, httpClient, st)
}

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
