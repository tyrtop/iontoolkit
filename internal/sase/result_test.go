package sase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func TestRunElementSuccess(t *testing.T) {
	var gotPrompt string
	f := &fakeClient{
		lookup: func(ctx context.Context, eid string) (api.Element, error) {
			return api.Element{
				ID: eid, Name: "S5032-RTR-01", HWID: "HW1",
				Model: "ion3000", Software: "6.3.1", Connected: true,
			}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			gotPrompt = prompt
			return []ioncli.CommandOutput{{Command: "uptime", Output: "5 days"}}, nil
		},
	}

	got, err := runElement(context.Background(), f, testOpts(), Target{ElementID: "123"})
	if err != nil {
		t.Fatal(err)
	}

	// The contract between result.go:32 and ioncli's prompt matching.
	if gotPrompt != "S5032-RTR-01#" {
		t.Errorf("prompt = %q, want %q", gotPrompt, "S5032-RTR-01#")
	}
	if got.ElementID != "123" || got.Name != "S5032-RTR-01" || !got.Connected {
		t.Errorf("result = %+v", got)
	}
	if len(got.Commands) != 1 || got.Commands[0].Output != "5 days" {
		t.Errorf("commands = %+v", got.Commands)
	}
}

func TestRunElementLookupError(t *testing.T) {
	want := errors.New("status 404")
	f := &fakeClient{
		lookup: func(context.Context, string) (api.Element, error) {
			return api.Element{}, want
		},
		// session deliberately nil: it must not be reached
	}

	_, err := runElement(context.Background(), f, testOpts(), Target{ElementID: "123"})
	// result.go:29 returns the error unwrapped, unlike the session path below.
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestRunElementRetriesThenSucceeds(t *testing.T) {
	o := testOpts()
	o.Attempts = 3
	o.Verbose = true // exercises the per-attempt log at result.go:43

	calls := 0
	f := &fakeClient{
		lookup: func(context.Context, string) (api.Element, error) {
			return api.Element{Name: "ion1"}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			calls++
			if calls < 3 {
				return nil, errors.New("hung session")
			}
			return []ioncli.CommandOutput{{Command: "uptime"}}, nil
		},
	}

	if _, err := runElement(context.Background(), f, o, Target{ElementID: "123"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("session calls = %d, want 3", calls)
	}
}

func TestRunElementAllAttemptsFail(t *testing.T) {
	o := testOpts()
	o.Attempts = 2

	sessionErr := errors.New("hung session")
	f := &fakeClient{
		lookup: func(context.Context, string) (api.Element, error) {
			return api.Element{Name: "ion1"}, nil
		},
		session: func(context.Context, string, string, string, string, []string) ([]ioncli.CommandOutput, error) {
			return nil, sessionErr
		},
	}

	_, err := runElement(context.Background(), f, o, Target{ElementID: "123"})
	if !errors.Is(err, sessionErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, sessionErr)
	}
	if !strings.Contains(err.Error(), "element 123: run:") {
		t.Errorf("err = %q, want the element id in the message", err)
	}
}
