package sase

import (
	"strings"
	"testing"
	"time"
)

func TestOptionsValidate(t *testing.T) {
	base := Options{
		Commands: []string{"dump device status"},
		Username: "u", Password: "p",
		Concurrency:    10,
		ElementTimeout: 60 * time.Second,
		SessionTimeout: 20 * time.Second,
	}

	cases := map[string]struct {
		mutate  func(*Options)
		wantErr bool
	}{
		"defaults": {func(o *Options) {}, false},
		"interactive needs no creds": {func(o *Options) {
			o.Commands, o.Username, o.Password = nil, "", ""
		}, false},
		"cmd without user":  {func(o *Options) { o.Username = "" }, true},
		"cmd without pass":  {func(o *Options) { o.Password = "" }, true},
		"zero concurrency":  {func(o *Options) { o.Concurrency = 0 }, true},
		"session > element": {func(o *Options) { o.SessionTimeout = 90 * time.Second }, true},
		"session == element": {func(o *Options) {
			o.SessionTimeout = o.ElementTimeout
		}, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			o := base
			tc.mutate(&o)
			err := o.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateForAcceptsATargetsOwnLogin(t *testing.T) {
	// A cross-tenant run has no single login in the Options: each target
	// carries the one its own tenant saved.
	base := Options{
		Commands:       []string{"dump device status"},
		Concurrency:    10,
		ElementTimeout: 60 * time.Second,
		SessionTimeout: 20 * time.Second,
	}

	cases := map[string]struct {
		opts    Options
		targets []Target
		wantErr string
	}{
		"every target brings its own": {base, []Target{
			{ElementID: "e1", User: "a", Pass: "1"},
			{ElementID: "e2", User: "b", Pass: "2"},
		}, ""},
		"one target is missing a login": {base, []Target{
			{ElementID: "e1", User: "a", Pass: "1"},
			{ElementID: "e2"},
		}, "e2"},
		"a half login is no login": {base, []Target{
			{ElementID: "e3", User: "a"},
		}, "e3"},
		"the options still fill a gap": {
			Options{Commands: base.Commands, Username: "u", Password: "p",
				Concurrency: 10, ElementTimeout: 60 * time.Second, SessionTimeout: 20 * time.Second},
			[]Target{{ElementID: "e1"}}, "",
		},
		"no commands means no login is needed": {
			Options{Concurrency: 10, ElementTimeout: 60 * time.Second, SessionTimeout: 20 * time.Second},
			[]Target{{ElementID: "e1"}}, "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.opts.ValidateFor(tc.targets)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateFor() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateFor() = nil, want an error naming %q", tc.wantErr)
			}
			// With several tenants in play, "no device login" does not say
			// which element is the problem.
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateFor() = %v, want it to name %q", err, tc.wantErr)
			}
		})
	}
}
