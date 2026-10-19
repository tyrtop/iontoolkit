package app

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

var _ api.TokenSource = (*saseauth.Source)(nil)

// BrowserOptions builds the browser settings for a store. The profile directory
// is the important part: the session cookie it holds is what lets later tenants
// be scoped without a second window.
func BrowserOptions(httpClient *http.Client, st *config.Store) saseauth.Options {
	opt := saseauth.Options{Client: httpClient}
	if st != nil {
		opt.DataDir = st.BrowserDir()
	}
	return opt
}

// BrowserSource builds a browser-backed token source for the tenant in f. A nil
// store is not fatal: the sign-in still works, it just will not be remembered
// for the next run.
//
// Nothing here opens a browser. Source.Token only spends credentials it already
// has, so a batch run against a browser tenant either works from the stored
// refresh token or fails asking to be signed in from the TUI, which is the only
// place a window is ever shown.
func BrowserSource(f Flags, httpClient *http.Client, st *config.Store) *saseauth.Source {
	opt := BrowserOptions(httpClient, st)
	if st == nil {
		return saseauth.NewSource(f.TSGID, "", opt, nil)
	}

	stored, _ := st.Secrets.Get(config.RefreshTokenKey(f.TSGID))
	// Narrowing the account token's scope is what makes a new tenant usable
	// without another trip through the browser. It goes in as a shared seed,
	// not this tenant's own: one refresh token redeemed from several tenants
	// at once loses all but one. Attached even when this tenant has a stored
	// token, because that token may be the thing the server rejects -- a
	// tenant whose credential expired is exactly who the account one is for.
	if acct, _ := st.Secrets.Get(config.AccountRefreshTokenKey()); acct != "" {
		opt.Seed = accountSeed(acct)
	}
	save := func(rt string) error {
		return st.Secrets.Set(config.RefreshTokenKey(f.TSGID), rt)
	}
	return saseauth.NewSource(f.TSGID, stored, opt, save)
}

// seeds is process-wide because a client is built per tenant, each opening its
// own store: there is nowhere else for tenants to meet. Keyed by token, so a
// fresh sign-in gets a fresh seed.
var seeds sync.Map

func accountSeed(token string) *saseauth.Seed {
	v, _ := seeds.LoadOrStore(token, saseauth.NewSeed(token))
	return v.(*saseauth.Seed)
}

// Debug sends every store this process opens to a throwaway root with
// in-memory secrets. It is a process-wide switch because the TUI, the batch
// path and BrowserSource each open their own store, and a run with one of them
// still pointed at the real config would not be isolated at all.
var Debug bool

// Fixture is the loaded --fixtures file, or nil for a real run. It isolates the
// store for the same reason --debug does, and seeds it so the ordinary tenant
// flow is what gets exercised.
var Fixture *fixture.Set

// Store opens the config store, honouring --debug and --fixtures.
func Store() (*config.Store, error) {
	if Fixture != nil {
		st, err := config.OpenDebug()
		if err != nil {
			return nil, err
		}
		return st, seed(st, Fixture)
	}
	if Debug {
		return config.OpenDebug()
	}
	return config.Open()
}

var (
	seedMu   sync.Mutex
	seededAs *fixture.Set
)

// seed writes the fixture's tenants and device logins into the store. Every
// entry point opens its own store, so it runs once per fixture rather than once
// per store: later opens have to see the settings the run has changed since.
func seed(st *config.Store, f *fixture.Set) error {
	seedMu.Lock()
	defer seedMu.Unlock()
	if seededAs == f {
		return nil
	}

	// Written from scratch rather than merged, so changing which fixture a
	// process runs cannot leave the previous one's tenants in the list.
	tenants := f.SavedTenants()
	cfg := &config.Config{Tenants: tenants}
	if len(tenants) > 0 {
		cfg.LastTenant = tenants[0].Name
	}
	for _, t := range tenants {
		// A service account tenant with no client secret fails ValidateCreds
		// before anything ever reaches the fixture transport.
		if err := st.Secrets.Set(config.ClientSecretKey(t.TSGID), "fixture"); err != nil {
			return fmt.Errorf("seed fixture tenant %s: %w", t.Name, err)
		}
	}
	for key, pass := range f.Passwords() {
		if err := st.Secrets.Set(key, pass); err != nil {
			return fmt.Errorf("seed fixture secret: %w", err)
		}
	}
	if err := st.SaveConfig(cfg); err != nil {
		return err
	}
	seededAs = f
	return nil
}

// OpenStore returns the config store, or nil when it cannot be opened.
func OpenStore() *config.Store {
	st, err := Store()
	if err != nil {
		return nil
	}
	return st
}

// ShortAuthError turns a tenant's sign-in failure into the one thing to do
// about it. The OAuth legs each report their own refusal, so the raw text runs
// to three clauses and buries the difference between a tenant this account
// cannot reach and one whose stored credential simply aged out.
func ShortAuthError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	// A sign-in that ran out of time proves nothing about access, and it
	// arrives wrapped in ErrInteractiveRequired like a refusal does, so it has
	// to be read before the access wording below claims it.
	case strings.Contains(text, "did not complete within"):
		return "sign-in timed out"
	// The server rejecting a stored refresh token outright means this tenant
	// did work once: the credential expired rather than access being denied.
	case strings.Contains(text, "invalid_grant"),
		strings.Contains(text, "stored refresh token rejected"):
		return "sign-in expired"
	case errors.Is(err, saseauth.ErrInteractiveRequired),
		strings.Contains(text, "interaction_required"),
		strings.Contains(text, "login_required"):
		return "not accessible with this sign-in"
	case IsAuthFailure(text):
		return "credential rejected"
	}
	if i := strings.Index(text, "\n"); i > 0 {
		text = text[:i]
	}
	// The SD-WAN API answers failures with a JSON document whose only useful
	// part is the error code; printed whole it buries the line it is on.
	if code := apiErrorCode(text); code != "" {
		return code
	}
	return text
}

var apiErrCode = regexp.MustCompile(`"code"\s*:\s*"([A-Z0-9_]+)"`)

func apiErrorCode(text string) string {
	m := apiErrCode.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	// Keep whatever came before the document, which says which call failed.
	prefix, _, _ := strings.Cut(text, "{")
	return strings.TrimRight(strings.TrimSpace(prefix), ":") + ": " + m[1]
}

// IsAuthFailure guesses whether a failure was the credential rather than the
// device. The wire gives us only text: a rejected websocket handshake reports
// the status it got instead of 101, and the REST calls report resp.Status.
func IsAuthFailure(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{
		"but got 401", "but got 403",
		"status 401", "status 403",
		"unauthorized", "forbidden", "token expired", "invalid_grant",
	} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}
