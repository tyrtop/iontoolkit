package ui

import "testing"

func TestSpinnerCycle(t *testing.T) {
	if got := SpinnerName("nonsense"); got != DefaultSpinner {
		t.Errorf("unknown name = %q, want %q", got, DefaultSpinner)
	}
	seen := map[string]bool{}
	name := DefaultSpinner
	for range spinnerChoices {
		if seen[name] {
			t.Fatalf("cycle repeated %q early", name)
		}
		seen[name] = true
		name = NextSpinnerName(name)
	}
	if name != DefaultSpinner {
		t.Errorf("cycle ended on %q, want to wrap to %q", name, DefaultSpinner)
	}
}
