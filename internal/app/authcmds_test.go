package app

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
)

func TestABrowserSignInIsNeverStartedForAServiceAccount(t *testing.T) {
	st := config.OpenAt(t.TempDir(), config.NewMemorySecrets())
	cfg := &config.Config{Tenants: []config.Tenant{{Name: "Acme", TSGID: "111", Auth: config.AuthServiceAccount}}}
	rt := NewRuntime(st, cfg)
	rt.UseTenant(cfg.Tenants[0])
	for _, name := range []string{"auth.tenant", "auth.refresh"} {
		err := rt.Exec(&runtime.Call{Ctx: t.Context()}, name)
		if err == nil || !strings.Contains(err.Error(), "service account") {
			t.Errorf("%s = %v, want it refused before any browser opens", name, err)
		}
	}
}
