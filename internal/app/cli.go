package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

// --themes, read by every command: a batch prints marks and a bar too
var ThemesFile string

func ThemesPath(st *config.Store) string {
	if ThemesFile != "" {
		return ThemesFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, ui.ThemesFileName)
}

// Called by every command, not just the TUI. Nothing here can fail a run; a
// nil cfg is read from the store.
func ApplyDisplaySettings(st *config.Store, cfg *config.Config) {
	_ = ui.LoadThemes(ThemesPath(st))
	if cfg == nil {
		if st == nil {
			return
		}
		var err error
		if cfg, err = st.Config(); err != nil {
			return
		}
	}
	_ = clock.Use(cfg.Zone)
	ui.UseTheme(cfg.Theme)
}

// CredSource is which rule picked the credentials. It gets printed, because
// otherwise the first sign of a wrong tenant is a fleet of dead elements.
type CredSource string

const (
	FromFlag CredSource = "--tenant"
	FromEnv  CredSource = "environment"
	FromLast CredSource = "last used"
	FromOnly CredSource = "the only saved tenant"
)

// Credentials is what a run with no TUI signs in with, and where that came
// from.
type Credentials struct {
	Flags  Flags
	Tenant config.Tenant
	From   CredSource

	// An environment credential that --tenant overrode. Printed, so an ignored
	// SCM_TOKEN isn't a surprise.
	dropped string
	// Kept so we can tell a keyring from the fallback file. Nil for an
	// environment run, which opens no store.
	secrets config.Secrets
}

// No secret values in here. It's for showing which tenant a run picked.
func (c Credentials) Lines() []string {
	var out []string
	if c.From == FromEnv {
		out = append(out, "using credentials from the environment")
	} else {
		out = append(out, fmt.Sprintf("using tenant %s (%s, %s)", c.Tenant.Name, c.Tenant.AuthLabel(), c.From))
	}
	if c.dropped != "" {
		out = append(out, c.dropped)
	}
	if c.secrets != nil && config.Plaintext(c.secrets) {
		w := "secrets: " + config.SecretsWarning(c.secrets)
		// Only the fallback store has a file to name.
		if p, ok := c.secrets.(interface{ Path() string }); ok {
			w += " at " + p.Path()
		}
		out = append(out, w)
	}
	return out
}

// ResolveCredentials reads the store and the keyring the same way the TUI
// does. Environment variables come before the saved tenants, so CI and
// SCM_TOKEN still work on a machine that has signed in.
//
// The secrets only go into the returned Flags. They never reach the
// environment and never get printed.
func ResolveCredentials(st *config.Store, name string) (Credentials, error) {
	env := BaseFlags()

	if st == nil {
		if name != "" {
			return Credentials{}, fmt.Errorf("--tenant %s: no config store to read tenants from", name)
		}
		return Credentials{Flags: env, From: FromEnv}, nil
	}

	cfg, err := st.Config()
	if err != nil {
		return Credentials{}, err
	}

	if name != "" {
		t, ok := cfg.Tenant(name)
		if !ok {
			return Credentials{}, fmt.Errorf("no saved tenant named %q; %s", name, TenantChoices(cfg))
		}
		return tenantCredentials(st, t, FromFlag)
	}

	if env.ValidateCreds() == nil {
		return Credentials{Flags: env, From: FromEnv}, nil
	}

	if cfg.LastTenant != "" {
		if t, ok := cfg.Tenant(cfg.LastTenant); ok {
			return tenantCredentials(st, t, FromLast)
		}
	}
	if len(cfg.Tenants) == 1 {
		return tenantCredentials(st, cfg.Tenants[0], FromOnly)
	}
	return Credentials{}, fmt.Errorf("no credentials: %s", TenantChoices(cfg))
}

func tenantCredentials(st *config.Store, t config.Tenant, from CredSource) (Credentials, error) {
	// Pass ION_USER through instead of falling back to the tenant's default
	// user. If ION_PASS is set too, it goes with the user named next to it.
	f := TenantFlags(st, t, os.Getenv("ION_USER"))

	c := Credentials{Tenant: t, From: from, secrets: st.Secrets}
	if from == FromFlag && f.Token != "" {
		// Otherwise NewClient picks up SCM_TOKEN, and the run hits whatever
		// tenant issued that token while reporting this one.
		f.Token = ""
		c.dropped = "ignoring SCM_TOKEN: --tenant chose a saved tenant"
	}
	if err := f.ValidateCreds(); err != nil {
		// A tenant can be in config.json with its keyring entry gone.
		return Credentials{}, fmt.Errorf("tenant %s has no usable credential: %w", t.Name, err)
	}
	c.Flags = f
	return c, nil
}

// TenantChoices lists the names, so a credential error can say what the
// options are.
func TenantChoices(cfg *config.Config) string {
	var names []string
	for _, t := range cfg.Tenants {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return "no tenants are saved — run iontk to sign in, or set SCM_CLIENT_ID, SCM_CLIENT_SECRET and SCM_TSG_ID"
	}
	return "saved tenants: " + strings.Join(names, ", ")
}
