package main

import (
	"os"

	"tyrtop.com/iontk/internal/config"
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

func resolveTenant(cfg *config.Config, name string) (config.Tenant, bool) {
	if t, ok := cfg.Resolve(name); ok {
		return t, true
	}
	if name == "" {
		return tenantFromEnv()
	}
	return config.Tenant{}, false
}

func tenantFlags(st *config.Store, t config.Tenant, ionUser string) flags {
	f := baseFlags()

	if t.TSGID != "" {
		f.TSGID = t.TSGID
	}
	if f.ClientID == "" {
		f.ClientID = t.ClientID
	}
	if f.ClientSecret == "" && t.TSGID != "" {
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
	return f
}

func needsIONPassword(st *config.Store, t config.Tenant, ionUser string) bool {
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

func forgetTenant(st *config.Store, cfg *config.Config, t config.Tenant) error {
	for _, u := range t.Users {
		_ = st.Secrets.Delete(config.IONPassKey(t.TSGID, u.Name))
	}
	_ = st.Secrets.Delete(config.ClientSecretKey(t.TSGID))
	_ = st.DropCache(t.TSGID)
	cfg.Remove(t.Name)
	return st.SaveConfig(cfg)
}
