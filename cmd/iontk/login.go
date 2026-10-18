package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

var _ api.TokenSource = (*saseauth.Source)(nil)

// browserSource builds a browser-backed token source for the tenant in f. A nil
// store is not fatal: the sign-in still works, it just will not be remembered
// for the next run.
func browserSource(f flags, httpClient *http.Client, st *config.Store) *saseauth.Source {
	opt := saseauth.Options{Chrome: f.Chrome, Client: httpClient}
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

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to a tenant in a browser and remember the session",
	Long: `login opens a browser, signs you in to the tenant, and stores the
resulting refresh token so later runs need no browser.

The tenant comes from --tsg or SCM_TSG_ID. Signing in this way is an
alternative to a service account: it needs no client secret, and the token it
produces carries the same tenant scope.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := baseFlags()
		if f.TSGID == "" {
			return fmt.Errorf("set --tsg or SCM_TSG_ID to choose the tenant to sign in to")
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()

		httpClient := &http.Client{Timeout: f.HTTPTimeout}
		src := browserSource(f, httpClient, openStore())

		fmt.Fprintln(cmd.OutOrStdout(), "opening a browser to sign in to tenant", f.TSGID)
		if err := src.SignIn(ctx); err != nil {
			return fmt.Errorf("sign in: %w", err)
		}

		tok, ttl, err := src.Token(ctx)
		if err != nil && !errors.Is(err, saseauth.ErrInteractiveRequired) {
			return fmt.Errorf("verify sign-in: %w", err)
		}
		if tok == "" {
			return fmt.Errorf("signed in but no access token was issued")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "signed in to %s; access token valid for %s\n", f.TSGID, ttl.Round(time.Second))
		if src.RefreshToken() == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "warning: no refresh token was issued, so the next run will need the browser again")
		}
		return nil
	},
}
