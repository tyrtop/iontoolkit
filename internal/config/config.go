package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type IONUser struct {
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
}

// How a tenant proves who it is. An empty Auth means a service account, so
// tenants written before browser sign-in existed keep working.
const (
	AuthServiceAccount = "service_account"
	AuthBrowser        = "browser"
)

type Tenant struct {
	Name     string    `json:"name"`
	TSGID    string    `json:"tsg_id"`
	ClientID string    `json:"client_id"`
	Auth     string    `json:"auth,omitempty"`
	Users    []IONUser `json:"users,omitempty"`
}

func (t Tenant) UsesBrowser() bool { return t.Auth == AuthBrowser }

// AuthLabel names the tenant's sign-in method for display.
func (t Tenant) AuthLabel() string {
	if t.UsesBrowser() {
		return "browser"
	}
	return "service account"
}

type Config struct {
	LastTenant  string   `json:"last_tenant,omitempty"`
	Spinner     string   `json:"spinner,omitempty"`
	AllowWrites bool     `json:"allow_writes,omitempty"`
	SessionCols int      `json:"session_cols,omitempty"`
	SessionRows int      `json:"session_rows,omitempty"`
	Tenants     []Tenant `json:"tenants,omitempty"`
}

type Store struct {
	Root    string
	Secrets Secrets
}

func Open() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	root := filepath.Join(dir, "iontk")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", root, err)
	}
	return &Store{Root: root, Secrets: OpenSecrets(root)}, nil
}

func OpenAt(root string, s Secrets) *Store {
	return &Store{Root: root, Secrets: s}
}

func (s *Store) configPath() string { return filepath.Join(s.Root, "config.json") }

// BrowserDir is the browser profile kept for sign-in. Holding the identity
// provider's session cookie here is what lets a later sign-in skip the window.
func (s *Store) BrowserDir() string { return filepath.Join(s.Root, "browser") }

func (s *Store) Config() (*Config, error) {
	b, err := os.ReadFile(s.configPath())
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &c, nil
}

func (s *Store) SaveConfig(c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return writeAtomic(s.configPath(), append(b, '\n'), 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (c *Config) Tenant(name string) (Tenant, bool) {
	for _, t := range c.Tenants {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return Tenant{}, false
}

func (c *Config) Upsert(t Tenant) {
	for i := range c.Tenants {
		if strings.EqualFold(c.Tenants[i].Name, t.Name) {
			c.Tenants[i] = t
			return
		}
	}
	c.Tenants = append(c.Tenants, t)
}

func (c *Config) Remove(name string) bool {
	for i := range c.Tenants {
		if strings.EqualFold(c.Tenants[i].Name, name) {
			c.Tenants = append(c.Tenants[:i], c.Tenants[i+1:]...)
			if strings.EqualFold(c.LastTenant, name) {
				c.LastTenant = ""
			}
			return true
		}
	}
	return false
}

func (c *Config) Resolve(name string) (Tenant, bool) {
	if name != "" {
		return c.Tenant(name)
	}
	if c.LastTenant != "" {
		if t, ok := c.Tenant(c.LastTenant); ok {
			return t, true
		}
	}
	if len(c.Tenants) == 1 {
		return c.Tenants[0], true
	}
	return Tenant{}, false
}

func (t Tenant) DefaultUser() string {
	for _, u := range t.Users {
		if u.Default {
			return u.Name
		}
	}
	if len(t.Users) > 0 {
		return t.Users[0].Name
	}
	return ""
}

func (t Tenant) HasUser(name string) bool {
	for _, u := range t.Users {
		if u.Name == name {
			return true
		}
	}
	return false
}

func (t *Tenant) AddUser(name string, makeDefault bool) {
	if name == "" {
		return
	}
	if !t.HasUser(name) {
		t.Users = append(t.Users, IONUser{Name: name})
	}
	if makeDefault {
		for i := range t.Users {
			t.Users[i].Default = t.Users[i].Name == name
		}
	}
}

func (t *Tenant) RemoveUser(name string) bool {
	for i := range t.Users {
		if t.Users[i].Name == name {
			wasDefault := t.Users[i].Default
			t.Users = append(t.Users[:i], t.Users[i+1:]...)
			if wasDefault && len(t.Users) > 0 {
				t.Users[0].Default = true
			}
			return true
		}
	}
	return false
}

func ClientSecretKey(tsg string) string { return "tsg/" + tsg + "/client_secret" }

func IONPassKey(tsg, user string) string { return "tsg/" + tsg + "/ion_user/" + user }

func RefreshTokenKey(tsg string) string { return "tsg/" + tsg + "/refresh_token" }
