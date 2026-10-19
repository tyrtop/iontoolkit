package main

import (
	"fmt"
	"io"

	"tyrtop.com/iontk/internal/app"
)

// Batch and shell both go through here, so they can't end up using different
// tenants.
func resolveFlags(w io.Writer) (app.Flags, error) {
	creds, err := app.ResolveCredentials(app.OpenStore(), flagTenant)
	if err != nil {
		return app.Flags{}, err
	}
	for _, line := range creds.Lines() {
		fmt.Fprintln(w, line)
	}
	return creds.Flags, nil
}
