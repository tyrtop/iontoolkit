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
	Timeout time.Duration
	Verbose bool
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

	return nil
}
