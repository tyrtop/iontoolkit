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
	if o.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if o.SessionTimeout > o.ElementTimeout {
		return fmt.Errorf("session timeout cannot be longer than element timeout")
	}
	return nil
}
