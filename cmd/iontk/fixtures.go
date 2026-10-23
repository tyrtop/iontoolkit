package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/tui"
)

func loadFixtures(w io.Writer) error {
	if flagFixtures == "" && !flagDemo {
		return nil
	}

	from, set, err := fakeFleet()
	if err != nil {
		return err
	}
	app.Fixture = set

	fmt.Fprintf(w, "fixtures: %s — no network, no saved state\n", from)
	for _, t := range set.Tenants {
		fmt.Fprintf(w, "fixtures: %s (tsg %s), %d elements\n", t.Name, t.TSGID, len(t.Elements))
		if !app.Defaults.Verbose {
			continue
		}
		for _, el := range t.Elements {
			fmt.Fprintf(w, "fixtures:   %s  %s\n", el.ID, el.Name)
		}
	}

	app.LaunchedFake = from

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

func fakeFleet() (string, *fixture.Set, error) {
	if flagFixtures != "" {
		set, err := fixture.Load(flagFixtures)
		return flagFixtures, set, err
	}

	path := tui.FixturesPath(app.OpenStore())
	if b, err := os.ReadFile(path); err == nil {
		set, perr := fixture.Parse(b, filepath.Base(path))
		return path, set, perr
	}
	set, err := fixture.Tutorial()
	return "the fleet iontk ships", set, err
}
