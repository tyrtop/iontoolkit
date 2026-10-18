package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// KnownTenant is a tenant the signed-in account can reach. These are discovered
// rather than configured: the portal decides what the account can see, so there
// is nothing for the user to add.
type KnownTenant struct {
	TSGID string `json:"tsg_id"`
	Name  string `json:"name"`
}

// TenantCache remembers the last discovered tenant list so the switcher works
// without a round trip, and so a restart does not need the browser.
type TenantCache struct {
	FetchedAt time.Time     `json:"fetched_at"`
	Account   string        `json:"account,omitempty"`
	Tenants   []KnownTenant `json:"tenants"`
}

func (s *Store) tenantsPath() string { return filepath.Join(s.Root, "tenants.json") }

// TenantCache reads the last discovered list. A nil store has none, which keeps
// rendering safe before a store exists.
func (s *Store) TenantCache() (*TenantCache, error) {
	if s == nil {
		return nil, nil
	}
	b, err := os.ReadFile(s.tenantsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.tenantsPath(), err)
	}
	var c TenantCache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.tenantsPath(), err)
	}
	return &c, nil
}

func (s *Store) SaveTenantCache(c *TenantCache) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tenants: %w", err)
	}
	return writeAtomic(s.tenantsPath(), b, 0o600)
}

func (s *Store) DropTenantCache() error {
	err := os.Remove(s.tenantsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (c *TenantCache) Age() time.Duration {
	if c == nil || c.FetchedAt.IsZero() {
		return 0
	}
	return time.Since(c.FetchedAt)
}
