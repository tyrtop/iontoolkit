package tui

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func resetModel(t *testing.T) tuiModel {
	t.Helper()
	m := pickModel(t)
	m.width, m.height = 100, 30
	// pickModel keeps its config in memory; the reset is about what is on disk.
	if err := m.store.SaveConfig(m.cfg); err != nil {
		t.Fatal(err)
	}
	m.phase = phaseSettings
	m.setSection = secReset
	m.setFocus = focusContent
	return m
}

func TestFactoryResetAsksTwice(t *testing.T) {
	m := resetModel(t)

	armed, _ := m.onSettingsKey("enter")
	if !armed.(tuiModel).resetArmed {
		t.Fatal("enter did not arm the reset")
	}
	if got := plain(armed.(tuiModel).viewSettings()); !strings.Contains(got, "press y to delete") {
		t.Errorf("the armed section does not say what y will do:\n%s", got)
	}

	// Still saved: arming is not the reset.
	if cfg, err := m.store.Config(); err != nil || len(cfg.Tenants) == 0 {
		t.Errorf("arming already deleted the config (%+v, %v)", cfg, err)
	}

	done, _ := armed.(tuiModel).onSettingsKey("y")
	got := done.(tuiModel)
	if got.hasTenant || len(got.cfg.Tenants) != 0 || len(got.all) != 0 {
		t.Errorf("the model still has a tenant after the reset: %+v", got.cfg)
	}
	if got.phase != phaseMenu {
		t.Errorf("phase = %v after the reset, want the menu", got.phase)
	}
	cfg, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tenants) != 0 {
		t.Errorf("config still holds %d tenants", len(cfg.Tenants))
	}
	if _, err := m.store.Secrets.Get(config.IONPassKey("1", "u")); err == nil {
		t.Error("the ION password survived the reset")
	}
}

func TestFactoryResetCancelsOnAnythingElse(t *testing.T) {
	for _, key := range []string{"n", "esc", "q", "enter"} {
		m := resetModel(t)
		armed, _ := m.onSettingsKey("enter")

		after, _ := armed.(tuiModel).onSettingsKey(key)
		if after.(tuiModel).resetArmed {
			t.Errorf("%q left the reset armed", key)
		}
		cfg, err := m.store.Config()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Tenants) == 0 {
			t.Errorf("%q reset anyway", key)
		}
	}
}

func TestArmingDoesNotSurviveLeavingTheSection(t *testing.T) {
	m := resetModel(t)
	armed, _ := m.onSettingsKey("enter")

	// Walking away and back must ask again, or a y typed at the General
	// section's spinner row would land on a disarmed-looking reset.
	away, _ := armed.(tuiModel).onSettingsKey("shift+tab")
	back, _ := away.(tuiModel).onSettingsKey("tab")
	if back.(tuiModel).resetArmed {
		t.Error("the reset was still armed after leaving the section and coming back")
	}

	fired, _ := back.(tuiModel).onSettingsKey("y")
	cfg, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tenants) == 0 {
		t.Fatal("y reset without having been asked")
	}
	if fired.(tuiModel).resetArmed {
		t.Error("y armed the reset; only enter does that")
	}
}

func TestResetSectionIsLast(t *testing.T) {
	// It is the one thing on this page that cannot be undone, so it does not
	// sit next to the rows someone is cycling through.
	if setSections[len(setSections)-1] != "Reset" {
		t.Errorf("sections end with %q, want Reset at the bottom", setSections[len(setSections)-1])
	}
}
