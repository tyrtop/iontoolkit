package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

func cacheStore(t *testing.T, tsg string, count int, fetched time.Time) *config.Store {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if count == 0 {
		return st
	}
	els := make([]api.Element, count)
	for i := range els {
		els[i] = api.Element{ID: "e", Name: "s0001-rtr-01"}
	}
	c := &config.ElementCache{TSGID: tsg, FetchedAt: fetched, Elements: els}
	if err := st.SaveCache(c); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	return st
}

func TestLaunchDecidesWhetherElementsAreOwedARefresh(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		fetched   time.Time
		wantState cacheState
	}{
		{"no cache at all", 0, time.Time{}, cacheUpdating},
		{"cache younger than the freshness window", 3, time.Now().Add(-2 * time.Minute), cacheCached},
		{"cache right at the window", 3, time.Now().Add(-cacheFresh - time.Second), cacheUpdating},
		{"cache well past the window", 3, time.Now().Add(-38 * time.Minute), cacheUpdating},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := cacheStore(t, "1", tt.count, tt.fetched)
			cfg := &config.Config{
				Tenants:    []config.Tenant{{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount, ClientID: "svc"}},
				LastTenant: "Acme Co",
			}
			m := newTUIModel(context.Background(), st, cfg)
			if m.cacheState() != tt.wantState {
				t.Errorf("cacheState = %v, want %v", m.cacheState(), tt.wantState)
			}
			if tt.count > 0 && m.cacheCount() != tt.count {
				t.Errorf("cacheCount = %d, want %d", m.cacheCount(), tt.count)
			}
		})
	}
}

func TestInitStartsARefreshOnlyWhenOneIsOwed(t *testing.T) {
	tests := []struct {
		name     string
		state    cacheState
		secret   bool
		wantCmds bool
	}{
		{"stale cache with usable credentials refreshes", cacheUpdating, true, true},
		{"fresh cache does not refresh", cacheCached, true, false},
		{"stale cache without credentials cannot refresh", cacheUpdating, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
			if tt.secret {
				if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
					t.Fatalf("seed secret: %v", err)
				}
			}
			m := withWorkspace(withFleet(withStore(tuiModel{
				ctx: context.Background(),
			}, st, &config.Config{}), tt.state, 0, time.Time{}), config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"}, true, "", authBrowser)
			m.spinner = ui.NewSpinner("")

			const always = 2
			got := m.Init()
			if got == nil {
				t.Fatal("Init() = nil, want at least the spinner tick")
			}
			batch, ok := got().(tea.BatchMsg)
			if !ok {
				t.Fatalf("Init() = %T, want a batch of at least %d", got(), always)
			}
			if refreshing := len(batch) > always; refreshing != tt.wantCmds {
				t.Errorf("Init() returned %d commands, so refreshing = %v, want %v",
					len(batch), refreshing, tt.wantCmds)
			}
		})
	}
}

func TestRefreshNow(t *testing.T) {
	t.Run("without a tenant it only reports", func(t *testing.T) {
		m := withStore(tuiModel{}, nil, &config.Config{})
		mm, cmd := m.refreshNow()
		got := mm.(tuiModel)
		if cmd != nil {
			t.Error("a command was issued with no tenant to refresh")
		}
		if !strings.Contains(got.status, "no tenant") {
			t.Errorf("status = %q, want it to say there is no tenant", got.status)
		}
	})

	t.Run("without credentials it fails visibly", func(t *testing.T) {
		root := t.TempDir()
		st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
		m := withWorkspace(withStore(tuiModel{
			ctx: context.Background(),
		}, st, &config.Config{}), config.Tenant{Name: "Acme Co", TSGID: "1"}, true, "", authBrowser)
		mm, _ := m.refreshNow()
		got := mm.(tuiModel)
		if got.cacheState() != cacheFailed {
			t.Errorf("cacheState = %v, want cacheFailed", got.cacheState())
		}
		if got.status == "" {
			t.Error("status is empty, want the credential problem surfaced")
		}
	})

	t.Run("with credentials it marks the cache as updating", func(t *testing.T) {
		root := t.TempDir()
		st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
		if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
			t.Fatalf("seed secret: %v", err)
		}
		m := withWorkspace(withFleet(withStore(tuiModel{
			ctx:    context.Background(),
			status: "something stale",
		}, st, &config.Config{}), cacheCached, 0, time.Time{}), config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"}, true, "", authBrowser)
		m.spinner = ui.NewSpinner("")

		mm, cmd := m.refreshNow()
		got := mm.(tuiModel)
		if got.cacheState() != cacheUpdating {
			t.Errorf("cacheState = %v, want cacheUpdating", got.cacheState())
		}
		if got.status != "" {
			t.Errorf("status = %q, want it cleared on a new refresh", got.status)
		}
		if cmd == nil {
			t.Error("no command returned, want the refresh started")
		}
	})
}

func TestAltRRefreshesFromTheElementsPage(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	m := withWorkspace(withFleet(withStore(tuiModel{
		ctx:   context.Background(),
		phase: phasePicking,
		width: 100, height: 30,
	}, st, &config.Config{}), cacheCached, 0, time.Time{}), config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"}, true, "", authBrowser)
	m.spinner = ui.NewSpinner("")
	m.filter = newField("", "", false).input
	m.table = newElementTable()

	mm, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModAlt}), "alt+r")
	got := mm.(tuiModel)
	if got.cacheState() != cacheUpdating {
		t.Errorf("cacheState = %v, want alt+r to start a refresh", got.cacheState())
	}
	if cmd == nil {
		t.Error("alt+r issued no command")
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want to stay on the elements page", got.phase)
	}
}

func TestRefreshKeybindIsAdvertised(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	m := withWorkspace(withStore(tuiModel{
		ctx:   context.Background(),
		phase: phasePicking,
		width: 200, height: 40,
	}, st, &config.Config{}), config.Tenant{Name: "Acme Co", TSGID: "1"}, true, "", authBrowser)
	m.spinner = ui.NewSpinner("")
	m.filter = newField("", "", false).input
	m.table = newElementTable()

	if out := m.viewPicking(); !strings.Contains(out, "alt+r refresh") {
		t.Errorf("the elements page does not advertise alt+r:\n%s", out)
	}
}

func TestAFreshCacheFillsTheListWithoutThePortal(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	cfg := &config.Config{Tenants: []config.Tenant{{Name: "Acme Co", TSGID: "1", ClientID: "svc", Added: true}}}
	if err := st.SaveCache(&config.ElementCache{TSGID: "1", FetchedAt: time.Now(),
		Elements: []api.Element{{ID: "e1", Name: "s0100-rtr-01"}}}); err != nil {
		t.Fatal(err)
	}
	m := withWorkspace(withStore(tuiModel{ctx: context.Background(), phase: phaseMenu}, st, cfg),
		cfg.Tenants[0], true, "", authTenant)
	m.spinner = ui.NewSpinner("")

	next, cmd := m.startContinue()
	got := next.(tuiModel)
	if got.phase != phasePicking || len(got.rows()) != 1 {
		t.Fatalf("phase %v with %d rows, want the cached element shown at once", got.phase, len(got.rows()))
	}
	if cmd == nil {
		t.Fatal("no load was started")
	}
	got.runtime().UseConnector(runtime.Connector{})
	if err := got.runtime().Exec(&runtime.Call{Ctx: context.Background()}, "fleet.load"); err != nil {
		t.Errorf("a fresh cache went to the portal: %v", err)
	}
}

func TestLaunchRefreshDoesNotHijackTheMenuWithABrowser(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	m := withWorkspace(withFleet(withStore(tuiModel{
		ctx:   context.Background(),
		phase: phaseMenu,
	}, st, &config.Config{}), cacheUpdating, 0, time.Time{}), config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}, true, "", authBrowser)
	m.spinner = ui.NewSpinner("")

	m.runtime().TokenDied("sign-in expired", false)
	mm, cmd := m.Update(fleetDoneMsg{err: errors.New("1 of 1 tenants failed to load")})
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want to stay on the menu rather than open a browser", got.phase)
	}
	if cmd != nil {
		t.Error("a background refresh opened a browser window")
	}
	if !strings.Contains(got.status, "sign-in required") {
		t.Errorf("status = %q, want it to point at the sign-in entry", got.status)
	}
	if got.signInResume {
		t.Error("signInResume was set by a background refresh")
	}
}

var errTokenTest = errors.New("token refresh: boom")

func TestOpeningTheListWhileALoadRunsStartsNoSecond(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Tenants: []config.Tenant{{Name: "Acme Co", TSGID: "1", ClientID: "svc", Added: true}}}
	m := withWorkspace(withStore(tuiModel{ctx: context.Background(), phase: phaseMenu}, st, cfg),
		cfg.Tenants[0], true, "", authTenant)
	m.spinner = ui.NewSpinner("")
	if _, _, err := m.runtime().BeginRefresh(); err != nil || m.runtime().Fleet().Pending == 0 {
		t.Fatalf("the refresh did not start (%v), so this proves nothing", err)
	}
	_, cmd := m.loadFleet()
	if _, batch := cmd().(tea.BatchMsg); batch {
		t.Error("a second load was started beside the one running")
	}
}
