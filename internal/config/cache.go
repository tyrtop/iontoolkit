package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

// CacheFresh is how long cached elements are treated as current, by the TUI
// on launch and by a --search that would rather answer from disk.
const CacheFresh = 15 * time.Minute

type ElementCache struct {
	TSGID     string            `json:"tsg_id"`
	FetchedAt time.Time         `json:"fetched_at"`
	Elements  []api.Element     `json:"elements"`
	Sites     map[string]string `json:"sites,omitempty"`
}

func (s *Store) cacheDir() string { return filepath.Join(s.Root, "cache") }

func (s *Store) cachePath(tsg string) string {
	return filepath.Join(s.cacheDir(), "elements-"+tsg+".json")
}

func (s *Store) Cache(tsg string) (*ElementCache, error) {
	b, err := os.ReadFile(s.cachePath(tsg))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	var c ElementCache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}
	return &c, nil
}

func (s *Store) SaveCache(c *ElementCache) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	return writeAtomic(s.cachePath(c.TSGID), b, 0o600)
}

func (s *Store) DropCache(tsg string) error {
	err := os.Remove(s.cachePath(tsg))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (c *ElementCache) Age() time.Duration {
	if c == nil || c.FetchedAt.IsZero() {
		return 0
	}
	return time.Since(c.FetchedAt)
}

func (c *ElementCache) Stale(ttl time.Duration) bool {
	return c == nil || c.FetchedAt.IsZero() || c.Age() > ttl
}

// Usable is a cache worth reading: one that was written and has something in
// it. An empty one is indistinguishable from a tenant that answered nothing.
func (c *ElementCache) Usable() bool { return c != nil && len(c.Elements) > 0 }
