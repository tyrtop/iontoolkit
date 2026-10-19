package main

import (
	"fmt"
	"io"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

// Batch and shell both go through here, so they can't end up using different
// tenants.
func resolveFlags(w io.Writer) (app.Flags, error) {
	st := app.OpenStore()
	app.ApplyDisplaySettings(st, nil)

	creds, err := app.ResolveCredentials(st, flagTenant)
	if err != nil {
		return app.Flags{}, err
	}
	for _, line := range creds.Lines() {
		fmt.Fprintln(w, line)
	}
	// A themes.yaml that would not parse is as worth saying here as a wrong
	// tenant is: the alternative is a scheme that silently did not load.
	if err := ui.ThemesErr(); err != "" {
		fmt.Fprintln(w, "themes: "+err)
	}
	return creds.Flags, nil
}
