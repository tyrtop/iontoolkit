package main

import (
	"fmt"
	"io"
	"os"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/fixture"
)

func loadFixtures(w io.Writer) error {
	if flagFixtures == "" {
		return nil
	}
	set, err := fixture.Load(flagFixtures)
	if err != nil {
		return err
	}
	app.Fixture = set

	fmt.Fprintf(w, "fixtures: %s — no network, no saved state\n", flagFixtures)
	for _, t := range set.Tenants {
		fmt.Fprintf(w, "fixtures: %s (tsg %s), %d elements\n", t.Name, t.TSGID, len(t.Elements))
		if !app.Defaults.Verbose {
			continue
		}
		for _, el := range t.Elements {
			fmt.Fprintf(w, "fixtures:   %s  %s\n", el.ID, el.Name)
		}
	}

	for _, k := range []string{"ION_USER", "ION_PASS", "SCM_TOKEN", "SCM_CLIENT_ID", "SCM_CLIENT_SECRET", "SCM_TSG_ID"} {
		if os.Getenv(k) == "" {
			continue
		}
		if err := os.Unsetenv(k); err != nil {
			return fmt.Errorf("unset %s for the fixture run: %w", k, err)
		}
		fmt.Fprintf(w, "fixtures: ignoring %s\n", k)
	}
	return nil
}
