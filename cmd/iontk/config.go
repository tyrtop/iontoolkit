package main

import (
	"fmt"
	"time"

	"tyrtop.com/iontk/internal/sase"
)

type flags struct {
	Token          string
	Commands       []string
	IONUsername    string
	IONPassword    string
	Elements       []string
	HTTPTimeout    time.Duration
	Verbose        bool
	ElementTimeout time.Duration
	Concurrency    int
	RPS            float64
	Burst          int
	SessionTimeout time.Duration
	Attempts       int
	ClientID       string
	ClientSecret   string
	TSGID          string
	RefreshMargin  time.Duration
}

func (f flags) Validate() error {
	if f.Token == "" {
		if f.ClientID == "" || f.ClientSecret == "" || f.TSGID == "" {
			return fmt.Errorf("set SCM_CLIENT_ID, SCM_CLIENT_SECRET, and SCM_TSG_ID, or SCM_TOKEN to override")
		}
	}
	if f.RefreshMargin <= 0 {
		return fmt.Errorf("refresh margin must be greater than 0")
	}
	if len(f.Elements) == 0 {
		return fmt.Errorf("an element is required")
	}
	if len(f.Elements) > 1 && len(f.Commands) == 0 {
		return fmt.Errorf("interactive mode requires exactly one element; use -cmd to run across multiple")
	}
	if f.RPS <= 0 {
		return fmt.Errorf("rps must be greater than 0")
	}
	if f.Burst < 1 {
		return fmt.Errorf("burst must be at least 1")
	}
	return nil
}

func (f flags) options() sase.Options {
	return sase.Options{
		Commands:       f.Commands,
		Username:       f.IONUsername,
		Password:       f.IONPassword,
		Concurrency:    f.Concurrency,
		ElementTimeout: f.ElementTimeout,
		SessionTimeout: f.SessionTimeout,
		Attempts:       f.Attempts,
		Verbose:        f.Verbose,
	}
}
