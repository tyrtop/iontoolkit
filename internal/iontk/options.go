package iontk

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
	Attempts       int
	Verbose        bool
}

func (o Options) Validate() error {
	if len(o.Commands) > 0 {
		if o.Username == "" || o.Password == "" {
			return fmt.Errorf("ION_USER and ION_PASS must be set when using -cmd")
		}
	}
	if o.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if o.SessionTimeout > o.ElementTimeout {
		return fmt.Errorf("session timeout cannot be longer than element timeout")
	}
	if o.Attempts <= 0 {
		return fmt.Errorf("attempts must be greater than 0")
	}
	if o.Attempts*int(o.SessionTimeout) > int(o.ElementTimeout) {
		return fmt.Errorf("attempts * session timeout must be less than element timeout")
	}
	return nil
}
