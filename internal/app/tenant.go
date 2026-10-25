package app

import (
	"os"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/runtime"
)

func tenantFromEnv() (config.Tenant, bool) {
	tsg := os.Getenv("SCM_TSG_ID")
	if tsg == "" {
		return config.Tenant{}, false
	}
	t := config.Tenant{
		Name:     "env",
		TSGID:    tsg,
		ClientID: os.Getenv("SCM_CLIENT_ID"),
	}
	if u := os.Getenv("ION_USER"); u != "" {
		t.AddUser(u, true)
	}
	return t, true
}

func ResolveTenant(cfg *config.Config, name string) (config.Tenant, bool) {
	if t, ok := cfg.Resolve(name); ok {
		return t, true
	}
	if name == "" {
		return tenantFromEnv()
	}
	return config.Tenant{}, false
}

func StartTenant(rt *runtime.Runtime, name string) ([]string, error) {
	cfg := configOf(rt)
	var creds Credentials
	var err error
	if rt.Store() != nil {
		creds, err = resolveCredentials(rt.Store(), cfg, name, BaseFlags())
	} else {
		creds, err = ResolveCredentials(nil, name)
	}
	t, ok := creds.Tenant, err == nil
	switch {
	case err != nil:
		t, ok = ResolveTenant(cfg, name)
	case creds.From == FromEnv:
		t, _ = ResolveTenant(&config.Config{}, "")
	}
	if ok {
		rt.UseTenant(t)
		rt.SetIONUser(t.DefaultUser())
	} else {
		rt.ClearTenant()
	}
	mode := cfg.AuthMode
	if mode != runtime.AuthBrowser && mode != runtime.AuthTenant {
		mode = ""
		if ok && !t.UsesBrowser() {
			mode = runtime.AuthTenant
		}
	}
	if mode != "" {
		rt.SetAuthMode(mode)
	}
	if err != nil {
		return nil, err
	}
	return creds.Lines(), nil
}

func configOf(rt *runtime.Runtime) *config.Config {
	if cfg := rt.Config(); cfg != nil {
		return cfg
	}
	return &config.Config{}
}

func TenantFlags(st *config.Store, t config.Tenant, ionUser string) Flags {
	f := BaseFlags()

	if t.TSGID != "" {
		f.TSGID = t.TSGID
	}
	f.BrowserAuth = t.UsesBrowser()
	if f.ClientID == "" {
		f.ClientID = t.ClientID
	}
	if f.ClientSecret == "" && t.TSGID != "" && !f.BrowserAuth {
		if v, err := st.Secrets.Get(config.ClientSecretKey(t.TSGID)); err == nil {
			f.ClientSecret = v
		}
	}

	if ionUser == "" {
		ionUser = t.DefaultUser()
	}
	if ionUser != "" {
		f.IONUsername = ionUser
		if f.IONPassword == "" && t.TSGID != "" {
			if v, err := st.Secrets.Get(config.IONPassKey(t.TSGID, ionUser)); err == nil {
				f.IONPassword = v
			}
		}
	}
	logging.Mask(f.ClientSecret)
	logging.Mask(f.IONPassword)
	return f
}

func NeedsIONPassword(st *config.Store, t config.Tenant, ionUser string) bool {
	if os.Getenv("ION_PASS") != "" {
		return false
	}
	if ionUser == "" {
		ionUser = t.DefaultUser()
	}
	if ionUser == "" {
		return true
	}
	v, err := st.Secrets.Get(config.IONPassKey(t.TSGID, ionUser))
	return err != nil || v == ""
}
