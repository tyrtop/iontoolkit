package ui

import "testing"

// A theme names its spinner, so a name that is not one has to be catchable at
// load: SpinnerName itself falls back to the default, which in a config file
// would look like the name being ignored.
func TestSpinnerNamesAreCheckable(t *testing.T) {
	if got := SpinnerName("nonsense"); got != DefaultSpinner {
		t.Errorf("unknown name = %q, want %q", got, DefaultSpinner)
	}
	if KnownSpinner("nonsense") {
		t.Error("KnownSpinner() accepted a shape that does not exist")
	}
	if !KnownSpinner(DefaultSpinner) {
		t.Errorf("KnownSpinner(%q) = false", DefaultSpinner)
	}

	names := SpinnerNames()
	if len(names) != len(spinnerChoices) {
		t.Fatalf("SpinnerNames() = %d, want one per shape", len(names))
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("two shapes are called %q", n)
		}
		seen[n] = true
		if !KnownSpinner(n) {
			t.Errorf("SpinnerNames() offered %q, which KnownSpinner rejects", n)
		}
	}
}
