package sase

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func TestRunPreservesOrderAndRecordsErrors(t *testing.T) {
	o := testOpts()
	o.Concurrency = 4

	elements := make([]string, 50)
	for i := range elements {
		elements[i] = fmt.Sprintf("e%02d", i)
	}

	f := &fakeClient{
		lookup: func(ctx context.Context, eid string) (api.Element, error) {
			if eid == "e07" {
				return api.Element{}, errors.New("not found")
			}
			return api.Element{ID: eid, Name: eid}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			return []ioncli.CommandOutput{{Command: "uptime", Output: eid}}, nil
		},
	}

	results := Run(context.Background(), f, o, elements)

	if len(results) != len(elements) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(elements))
	}
	// results[i] must correspond to elements[i] regardless of completion order.
	for i, r := range results {
		if r.ElementID != elements[i] {
			t.Fatalf("results[%d].ElementID = %q, want %q", i, r.ElementID, elements[i])
		}
	}
	if results[7].Error == "" {
		t.Error("results[7] should carry an error")
	}
	for i, r := range results {
		if i != 7 && r.Error != "" {
			t.Errorf("results[%d] unexpected error: %s", i, r.Error)
		}
	}
}

func TestRunRespectsConcurrency(t *testing.T) {
	o := testOpts()
	o.Concurrency = 3

	var inFlight, maxInFlight atomic.Int32
	f := &fakeClient{
		lookup: func(ctx context.Context, eid string) (api.Element, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond) // hold the slot so workers overlap
			return api.Element{Name: eid}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			return nil, nil
		},
	}

	Run(context.Background(), f, o, []string{"a", "b", "c", "d", "e", "f", "g", "h"})

	if got := maxInFlight.Load(); got > 3 {
		t.Errorf("max concurrent elements = %d, want <= 3", got)
	}
}
