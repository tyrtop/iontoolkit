package config

import "testing"

func TestDefaultUserSwitchesRepeatedly(t *testing.T) {
	cfg := &Config{Tenants: []Tenant{{
		Name: "x", TSGID: "1",
		Users: []IONUser{{Name: "a", Default: true}, {Name: "b"}, {Name: "c"}},
	}}}

	for _, pick := range []string{"b", "c", "a", "b"} {
		cur := cfg.Tenants[0]
		cur.AddUser(pick, true)
		cfg.Upsert(cur)

		got := cfg.Tenants[0].DefaultUser()
		t.Logf("set %q -> DefaultUser()=%q users=%+v", pick, got, cfg.Tenants[0].Users)
		if got != pick {
			t.Errorf("after setting %q the default is %q", pick, got)
		}

		n := 0
		for _, u := range cfg.Tenants[0].Users {
			if u.Default {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d users marked default, want exactly 1", n)
		}
	}
}
