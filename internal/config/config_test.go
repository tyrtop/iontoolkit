package config

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return OpenAt(root, NewFileSecrets(filepath.Join(root, "secrets.json")))
}

func TestConfigMissingFileIsEmpty(t *testing.T) {
	c, err := newTestStore(t).Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tenants) != 0 || c.LastTenant != "" {
		t.Errorf("want empty config, got %+v", c)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	s := newTestStore(t)

	want := &Config{
		LastTenant: "acme-prod",
		Tenants: []Tenant{{
			Name:     "acme-prod",
			TSGID:    "1234567890",
			ClientID: "svc@1234.iam",
			Users:    []IONUser{{Name: "readonly", Default: true}, {Name: "cisops"}},
		}},
	}
	if err := s.SaveConfig(want); err != nil {
		t.Fatal(err)
	}

	got, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTenant != want.LastTenant || len(got.Tenants) != 1 {
		t.Fatalf("got %+v", got)
	}
	tn := got.Tenants[0]
	if tn.TSGID != "1234567890" || tn.ClientID != "svc@1234.iam" || len(tn.Users) != 2 {
		t.Errorf("tenant = %+v", tn)
	}
	if tn.DefaultUser() != "readonly" {
		t.Errorf("DefaultUser() = %q, want readonly", tn.DefaultUser())
	}
}

func TestConfigModeIsOwnerOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveConfig(&Config{LastTenant: "x"}); err != nil {
		t.Fatal(err)
	}
	fi, err := statFile(s.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %o, want 600", perm)
	}
}

func TestUpsertReplacesByName(t *testing.T) {
	c := &Config{}
	c.Upsert(Tenant{Name: "acme", TSGID: "1"})
	c.Upsert(Tenant{Name: "ACME", TSGID: "2"})

	if len(c.Tenants) != 1 {
		t.Fatalf("want 1 tenant, got %d", len(c.Tenants))
	}
	if c.Tenants[0].TSGID != "2" {
		t.Errorf("TSGID = %q, want 2", c.Tenants[0].TSGID)
	}
}

func TestRemoveClearsLastTenant(t *testing.T) {
	c := &Config{LastTenant: "acme", Tenants: []Tenant{{Name: "acme"}, {Name: "lab"}}}
	if !c.Remove("acme") {
		t.Fatal("Remove returned false")
	}
	if c.LastTenant != "" {
		t.Errorf("LastTenant = %q, want empty", c.LastTenant)
	}
	if len(c.Tenants) != 1 || c.Tenants[0].Name != "lab" {
		t.Errorf("tenants = %+v", c.Tenants)
	}
	if c.Remove("nope") {
		t.Error("Remove of missing tenant returned true")
	}
}

func TestResolve(t *testing.T) {
	two := &Config{Tenants: []Tenant{{Name: "a"}, {Name: "b"}}}
	one := &Config{Tenants: []Tenant{{Name: "only"}}}
	last := &Config{LastTenant: "b", Tenants: []Tenant{{Name: "a"}, {Name: "b"}}}
	stale := &Config{LastTenant: "gone", Tenants: []Tenant{{Name: "a"}, {Name: "b"}}}

	cases := map[string]struct {
		cfg   *Config
		name  string
		want  string
		found bool
	}{
		"explicit name":         {two, "b", "b", true},
		"explicit name missing": {two, "zz", "", false},
		"falls back to last":    {last, "", "b", true},
		"single tenant":         {one, "", "only", true},
		"ambiguous":             {two, "", "", false},
		"last tenant deleted":   {stale, "", "", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := tc.cfg.Resolve(tc.name)
			if ok != tc.found {
				t.Fatalf("found = %v, want %v", ok, tc.found)
			}
			if got.Name != tc.want {
				t.Errorf("name = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func TestUserManagement(t *testing.T) {
	var tn Tenant

	tn.AddUser("readonly", true)
	tn.AddUser("cisops", false)
	tn.AddUser("readonly", false)

	if len(tn.Users) != 2 {
		t.Fatalf("want 2 users, got %+v", tn.Users)
	}
	if tn.DefaultUser() != "readonly" {
		t.Errorf("DefaultUser() = %q", tn.DefaultUser())
	}

	tn.AddUser("cisops", true)
	if tn.DefaultUser() != "cisops" {
		t.Errorf("after re-default, DefaultUser() = %q", tn.DefaultUser())
	}

	if !tn.RemoveUser("cisops") {
		t.Fatal("RemoveUser returned false")
	}
	if tn.DefaultUser() != "readonly" {
		t.Errorf("default should move to survivor, got %q", tn.DefaultUser())
	}
	if tn.HasUser("cisops") {
		t.Error("removed user still present")
	}
}

func TestDefaultUserWithNoUsers(t *testing.T) {
	var tn Tenant
	if got := tn.DefaultUser(); got != "" {
		t.Errorf("DefaultUser() = %q, want empty", got)
	}
}

func TestSecretKeysAreDistinct(t *testing.T) {
	if ClientSecretKey("1") == ClientSecretKey("2") {
		t.Error("client secret keys collide across tenants")
	}
	if IONPassKey("1", "a") == IONPassKey("1", "b") {
		t.Error("ion pass keys collide across users")
	}
	if IONPassKey("1", "a") == IONPassKey("2", "a") {
		t.Error("ion pass keys collide across tenants")
	}
	if ClientSecretKey("1") == IONPassKey("1", "client_secret") {
		t.Error("key namespaces overlap")
	}
}
