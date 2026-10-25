package runtime

import (
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
)

func TestABatchRunCountsOncePerModel(t *testing.T) {
	rt := New()
	rows := []Element{
		{Element: api.Element{ID: "1", Model: "ion 1200"}},
		{Element: api.Element{ID: "3", Model: "ion 1200"}},
		{Element: api.Element{ID: "2", Model: "ion 3200"}},
	}
	rt.recordUse([]string{"uptime", "dump device status"}, rows)
	if len(rt.History()) != 4 {
		t.Fatalf("history = %+v, want two commands on each of two models", rt.History())
	}
	for _, u := range rt.History() {
		if u.Uses != 1 {
			t.Errorf("%q on %s has %d uses, want 1 for the whole run", u.Command, u.Model, u.Uses)
		}
	}
}
