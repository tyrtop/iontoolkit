package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/tutorial"
	"tyrtop.com/iontk/internal/ui"
)

var ThemesFile string

var LaunchedFake string

func ThemesPath(st *config.Store) string {
	if ThemesFile != "" {
		return ThemesFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, ui.ThemesDirName)
}

func RemoveThemes(st *config.Store) error {
	if st == nil {
		return nil
	}
	dir := filepath.Join(st.Root, ui.ThemesDirName)
	legacy := filepath.Join(st.Root, ui.ThemesFileName)
	return errors.Join(os.RemoveAll(dir), os.RemoveAll(legacy))
}

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
	ui.HideThemes(cfg.HiddenThemes)
	ui.UseTheme(cfg.Theme)
}

type CredSource string

const (
	FromFlag CredSource = "--tenant"
	FromEnv  CredSource = "environment"
	FromLast CredSource = "last used"
	FromOnly CredSource = "the only saved tenant"
)

type Credentials struct {
	Flags  Flags
	Tenant config.Tenant
	From   CredSource

	dropped string
	secrets config.Secrets
}

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
		if p, ok := c.secrets.(interface{ Path() string }); ok {
			w += " at " + p.Path()
		}
		out = append(out, w)
	}
	return out
}

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
	f := TenantFlags(st, t, os.Getenv("ION_USER"))

	c := Credentials{Tenant: t, From: from, secrets: st.Secrets}
	if from == FromFlag && f.Token != "" {
		f.Token = ""
		c.dropped = "ignoring SCM_TOKEN: --tenant chose a saved tenant"
	}
	if err := f.ValidateCreds(); err != nil {
		return Credentials{}, fmt.Errorf("tenant %s has no usable credential: %w", t.Name, err)
	}
	c.Flags = f
	return c, nil
}

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

var (
	LogFile  string
	LogLevel string
)

func LogsPath(st *config.Store) string {
	if LogFile != "" {
		return LogFile
	}
	if st == nil {
		return ""
	}
	return st.LogsDir()
}

func LogLevelAsked() string {
	if LogLevel != "" {
		return LogLevel
	}
	return os.Getenv("ION_LOG_LEVEL")
}

func StartLogging(st *config.Store, stderr io.Writer) error {
	level, lvlErr := logging.ParseLevel(LogLevelAsked())
	if lvlErr != nil {
		if LogLevel != "" {
			return lvlErr
		}
		level = slog.LevelInfo
	}

	opt := logging.Options{Level: level, Stderr: stderr}
	if LogFile != "" {
		opt.File = LogFile
	} else if st != nil {
		opt.Dir = st.LogsDir()
	}
	if _, err := logging.Start(opt); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	return lvlErr
}

var TutorialFile string

func TutorialsPath(st *config.Store) string {
	if TutorialFile != "" {
		return TutorialFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, tutorial.DirName)
}
