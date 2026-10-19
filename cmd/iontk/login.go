package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in with a browser and store a credential for every tenant",
	Long: `Opens a browser, signs in to the account, and then gives each tenant in
the working set a credential of its own.

The per-tenant credentials are what make this stick: each one is renewed and
re-stored every time it is used, so ordinary use keeps them alive. The account
credential alone cannot do that -- the server rotates it as it is redeemed, so
it only ever covers one tenant.

--search offers the same thing when it finds an expired credential, so this is
only needed to renew ahead of time or to sign in for a tenant nothing has
asked for yet. --tenant does one tenant instead of the working set.`,
	Example: `  iontk login
  iontk login --tenant Synovus`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogin(cmd.Context())
	},
}

func runLogin(ctx context.Context) error {
	st := app.OpenStore()
	if st == nil {
		return fmt.Errorf("login needs the config store, which could not be opened")
	}
	tenants, err := searchScope(st)
	if err != nil {
		return err
	}
	if _, err := app.SignIn(ctx, st, os.Stderr); err != nil {
		return err
	}
	return app.WarmTenants(ctx, st, tenants, os.Stderr)
}
