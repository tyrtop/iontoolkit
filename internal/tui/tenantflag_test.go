package tui

import (
	"context"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func TestTenantFlagOpensOnThatTenant(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{
		LastTenant: "Globex",
		Tenants: []config.Tenant{
			{Name: "Acme Co", TSGID: "1"},
			{Name: "Globex", TSGID: "2"},
		},
	}

	old := Tenant
	t.Cleanup(func() { Tenant = old })
	Tenant = "acme co"

	m := newTUIModel(context.Background(), st, cfg)
	if !m.hasTenant() {
		t.Fatal("hasTenant = false, want --tenant to resolve")
	}
	if m.tenant().Name != "Acme Co" {
		t.Errorf("tenant = %q, want --tenant to win over last_tenant %q", m.tenant().Name, cfg.LastTenant)
	}

	Tenant = ""
	if got := newTUIModel(context.Background(), st, cfg); got.tenant().Name != "Globex" {
		t.Errorf("tenant = %q, want the last tenant used with no --tenant", got.tenant().Name)
	}
}
