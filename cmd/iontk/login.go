package main

import (
	"net/http"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

var _ api.TokenSource = (*saseauth.Source)(nil)

// browserSource builds a browser-backed token source for the tenant in f. A nil
// store is not fatal: the sign-in still works, it just will not be remembered
// for the next run.
//
// Nothing here opens a browser. Source.Token only spends credentials it already
// has, so a batch run against a browser tenant either works from the stored
// refresh token or fails asking to be signed in from the TUI, which is the only
// place a window is ever shown.
func browserSource(f flags, httpClient *http.Client, st *config.Store) *saseauth.Source {
	opt := saseauth.Options{Client: httpClient}
	if st == nil {
		return saseauth.NewSource(f.TSGID, "", opt, nil)
	}

	opt.DataDir = st.BrowserDir()
	stored, _ := st.Secrets.Get(config.RefreshTokenKey(f.TSGID))
	save := func(rt string) error {
		return st.Secrets.Set(config.RefreshTokenKey(f.TSGID), rt)
	}
	return saseauth.NewSource(f.TSGID, stored, opt, save)
}

// openStore returns the config store, or nil when it cannot be opened.
func openStore() *config.Store {
	st, err := config.Open()
	if err != nil {
		return nil
	}
	return st
}
