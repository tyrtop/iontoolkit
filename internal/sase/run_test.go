package sase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
			time.Sleep(2 * time.Millisecond)
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

func TestTargetCredsPreferTheirOwn(t *testing.T) {
	o := Options{Username: "fallback-user", Password: "fallback-pass"}

	tests := []struct {
		name             string
		target           Target
		wantUser, wantPw string
	}{
		{
			name:     "a target with its own login uses it",
			target:   Target{ElementID: "1", User: "tenant-user", Pass: "tenant-pass"},
			wantUser: "tenant-user", wantPw: "tenant-pass",
		},
		{
			name:     "a target with none falls back to the options",
			target:   Target{ElementID: "1"},
			wantUser: "fallback-user", wantPw: "fallback-pass",
		},
		{
			name:     "a half-filled target fills the rest from the options",
			target:   Target{ElementID: "1", User: "tenant-user"},
			wantUser: "tenant-user", wantPw: "fallback-pass",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, pass := tt.target.creds(o)
			if user != tt.wantUser || pass != tt.wantPw {
				t.Errorf("creds() = %q/%q, want %q/%q", user, pass, tt.wantUser, tt.wantPw)
			}
		})
	}
}

type credRecorder struct {
	mu   sync.Mutex
	seen map[string][2]string
}

func (c *credRecorder) LookupElement(ctx context.Context, eid string) (api.Element, error) {
	return api.Element{ID: eid, Name: "e" + eid}, nil
}

func (c *credRecorder) ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string][2]string{}
	}
	c.seen[eid] = [2]string{user, pass}
	return []ioncli.CommandOutput{{Command: cmds[0], Output: "ok"}}, nil
}

func TestRunStreamDialsEachElementWithItsOwnLogin(t *testing.T) {
	rec := &credRecorder{}
	o := testOpts()
	o.Commands = []string{"dump device status"}
	o.Username, o.Password = "should-not-be-used", "should-not-be-used"

	targets := []Target{
		{ElementID: "1", Client: rec, User: "tsc-ops", Pass: "pw-tsc"},
		{ElementID: "2", Client: rec, User: "nb-ops", Pass: "pw-nb"},
	}

	for ev := range RunStream(context.Background(), o, targets) {
		if ev.Done && ev.Result.Error != "" {
			t.Fatalf("element %s failed: %s", ev.Element, ev.Result.Error)
		}
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := rec.seen["1"]; got != [2]string{"tsc-ops", "pw-tsc"} {
		t.Errorf("element 1 dialled with %v, want its own tenant's login", got)
	}
	if got := rec.seen["2"]; got != [2]string{"nb-ops", "pw-nb"} {
		t.Errorf("element 2 dialled with %v, want its own tenant's login", got)
	}
}

type deadCredClient struct {
	fakeClient
	dead    atomic.Bool
	lookups atomic.Int32
	dials   atomic.Int32
}

func newDeadCredClient() *deadCredClient {
	c := &deadCredClient{}
	c.lookup = func(ctx context.Context, eid string) (api.Element, error) {
		c.lookups.Add(1)
		return api.Element{ID: eid, Name: "ion-" + eid}, nil
	}
	c.session = func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
		c.dials.Add(1)
		return nil, errors.New("expected handshake response status code 101 but got 401")
	}
	return c
}

func (c *deadCredClient) RefreshStopped() bool { return c.dead.Load() }

func TestARunStopsReachingOutOnceTheCredentialIsSpent(t *testing.T) {
	c := newDeadCredClient()
	c.dead.Store(true)

	elements := []string{"1", "2", "3", "4", "5"}
	var results []Result
	for ev := range RunStream(context.Background(), testOpts(), Targets(c, elements)) {
		if ev.Done {
			results = append(results, ev.Result)
		}
	}

	if len(results) != len(elements) {
		t.Fatalf("got %d results, want one per element", len(results))
	}
	for _, r := range results {
		if !strings.Contains(r.Error, "sign-in expired") {
			t.Errorf("result error = %q, want it to name the expired sign-in", r.Error)
		}
	}
	if got := c.lookups.Load(); got != 0 {
		t.Errorf("%d element lookups were attempted, want 0 on a spent credential", got)
	}
	if got := c.dials.Load(); got != 0 {
		t.Errorf("%d sessions were dialled, want 0 on a spent credential", got)
	}
}

func TestAHealthyCredentialRunsNormally(t *testing.T) {
	c := newDeadCredClient()
	c.session = func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
		c.dials.Add(1)
		return []ioncli.CommandOutput{{Command: "uptime", Output: "up 1 day"}}, nil
	}

	for ev := range RunStream(context.Background(), testOpts(), Targets(c, []string{"1", "2"})) {
		if ev.Done && ev.Result.Error != "" {
			t.Errorf("element %s failed: %s", ev.Element, ev.Result.Error)
		}
	}
	if got := c.dials.Load(); got != 2 {
		t.Errorf("%d sessions dialled, want 2", got)
	}
}

func TestAClientThatCannotSaySoIsAssumedGood(t *testing.T) {
	var plain Client = &fakeClient{
		lookup: func(ctx context.Context, eid string) (api.Element, error) {
			return api.Element{ID: eid}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			return []ioncli.CommandOutput{{Command: "uptime", Output: "ok"}}, nil
		},
	}
	if credentialDead(plain) {
		t.Error("credentialDead() = true for a client that cannot report, want it assumed good")
	}
	res := Run(context.Background(), plain, testOpts(), []string{"1"})
	if len(res) != 1 || res[0].Error != "" {
		t.Errorf("results = %+v, want the run to proceed", res)
	}
}
