package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func savedStore(t *testing.T) (*Store, *Config) {
	t.Helper()
	root := t.TempDir()
	st := OpenAt(root, NewFileSecrets(filepath.Join(root, "secrets.json")))

	cfg := &Config{
		LastTenant: "Acme Co",
		Tenants: []Tenant{
			{Name: "Acme Co", TSGID: "111", Auth: AuthBrowser, Users: []IONUser{{Name: "admin", Default: true}}},
			{Name: "Lab", TSGID: "222", Users: []IONUser{{Name: "ops"}, {Name: "ro"}}},
		},
	}
	if err := st.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		IONPassKey("111", "admin"), IONPassKey("222", "ops"), IONPassKey("222", "ro"),
		ClientSecretKey("111"), ClientSecretKey("222"),
		RefreshTokenKey("111"), RefreshTokenKey("222"),
		AccountRefreshTokenKey(),
	} {
		if err := st.Secrets.Set(k, "secret"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveCache(&ElementCache{TSGID: "111"}); err != nil {
		t.Fatal(err)
	}
	cmds := &CommandStore{}
	cmds.Record("dump device status", "ion 1200", time.Now())
	if err := cmds.SetFavourite(1, "dump interface status all", "ion 1200"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCommands(cmds); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTenantCache(&TenantCache{Account: "someone"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.BrowserDir(), "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.BrowserDir(), "Default", "Cookies"), []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.LogsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.LogsDir(), "iontk-20261020-091403.log"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return st, cfg
}

func TestResetLeavesNothingBehind(t *testing.T) {
	st, cfg := savedStore(t)

	if err := st.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	for _, t2 := range cfg.Tenants {
		for _, u := range t2.Users {
			if v, err := st.Secrets.Get(IONPassKey(t2.TSGID, u.Name)); err == nil {
				t.Errorf("ION password for %s/%s survived as %q", t2.TSGID, u.Name, v)
			}
		}
		if _, err := st.Secrets.Get(ClientSecretKey(t2.TSGID)); err == nil {
			t.Errorf("client secret for %s survived", t2.TSGID)
		}
		if _, err := st.Secrets.Get(RefreshTokenKey(t2.TSGID)); err == nil {
			t.Errorf("refresh token for %s survived", t2.TSGID)
		}
	}
	if _, err := st.Secrets.Get(AccountRefreshTokenKey()); err == nil {
		t.Error("the account refresh token survived")
	}

	for _, path := range []string{
		st.configPath(),
		st.tenantsPath(),
		st.cacheDir(),
		st.commandsPath(),
		st.BrowserDir(),
		st.LogsDir(),
		filepath.Join(st.Root, "secrets.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists after the reset (stat err = %v)", path, err)
		}
	}

	after, err := st.Config()
	if err != nil {
		t.Fatalf("Config() after reset error = %v", err)
	}
	if len(after.Tenants) != 0 || after.LastTenant != "" {
		t.Errorf("config after reset = %+v, want the zero value", after)
	}
}

func TestResetOnAFreshStoreIsNotAnError(t *testing.T) {
	root := t.TempDir()
	st := OpenAt(root, NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Reset(); err != nil {
		t.Errorf("Reset() on a store with nothing saved error = %v", err)
	}
}

func TestResetDeletesSecretsBeforeTheConfigThatNamesThem(t *testing.T) {
	st, _ := savedStore(t)

	st.Secrets = &orderSpy{Secrets: st.Secrets, configPath: st.configPath()}
	if err := st.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if spy := st.Secrets.(*orderSpy); spy.configGoneFirst {
		t.Error("the config was deleted while keyring entries were still being enumerated")
	} else if spy.deletes == 0 {
		t.Error("no secrets were deleted at all")
	}
}

type orderSpy struct {
	Secrets
	configPath      string
	deletes         int
	configGoneFirst bool
}

func (s *orderSpy) Delete(key string) error {
	s.deletes++
	if _, err := os.Stat(s.configPath); os.IsNotExist(err) {
		s.configGoneFirst = true
	}
	return s.Secrets.Delete(key)
}
