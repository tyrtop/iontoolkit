package app

import (
	"fmt"
	"os"
	"time"

	"tyrtop.com/iontk/internal/sase"
)

// RootFlags are the knobs the root command binds its command-line flags to.
// They live here, not in the command, because the TUI reads the same numbers
// without ever having parsed a flag.
type RootFlags struct {
	HTTPTimeout    time.Duration
	ElementTimeout time.Duration
	SessionTimeout time.Duration
	RefreshMargin  time.Duration
	Concurrency    int
	Burst          int
	Attempts       int
	RPS            float64
	Verbose        bool
	AllowWrites    bool
	TSGID          string
}

// Defaults doubles as the flag defaults the root command declares, so the
// numbers are stated once and a run that never parsed a flag still works.
var Defaults = RootFlags{
	HTTPTimeout:    15 * time.Second,
	ElementTimeout: 60 * time.Second,
	SessionTimeout: 20 * time.Second,
	RefreshMargin:  75 * time.Second,
	Concurrency:    10,
	Burst:          10,
	Attempts:       1,
	RPS:            5,
}

type Flags struct {
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
	BrowserAuth    bool
}

func BaseFlags() Flags {
	f := Flags{
		Token:          os.Getenv("SCM_TOKEN"),
		ClientID:       os.Getenv("SCM_CLIENT_ID"),
		ClientSecret:   os.Getenv("SCM_CLIENT_SECRET"),
		TSGID:          os.Getenv("SCM_TSG_ID"),
		IONUsername:    os.Getenv("ION_USER"),
		IONPassword:    os.Getenv("ION_PASS"),
		HTTPTimeout:    Defaults.HTTPTimeout,
		Verbose:        Defaults.Verbose,
		RPS:            Defaults.RPS,
		Burst:          Defaults.Burst,
		RefreshMargin:  Defaults.RefreshMargin,
		Concurrency:    Defaults.Concurrency,
		ElementTimeout: Defaults.ElementTimeout,
		SessionTimeout: Defaults.SessionTimeout,
		Attempts:       Defaults.Attempts,
	}
	if Defaults.TSGID != "" {
		f.TSGID = Defaults.TSGID
	}
	return f
}

func (f Flags) ValidateCreds() error {
	if f.Token != "" {
		return nil
	}
	if f.ClientID != "" && f.ClientSecret != "" && f.TSGID != "" {
		return nil
	}
	// Browser authentication carries the tenant in the token scope, so the
	// tenant is the only thing it still needs up front.
	if f.BrowserAuth && f.TSGID != "" {
		return nil
	}
	return fmt.Errorf("set SCM_CLIENT_ID, SCM_CLIENT_SECRET, and SCM_TSG_ID, or SCM_TOKEN to override")
}

func (f Flags) Validate() error {
	if err := f.ValidateCreds(); err != nil {
		return err
	}
	if f.RefreshMargin <= 0 {
		return fmt.Errorf("refresh margin must be greater than 0")
	}
	if len(f.Elements) == 0 {
		return fmt.Errorf("an element is required")
	}
	if len(f.Elements) > 1 && len(f.Commands) == 0 {
		return fmt.Errorf("shell requires exactly one element; use \"iontk run\" for multiple")
	}
	if f.RPS <= 0 {
		return fmt.Errorf("rps must be greater than 0")
	}
	if f.Burst < 1 {
		return fmt.Errorf("burst must be at least 1")
	}
	return nil
}

func (f Flags) Options() sase.Options {
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
