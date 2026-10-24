package runtime

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
)

func secret(t *testing.T, st *config.Store, key string) string {
	t.Helper()
	v, _ := st.Secrets.Get(key)
	return v
}

func TestATenantAddedIsSavedAndInUse(t *testing.T) {
	rt, st := withConfig(t)
	if _, err := say(t, rt, "tenant.add", map[string]string{"name": "Acme Co", "tsg": "111", "client_id": "c", "secret": "s"}); err != nil {
		t.Fatal(err)
	}
	if cur, ok := rt.Tenant(); !ok || cur.Name != "Acme Co" || rt.Config().LastTenant != "Acme Co" {
		t.Errorf("in use = %+v %v, last %q", cur, ok, rt.Config().LastTenant)
	}
	if got := secret(t, st, config.ClientSecretKey("111")); got != "s" {
		t.Errorf("client secret = %q", got)
	}
	cfg, _ := st.Config()
	if _, ok := cfg.Tenant("acme co"); !ok {
		t.Error("the tenant was not saved")
	}
	if _, err := say(t, rt, "tenant.add", map[string]string{"name": "x", "tsg": ""}); err == nil {
		t.Error("a tenant with no tsg id was saved")
	}
}

func TestANewTSGTakesTheSecretsWithIt(t *testing.T) {
	rt, st := withConfig(t)
	rt.AddTenant(config.Tenant{Name: "Acme Co", TSGID: "111", ClientID: "c"}, "s")
	rt.AddUser("Acme Co", "", "admin", "ion")
	st.Secrets.Set(config.RefreshTokenKey("111"), "rt")

	said, err := say(t, rt, "tenant.edit", map[string]string{"name": "Acme", "tsg": "222"})
	if err != nil || len(said) != 1 || said[0] != "saved Acme" {
		t.Fatalf("edit = %q, %v", said, err)
	}
	for key, want := range map[string]string{
		config.ClientSecretKey("222"): "s", config.RefreshTokenKey("222"): "rt", config.IONPassKey("222", "admin"): "ion",
		config.ClientSecretKey("111"): "", config.IONPassKey("111", "admin"): "",
	} {
		if got := secret(t, st, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if cur, _ := rt.Tenant(); cur.Name != "Acme" || cur.TSGID != "222" || cur.ClientID != "c" || rt.Config().LastTenant != "Acme" {
		t.Errorf("in use = %+v, last %q", cur, rt.Config().LastTenant)
	}
}

func TestRemovingATenantDropsItsSecretsAndFallsBack(t *testing.T) {
	rt, st := withConfig(t)
	rt.AddTenant(config.Tenant{Name: "Globex", TSGID: "222"}, "")
	rt.AddTenant(config.Tenant{Name: "Acme Co", TSGID: "111", Auth: config.AuthBrowser}, "")
	st.Secrets.Set(config.RefreshTokenKey("111"), "rt")

	if _, err := say(t, rt, "tenant.remove", map[string]string{"tenant": "Acme Co"}); err != nil {
		t.Fatal(err)
	}
	if got := secret(t, st, config.RefreshTokenKey("111")); got != "" {
		t.Errorf("refresh token survived: %q", got)
	}
	if cur, ok := rt.Tenant(); !ok || cur.Name != "Globex" {
		t.Errorf("fell back to %+v %v, want the one left", cur, ok)
	}
	said, err := say(t, rt, "tenant.undo", nil)
	if err != nil || !strings.HasPrefix(said[0], "Acme Co is back") {
		t.Fatalf("undo = %q, %v", said, err)
	}
	if _, ok := rt.Config().Tenant("Acme Co"); !ok {
		t.Error("undo did not put the record back")
	}
	if got := secret(t, st, config.RefreshTokenKey("111")); got != "" {
		t.Error("undo brought a secret back")
	}
}

func TestUsersAddDefaultRunAsAndRemove(t *testing.T) {
	rt, st := withConfig(t)
	rt.AddTenant(config.Tenant{Name: "Acme Co", TSGID: "111"}, "")
	for _, u := range []string{"admin", "ops"} {
		if _, err := say(t, rt, "user.add", map[string]string{"name": u, "password": u + "-pw"}); err != nil {
			t.Fatal(err)
		}
	}
	if rt.RunAs("111") != "admin" || rt.IONUser() != "admin" {
		t.Errorf("run as %q, ion user %q; want the first added", rt.RunAs("111"), rt.IONUser())
	}
	if _, err := say(t, rt, "user.default", map[string]string{"user": "ops"}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := rt.Tenant(); cur.DefaultUser() != "ops" || rt.RunAs("111") != "ops" {
		t.Errorf("default %q, run as %q", cur.DefaultUser(), rt.RunAs("111"))
	}
	said, _ := say(t, rt, "user.list", nil)
	if len(said) != 2 || !strings.Contains(said[1], "ops  (default, active, saved)") {
		t.Errorf("user.list = %q", said)
	}

	if _, err := say(t, rt, "user.edit", map[string]string{"user": "ops", "name": "noc"}); err != nil {
		t.Fatal(err)
	}
	if got := secret(t, st, config.IONPassKey("111", "noc")); got != "ops-pw" || rt.RunAs("111") != "noc" {
		t.Errorf("renamed: password %q, run as %q", got, rt.RunAs("111"))
	}
	if _, err := say(t, rt, "user.remove", map[string]string{"user": "noc"}); err != nil {
		t.Fatal(err)
	}
	if got := secret(t, st, config.IONPassKey("111", "noc")); got != "" || rt.RunAs("111") != "admin" || rt.IONUser() != "admin" {
		t.Errorf("removed: password %q, run as %q, ion user %q", got, rt.RunAs("111"), rt.IONUser())
	}
}
