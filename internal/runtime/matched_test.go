package runtime

import (
	"fmt"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
)

func someElements(n int) []Element {
	out := make([]Element, n)
	for i := range out {
		out[i] = Element{Element: api.Element{ID: fmt.Sprint(i), Name: fmt.Sprintf("S%04d-RTR-01", i)}, TSG: "t1"}
	}
	return out
}

func TestAMatchFromAReplacedListNeverReachesPastTheNewOne(t *testing.T) {
	rt := New()
	rt.SetElements(someElements(3))
	if got := len(rt.MatchedElements()); got != 3 {
		t.Fatalf("%d matched, want 3", got)
	}
	rt.SetElements(someElements(1))
	if _, ok := rt.MatchedAt(2); ok {
		t.Error("a position from the old list still answered")
	}
	if el, ok := rt.MatchedAt(0); !ok || el.ID != "0" {
		t.Errorf("MatchedAt(0) = %v, %v", el.ID, ok)
	}
	if got := rt.MatchedElements(); len(got) != 1 || got[0].ID != "0" {
		t.Errorf("matched %v", got)
	}
}

func TestReadingMatchesWhileAnotherTenantReplacesTheListNeverPanics(t *testing.T) {
	rt := New()
	rt.SetElements(someElements(50))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 400 {
			rt.SetElements(someElements(10 + 40*(i%2)))
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
			for _, el := range rt.MatchedElements() {
				_ = el.ID
			}
			rt.MatchedAt(45)
		}
	}
}
