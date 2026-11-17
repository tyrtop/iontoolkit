package app

import (
	"fmt"
	"os"
	"path/filepath"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
)

var FixturesFile string

func FixturesPath(st *config.Store) string {
	if FixturesFile != "" {
		return FixturesFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, fixture.FileName)
}

func FakeFleet(st *config.Store) (string, *fixture.Set, error) {
	if FixturesFile != "" {
		set, err := fixture.Load(FixturesFile)
		return FixturesFile, set, err
	}
	if path := FixturesPath(st); path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			set, perr := fixture.Parse(b, filepath.Base(path))
			return path, set, perr
		}
		if !os.IsNotExist(err) {
			return path, nil, err
		}
	}
	set, err := fixture.Tutorial()
	return "the fleet iontk ships", set, err
}

func UseFixtures(from string, set *fixture.Set) ([]string, error) {
	Fixture, LaunchedFake = set, from
	lines := []string{fmt.Sprintf("fixtures: %s — no network, no saved state", from)}
	for _, t := range set.Tenants {
		lines = append(lines, fmt.Sprintf("fixtures: %s (tsg %s), %d elements", t.Name, t.TSGID, len(t.Elements)))
		if !Defaults.Verbose {
			continue
		}
		for _, el := range t.Elements {
			lines = append(lines, fmt.Sprintf("fixtures:   %s  %s", el.ID, el.Name))
		}
	}
	for _, k := range []string{"ION_USER", "ION_PASS", "SCM_TOKEN", "SCM_CLIENT_ID", "SCM_CLIENT_SECRET", "SCM_TSG_ID"} {
		if os.Getenv(k) == "" {
			continue
		}
		if err := os.Unsetenv(k); err != nil {
			return lines, fmt.Errorf("unset %s for the fixture run: %w", k, err)
		}
		lines = append(lines, "fixtures: ignoring "+k)
	}
	return lines, nil
}
