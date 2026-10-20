package main

import (
	"fmt"
	"io"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

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
	if err := ui.ThemesErr(); err != "" {
		fmt.Fprintln(w, "themes: "+err)
	}
	return creds.Flags, nil
}
