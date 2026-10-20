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

func WarmTenants(ctx context.Context, st *config.Store, tenants []config.Tenant, w io.Writer) error {
	httpClient := &http.Client{Timeout: Defaults.HTTPTimeout}
	var failed int

	for _, t := range tenants {
		if !t.UsesBrowser() {
			fmt.Fprintf(w, "  %-28s service account, nothing to sign in\n", t.Name)
			continue
		}
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
		fmt.Fprintf(w, "%d of %d tenants could not be signed in; this account may not have access to them\n",
			failed, len(tenants))
	}
	return nil
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
