package main

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func TestTheCLIUsesEnvironmentCredentialsBeforeTheLastTenant(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SCM_TSG_ID", "999")
	t.Setenv("SCM_CLIENT_ID", "id@999.iam.panserviceaccount.com")
	t.Setenv("SCM_CLIENT_SECRET", "secret")
	st, err := config.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConfig(&config.Config{LastTenant: "Acme", Tenants: []config.Tenant{
		{Name: "Acme", TSGID: "111", Auth: config.AuthBrowser},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tenantNotes = nil })

	theRuntime = nil
	got, ok := cliRuntime().Tenant()
	if !ok || got.TSGID != "999" {
		t.Errorf("tenant = %+v, want the environment's 999 ahead of the last-used Acme", got)
	}
	if !strings.Contains(strings.Join(tenantNotes, "\n"), "environment") {
		t.Errorf("notes = %q, want them to say the environment was used", tenantNotes)
	}
}
