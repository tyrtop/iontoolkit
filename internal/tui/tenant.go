package tui

import "tyrtop.com/iontk/internal/config"

func forgetTenant(st *config.Store, cfg *config.Config, t config.Tenant) error {
	for _, u := range t.Users {
		_ = st.Secrets.Delete(config.IONPassKey(t.TSGID, u.Name))
	}
	_ = st.Secrets.Delete(config.ClientSecretKey(t.TSGID))
	_ = st.Secrets.Delete(config.RefreshTokenKey(t.TSGID))
	_ = st.DropCache(t.TSGID)
	cfg.Remove(t.Name)
	return st.SaveConfig(cfg)
}
