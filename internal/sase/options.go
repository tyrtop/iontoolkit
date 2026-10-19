package sase

import (
	"fmt"
	"time"
)

type Options struct {
	Commands       []string
	Username       string
	Password       string
	Concurrency    int
	ElementTimeout time.Duration
	SessionTimeout time.Duration
	Verbose        bool
}

func (o Options) Validate() error {
	if len(o.Commands) > 0 {
		if o.Username == "" || o.Password == "" {
			// The login can come from the tenant's keyring entry now, so the
			// environment isn't the only thing worth suggesting.
			return fmt.Errorf("--cmd needs a device login: set ION_USER and ION_PASS, or save one for the tenant in iontk")
		}
	}
	return o.validateRest()
}

// ValidateFor is Validate for a run whose targets carry their own logins,
// which is what lets one run span tenants. The element is named, because with
// several tenants in play "no device login" does not say which one is missing.
func (o Options) ValidateFor(targets []Target) error {
	if len(o.Commands) > 0 {
		for _, t := range targets {
			if u, p := t.creds(o); u == "" || p == "" {
				return fmt.Errorf("element %s has no device login: save one for its tenant in iontk, or set ION_USER and ION_PASS", t.ElementID)
			}
		}
	}
	return o.validateRest()
}

func (o Options) validateRest() error {
	if o.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if o.SessionTimeout > o.ElementTimeout {
		return fmt.Errorf("session timeout cannot be longer than element timeout")
	}
	return nil
}
