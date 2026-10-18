package app

import (
	"net/http"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

var _ api.TokenSource = (*saseauth.Source)(nil)

// BrowserOptions builds the browser settings for a store. The profile directory
// is the important part: the session cookie it holds is what lets later tenants
// be scoped without a second window.
func BrowserOptions(httpClient *http.Client, st *config.Store) saseauth.Options {
	opt := saseauth.Options{Client: httpClient}
	if st != nil {
		opt.DataDir = st.BrowserDir()
	}
	return opt
}

// BrowserSource builds a browser-backed token source for the tenant in f. A nil
// store is not fatal: the sign-in still works, it just will not be remembered
// for the next run.
//
// Nothing here opens a browser. Source.Token only spends credentials it already
// has, so a batch run against a browser tenant either works from the stored
// refresh token or fails asking to be signed in from the TUI, which is the only
// place a window is ever shown.
func BrowserSource(f Flags, httpClient *http.Client, st *config.Store) *saseauth.Source {
	opt := BrowserOptions(httpClient, st)
	if st == nil {
		return saseauth.NewSource(f.TSGID, "", opt, nil)
	}

	stored, _ := st.Secrets.Get(config.RefreshTokenKey(f.TSGID))
	if stored == "" {
		// A tenant with no credential of its own starts from the account's.
		// Narrowing that token's scope to this tenant is what makes a new
		// tenant usable without another trip through the browser.
		stored, _ = st.Secrets.Get(config.AccountRefreshTokenKey())
	}
	save := func(rt string) error {
		return st.Secrets.Set(config.RefreshTokenKey(f.TSGID), rt)
	}
	return saseauth.NewSource(f.TSGID, stored, opt, save)
}

// OpenStore returns the config store, or nil when it cannot be opened.
func OpenStore() *config.Store {
	st, err := config.Open()
	if err != nil {
		return nil
	}
	return st
}

// IsAuthFailure guesses whether a failure was the credential rather than the
// device. The wire gives us only text: a rejected websocket handshake reports
// the status it got instead of 101, and the REST calls report resp.Status.
func IsAuthFailure(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{
		"but got 401", "but got 403",
		"status 401", "status 403",
		"unauthorized", "forbidden", "token expired", "invalid_grant",
	} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}
