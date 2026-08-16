package main

import (
	"time"
	"fmt"
)

type Config struct {
	Token string
	Command string
	IONUsername string
	IONPassword string
	Elements []string
	HTTPTimeout time.Duration
	Verbose bool
	ElementTimeout time.Duration
	Concurancy int
	RPS float64
	Burst int
	SessionTimeout time.Duration
	Attempts int
}

func(c Config) Validate() error{
	if c.Token == "" {
		return fmt.Errorf("SCM_TOKEN not set")
	}
	if c.Command != "" {
		if c.IONUsername == "" ||  c.IONPassword == "" {
			return fmt.Errorf("ION_USER and ION_PASS must be set when using -cmd")
		}
	}
	if len(c.Elements) == 0 {
		return fmt.Errorf("an element is required")
	}
	if len(c.Elements) > 1 && c.Command == "" {
		return fmt.Errorf("interactive mode requires exactly one element; use -cmd to run across multiple")
	}
	if c.Concurancy < 1 {
		return fmt.Errorf("concurrancy must be at least 1")
	}
	if c.RPS <= 0 {
		return fmt.Errorf("rps must be greater than 0")
	}
	if c.Burst < 1 {
		return fmt.Errorf("burst must be at least 1")
	}
	if c.SessionTimeout> c.ElementTimeout{
		return fmt.Errorf("session timeout cannot be longer than element timeout")
	}
	if c.Attempts <= 0 {
		return fmt.Errorf("attempts must be greater than 0")
	}
	if c.Attempts * int(c.SessionTimeout) > int(c.ElementTimeout) {
		return fmt.Errorf("attempts * session timeout must be less than element timeout")
	}


	return nil
}
