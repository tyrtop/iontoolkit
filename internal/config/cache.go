package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

const CacheFresh = 15 * time.Minute

type ElementCache struct {
	TSGID     string                       `json:"tsg_id"`
	FetchedAt time.Time                    `json:"fetched_at"`
	Elements  []api.Element                `json:"elements"`
	Sites     map[string]string            `json:"sites,omitempty"`
	HA        map[string]string            `json:"ha,omitempty"`
	Cells     map[string]map[string]string `json:"cells,omitempty"`
}

func (s *Store) SaveHA(tsg string, states map[string]string) error {
	c, err := s.Cache(tsg)
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	if c.HA == nil {
		c.HA = map[string]string{}
	}
	for eid, w := range states {
		c.HA[eid] = w
	}
	return s.SaveCache(c)
}

func (s *Store) SaveCells(tsg, col string, cells map[string]string) error {
	c, err := s.Cache(tsg)
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	if c.Cells == nil {
		c.Cells = map[string]map[string]string{}
	}
	if c.Cells[col] == nil {
		c.Cells[col] = map[string]string{}
	}
	for eid, text := range cells {
		c.Cells[col][eid] = text
	}
	return s.SaveCache(c)
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

func (c *ElementCache) Usable() bool { return c != nil && len(c.Elements) > 0 }
