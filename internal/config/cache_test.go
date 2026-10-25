package config

import (
	"os"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

func TestCacheMissingIsNil(t *testing.T) {
	c, err := newTestStore(t).Cache("1234")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("want nil cache, got %+v", c)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	s := newTestStore(t)

	want := &ElementCache{
		TSGID:     "1234",
		FetchedAt: time.Now().Add(-90 * time.Minute),
		Elements: []api.Element{
			{ID: "1", Name: "S0103-RTR-01", Model: "ion 2000", Connected: true},
			{ID: "2", Name: "S0103-RTR-02", Model: "ion 9000"},
		},
	}
	if err := s.SaveCache(want); err != nil {
		t.Fatal(err)
	}

	got, err := s.Cache("1234")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("cache came back nil")
	}
	if len(got.Elements) != 2 || got.Elements[0].Name != "S0103-RTR-01" {
		t.Errorf("elements = %+v", got.Elements)
	}
	if !got.Elements[0].Connected || got.Elements[1].Connected {
		t.Error("connected flags did not survive the round trip")
	}
}

func TestCacheIsolatedPerTenant(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveCache(&ElementCache{TSGID: "aaa", Elements: []api.Element{{ID: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCache(&ElementCache{TSGID: "bbb", Elements: []api.Element{{ID: "2"}, {ID: "3"}}}); err != nil {
		t.Fatal(err)
	}

	a, _ := s.Cache("aaa")
	b, _ := s.Cache("bbb")
	if a == nil || b == nil {
		t.Fatal("one tenant cache is missing")
	}
	if len(a.Elements) != 1 || len(b.Elements) != 2 {
		t.Errorf("tenant caches bled together: %d / %d", len(a.Elements), len(b.Elements))
	}
}

func TestCacheStaleness(t *testing.T) {
	fresh := &ElementCache{FetchedAt: time.Now().Add(-time.Minute)}
	old := &ElementCache{FetchedAt: time.Now().Add(-3 * time.Hour)}
	var zero *ElementCache
	never := &ElementCache{}

	if fresh.Stale(time.Hour) {
		t.Error("1m old cache reported stale against 1h ttl")
	}
	if !old.Stale(time.Hour) {
		t.Error("3h old cache reported fresh against 1h ttl")
	}
	if !zero.Stale(time.Hour) {
		t.Error("nil cache should be stale")
	}
	if !never.Stale(time.Hour) {
		t.Error("cache with zero FetchedAt should be stale")
	}
	if zero.Age() != 0 {
		t.Error("nil cache Age() should be zero")
	}
	if fresh.Age() < 30*time.Second {
		t.Errorf("Age() = %v, want about a minute", fresh.Age())
	}
}

func TestCacheModeIsOwnerOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCache(&ElementCache{TSGID: "1234"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.cachePath("1234"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache mode = %o, want 600", perm)
	}
}

func TestDropCache(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCache(&ElementCache{TSGID: "1234", Elements: []api.Element{{ID: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DropCache("1234"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Cache("1234"); c != nil {
		t.Error("cache survived DropCache")
	}
	if err := s.DropCache("1234"); err != nil {
		t.Errorf("DropCache on missing file = %v, want nil", err)
	}
}

func TestSavingTheElementsAgainKeepsTheHAAndCells(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCache(&ElementCache{TSGID: "1", Elements: []api.Element{{ID: "1"}}}); err != nil {
		t.Fatal(err)
	}
	s.SaveHA("1", map[string]string{"1": "active"})
	s.SaveCells("1", "uptime", map[string]string{"1": "3d"})
	if err := s.SaveCache(&ElementCache{TSGID: "1", Elements: []api.Element{{ID: "1"}, {ID: "2"}}}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Cache("1")
	if c == nil || len(c.Elements) != 2 || c.HA["1"] != "active" || c.Cells["uptime"]["1"] != "3d" {
		t.Errorf("after a second refresh: %+v", c)
	}
}
