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
	}

	_, err := runElement(context.Background(), f, testOpts(), Target{ElementID: "123"})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestRunElementDoesNotRerunASessionThatConnected(t *testing.T) {
	o := testOpts()

	calls := 0
	f := &fakeClient{
		lookup: func(context.Context, string) (api.Element, error) {
			return api.Element{Name: "ion1"}, nil
		},
		session: func(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
			calls++
			return nil, errors.New("hung session")
		},
	}

	if _, err := runElement(context.Background(), f, o, Target{ElementID: "123"}); err == nil {
		t.Fatal("runElement() error = nil, want the failure reported")
	}
	if calls != 1 {
		t.Errorf("session calls = %d, want 1", calls)
	}
}

func TestRunElementReportsAFailedSession(t *testing.T) {
	o := testOpts()

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

func TestAFailedSessionKeepsTheIONsStatusAndWhatRan(t *testing.T) {
	f := &fakeClient{
		lookup: func(context.Context, string) (api.Element, error) {
			return api.Element{Name: "ion1", Model: "ion 1200-s", Software: "6.4.1", Connected: true}, nil
		},
		session: func(context.Context, string, string, string, string, []string) ([]ioncli.CommandOutput, error) {
			return []ioncli.CommandOutput{{Command: "uptime", Output: "up 3 days"}}, errors.New(`read after "dump device status": EOF`)
		},
	}
	var got Result
	for ev := range RunStream(context.Background(), testOpts(), []Target{{ElementID: "123", Client: f, User: "u", Pass: "p"}}) {
		if ev.Done {
			got = ev.Result
		}
	}
	if got.Error == "" || got.ElementID != "123" || got.Name != "ion1" || got.Model != "ion 1200-s" || !got.Connected || len(got.Commands) != 1 {
		t.Errorf("result = %+v, want the error with the ION's status and what ran before it", got)
	}
}
