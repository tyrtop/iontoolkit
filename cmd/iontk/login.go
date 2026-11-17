package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/runtime"
)

var loginCmd = deprecated(&cobra.Command{
	Use:   "login",
	Short: "Sign in with a browser and store a credential for every tenant",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogin(cmd.Context())
	},
}, "iontk auth login")

func runLogin(ctx context.Context) error {
	rt := cliRuntime()
	if rt.Store() == nil {
		return fmt.Errorf("login needs the config store, which could not be opened")
	}
	tenants, err := rt.SearchScope(flagTenant, app.Defaults.TSGID)
	if err != nil {
		return err
	}
	names := make([]string, len(tenants))
	for i, t := range tenants {
		names[i] = t.Name
	}
	say := func(l string) { fmt.Fprintln(os.Stderr, l) }
	return rt.Exec(&runtime.Call{Ctx: ctx, Out: say, Args: map[string]string{"tenants": strings.Join(names, ",")}}, "auth.login")
}

func deprecated(c *cobra.Command, now string) *cobra.Command {
	c.Deprecated = "use `" + now + "`"
	return c
}
