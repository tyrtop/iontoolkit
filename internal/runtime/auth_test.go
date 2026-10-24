package runtime

import (
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
)

func TestPicksStartWithTheFleetAndBecomeIt(t *testing.T) {
	rt, st := withConfig(t)
	rt.Config().Tenants = []config.Tenant{
		{Name: "Tractor Supply", TSGID: "111", Auth: config.AuthBrowser, Added: true},
		{Name: "Old Co", TSGID: "333", Auth: config.AuthBrowser, Added: true},
	}
	rt.UseTenant(rt.Config().Tenants[1])
	rt.SetDiscovered([]saseauth.Tenant{{ID: "111", Name: "Tractor Supply"}, {ID: "222", Name: "Niagara"}, {ID: "333", Name: "Old Co"}})
	if !rt.Picked("111") || !rt.Picked("333") || rt.Picked("222") {
		t.Fatalf("picks at start: %d", rt.PickedCount())
	}

	rt.TogglePick("333")
	rt.TogglePick("222")
	if err := rt.AddPicked(); err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Config()
	if !cfg.IsAdded("111") || !cfg.IsAdded("222") || cfg.IsAdded("333") {
		t.Errorf("fleet = %+v", cfg.AddedTenants())
	}
	if cur, _ := rt.Tenant(); cur.TSGID != "111" || cfg.LastTenant != "Tractor Supply" {
		t.Errorf("in use %+v, last %q; want the first picked, as the one in use left the fleet", cur, cfg.LastTenant)
	}
	if len(rt.Discovered()) != 0 || rt.PickedCount() != 0 {
		t.Error("the picks outlived being added")
	}
}

func TestNothingPickedChangesNothing(t *testing.T) {
	rt, _ := withConfig(t)
	rt.Config().Tenants = []config.Tenant{{Name: "A", TSGID: "1", Auth: config.AuthBrowser, Added: true}}
	rt.SetDiscovered([]saseauth.Tenant{{ID: "1", Name: "A"}})
	rt.PickAll()
	if rt.PickedCount() != 0 {
		t.Fatal("ctrl+a with everything picked should clear")
	}
	if err := rt.AddPicked(); err == nil {
		t.Fatal("nothing picked was taken")
	}
	if !rt.Config().IsAdded("1") {
		t.Error("a refused pick still took a tenant out of the fleet")
	}
}

func TestATokenDeathKeepsItsFirstReasonWhenAsked(t *testing.T) {
	rt := New()
	rt.TokenDied("sign-in expired", true)
	rt.TokenDied("token expired — sign in again", true)
	if got := rt.TokenDead(); got != "sign-in expired" {
		t.Errorf("dead = %q", got)
	}
	rt.TokenDied("later", false)
	rt.TokenAlive()
	if rt.TokenDead() != "" {
		t.Error("still dead after TokenAlive")
	}
}
