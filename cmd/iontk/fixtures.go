package main

import (
	"fmt"
	"io"
	"os"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/fixture"
)

// loadFixtures runs before every command, so --fixtures works on the root and
// on the subcommands alike.
func loadFixtures(w io.Writer) error {
	if flagFixtures == "" {
		return nil
	}
	set, err := fixture.Load(flagFixtures)
	if err != nil {
		return err
	}
	app.Fixture = set

	// Said out loud, because a fixture run that looked like a real one would be
	// the worst possible confusion in a tool that writes to appliances.
	fmt.Fprintf(w, "fixtures: %s — no network, no saved state\n", flagFixtures)
	for _, t := range set.Tenants {
		fmt.Fprintf(w, "fixtures: %s (tsg %s), %d elements\n", t.Name, t.TSGID, len(t.Elements))
		// IDs are generated from the names unless the file gives them, and
		// --element needs one, so -v is how you find out what they are.
		if !app.Defaults.Verbose {
			continue
		}
		for _, el := range t.Elements {
			fmt.Fprintf(w, "fixtures:   %s  %s\n", el.ID, el.Name)
		}
	}

	// The environment would otherwise win over the fixture's own logins and
	// token, and every session would answer "Login incorrect".
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
