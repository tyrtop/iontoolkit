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
			return fmt.Errorf("ION_USER and ION_PASS must be set when using --cmd")
		}
	}
	if o.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if o.SessionTimeout > o.ElementTimeout {
		return fmt.Errorf("session timeout cannot be longer than element timeout")
	}
	if o.SessionTimeout > o.ElementTimeout {
		return fmt.Errorf("session timeout must not exceed element timeout")
	}
	return nil
}
