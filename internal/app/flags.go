package app

import (
	"fmt"
	"os"
	"time"

	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
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
	RPS            float64
	DialAttempts   int
	DialTimeout    time.Duration
	Verbose        bool
	AllowWrites    bool
	TSGID          string
	PagerOff       string
	PagerPrompts   []string
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
	DialAttempts:   3,
	DialTimeout:    10 * time.Second,
	RPS:            5,
	PagerPrompts:   ioncli.DefaultPagers,
	// Works on an ion 1200-s running 6.3.4. "terminal length 0", "no paging"
	// and "set cli pager off" all get rejected there. If a device rejects this
	// one too, the pager scraper handles the run.
	PagerOff: "set paging off",
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
	DialAttempts   int
	DialTimeout    time.Duration
	PagerOff       string
	PagerPrompts   []string
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
		DialAttempts:   Defaults.DialAttempts,
		DialTimeout:    Defaults.DialTimeout,
		PagerOff:       Defaults.PagerOff,
		PagerPrompts:   Defaults.PagerPrompts,
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
	return f.ValidateRun()
}

// ValidateRun checks everything but the credentials, for a run whose targets
// carry their own: a --search can span tenants, and each one signs in for
// itself.
func (f Flags) ValidateRun() error {
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
		Verbose:        f.Verbose,
	}
}
