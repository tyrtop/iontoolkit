package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/logging"
)

type IONUser struct {
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
}

const (
	AuthServiceAccount = "service_account"
	AuthBrowser        = "browser"
)

type Tenant struct {
	Name     string    `json:"name"`
	TSGID    string    `json:"tsg_id"`
	ClientID string    `json:"client_id"`
	Auth     string    `json:"auth,omitempty"`
	Added    bool      `json:"added,omitempty"`
	Users    []IONUser `json:"users,omitempty"`
}

func (c *Config) AddedTenants() []Tenant {
	if c == nil {
		return nil
	}
	var out []Tenant
	for _, t := range c.Tenants {
		if t.Added {
			out = append(out, t)
		}
	}
	return out
}

func (c *Config) IsAdded(tsg string) bool {
	if c == nil {
		return false
	}
	for _, t := range c.Tenants {
		if t.TSGID == tsg {
			return t.Added
		}
	}
	return false
}

func (t Tenant) UsesBrowser() bool { return t.Auth == AuthBrowser }

func (t Tenant) AuthLabel() string {
	if t.UsesBrowser() {
		return "browser"
	}
	return "service account"
}

type Config struct {
	LastTenant   string   `json:"last_tenant,omitempty"`
	Spinner      string   `json:"spinner,omitempty"`
	DefaultMode  string   `json:"default_mode,omitempty"`
	AuthMode     string   `json:"auth_mode,omitempty"`
	AllowWrites  bool     `json:"allow_writes,omitempty"`
	SessionCols  int      `json:"session_cols,omitempty"`
	SessionRows  int      `json:"session_rows,omitempty"`
	PageStep     int      `json:"page_step,omitempty"`
	Theme        string   `json:"theme,omitempty"`
	HiddenThemes []string `json:"hidden_themes,omitempty"`
	Zone         string   `json:"zone,omitempty"`
	TutorialSeen bool     `json:"tutorial_seen,omitempty"`
	BatchFormat  string   `json:"batch_format,omitempty"`
	BatchOrient  string   `json:"batch_orient,omitempty"`
	BatchToFile  bool     `json:"batch_to_file,omitempty"`
	BatchDir     string   `json:"batch_dir,omitempty"`
	Tenants      []Tenant `json:"tenants,omitempty"`
}

func (c *Config) BatchOutDir() string {
	if c == nil || !c.BatchToFile {
		return ""
	}
	return c.BatchDir
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

func DebugRoot() string { return filepath.Join(os.TempDir(), "iontk-debug") }

var (
	debugOnce    sync.Once
	debugSecrets Secrets
	debugErr     error
)

func OpenDebug() (*Store, error) {
	root := DebugRoot()
	debugOnce.Do(func() {
		if err := os.RemoveAll(root); err != nil {
			debugErr = fmt.Errorf("clear %s: %w", root, err)
			return
		}
		debugSecrets = NewMemorySecrets()
	})
	if debugErr != nil {
		return nil, debugErr
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", root, err)
	}
	return &Store{Root: root, Secrets: debugSecrets}, nil
}

func (s *Store) configPath() string { return filepath.Join(s.Root, "config.json") }

func (s *Store) BrowserDir() string { return filepath.Join(s.Root, "browser") }

func (s *Store) LogsDir() string { return filepath.Join(s.Root, logging.DirName) }

func (s *Store) ExportsDir() string { return filepath.Join(s.Root, "exports") }

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

func AccountRefreshTokenKey() string { return "account/refresh_token" }
