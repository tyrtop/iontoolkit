package tui

import "testing"

func TestTheShippedMenuParses(t *testing.T) {
	entries, err := builtinMenu()
	if err != nil || len(entries) == 0 {
		t.Fatalf("menu.yaml: %v (%d entries)", err, len(entries))
	}
}
