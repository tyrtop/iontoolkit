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
			if m.cacheState != tt.wantState {
				t.Errorf("cacheState = %v, want %v", m.cacheState, tt.wantState)
			}
			if tt.count > 0 && m.cacheCount != tt.count {
				t.Errorf("cacheCount = %d, want %d", m.cacheCount, tt.count)
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
			m := tuiModel{
				ctx:        context.Background(),
				store:      st,
				cfg:        &config.Config{},
				tenant:     config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"},
				hasTenant:  true,
				cacheState: tt.state,
			}
			m.spinner = ui.NewSpinner("")

			// tea.Batch returns nil when it is given only the spinner tick, so the
			// shape of the returned command distinguishes the two cases.
			got := m.Init()
			if got == nil {
				t.Fatal("Init() = nil, want at least the spinner tick")
			}
			msg := got()
			_, batched := msg.(tea.BatchMsg)
			if batched != tt.wantCmds {
				t.Errorf("Init() produced a batch = %v, want %v", batched, tt.wantCmds)
			}
		})
	}
}

func TestRefreshNow(t *testing.T) {
	t.Run("without a tenant it only reports", func(t *testing.T) {
		m := tuiModel{cfg: &config.Config{}}
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
		m := tuiModel{
			ctx: context.Background(), store: st, cfg: &config.Config{},
			tenant: config.Tenant{Name: "Acme Co", TSGID: "1"}, hasTenant: true,
		}
		mm, _ := m.refreshNow()
		got := mm.(tuiModel)
		if got.cacheState != cacheFailed {
			t.Errorf("cacheState = %v, want cacheFailed", got.cacheState)
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
		m := tuiModel{
			ctx: context.Background(), store: st, cfg: &config.Config{},
			tenant:     config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"},
			hasTenant:  true,
			cacheState: cacheCached,
			status:     "something stale",
		}
		m.spinner = ui.NewSpinner("")

		mm, cmd := m.refreshNow()
		got := mm.(tuiModel)
		if got.cacheState != cacheUpdating {
			t.Errorf("cacheState = %v, want cacheUpdating", got.cacheState)
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
	m := tuiModel{
		ctx: context.Background(), store: st, cfg: &config.Config{},
		tenant:     config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"},
		hasTenant:  true,
		phase:      phasePicking,
		selected:   map[string]bool{},
		cacheState: cacheCached,
		width:      100, height: 30,
	}
	m.spinner = ui.NewSpinner("")
	m.filter = newField("", "", false).input
	m.table = newElementTable()

	mm, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModAlt}), "alt+r")
	got := mm.(tuiModel)
	if got.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want alt+r to start a refresh", got.cacheState)
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
	m := tuiModel{
		ctx: context.Background(), store: st, cfg: &config.Config{},
		tenant:    config.Tenant{Name: "Acme Co", TSGID: "1"},
		hasTenant: true,
		phase:     phasePicking,
		selected:  map[string]bool{},
		width:     200, height: 40,
	}
	m.spinner = ui.NewSpinner("")
	m.filter = newField("", "", false).input
	m.table = newElementTable()

	if out := m.viewPicking(); !strings.Contains(out, "alt+r refresh") {
		t.Errorf("the elements page does not advertise alt+r:\n%s", out)
	}
}

func TestFreshCacheSkipsTheRefetchOnceAuthenticated(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	m := tuiModel{
		ctx: context.Background(), store: st, cfg: &config.Config{},
		tenant: config.Tenant{Name: "Acme Co", TSGID: "1"}, hasTenant: true,
		cacheState: cacheCached,
		phase:      phaseLoading,
		selected:   map[string]bool{},
	}
	m.spinner = ui.NewSpinner("")

	// This client has no refresh loop, so watchToken contributes nothing and a
	// non-nil command means a fetch was started.
	mm, cmd := m.Update(clientReadyMsg{client: &api.Client{}})
	got := mm.(tuiModel)
	if cmd != nil {
		t.Error("a fresh cache still triggered a re-fetch")
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want phasePicking once the client is ready", got.phase)
	}

	// A stale cache must still fetch, which needs a tenant with credentials.
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	m.cfg = &config.Config{Tenants: []config.Tenant{
		{Name: "Acme Co", TSGID: "1", ClientID: "svc", Added: true},
	}}
	m.cacheState = cacheUpdating
	_, cmd = m.Update(clientReadyMsg{client: &api.Client{}})
	if cmd == nil {
		t.Error("a stale cache did not trigger a fetch")
	}
}

func TestLaunchRefreshDoesNotHijackTheMenuWithABrowser(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	m := tuiModel{
		ctx: context.Background(), store: st, cfg: &config.Config{},
		tenant:     config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser},
		hasTenant:  true,
		phase:      phaseMenu,
		cacheState: cacheUpdating,
		selected:   map[string]bool{},
	}
	m.spinner = ui.NewSpinner("")

	mm, cmd := m.Update(needsSignInMsg{})
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

func TestWatchTokenDoesNotBlockWithoutARefreshLoop(t *testing.T) {
	// api.NewClient takes a fixed token and starts no refresh loop, so it has no
	// error channel. Receiving from a nil channel blocks forever, which would
	// strand a goroutine for the whole session.
	if got := watchToken(nil); got != nil {
		t.Error("watchToken(nil) returned a command, which would block on receive")
	}

	ch := make(chan error, 1)
	cmd := watchToken(ch)
	if cmd == nil {
		t.Fatal("watchToken returned nil for a real channel")
	}
	ch <- errTokenTest
	msg, ok := cmd().(tokenErrMsg)
	if !ok {
		t.Fatalf("got %T, want tokenErrMsg", cmd())
	}
	if !errors.Is(msg.err, errTokenTest) {
		t.Errorf("err = %v, want %v", msg.err, errTokenTest)
	}
	if msg.stopped {
		t.Error("stopped = true for a plain error")
	}

	// A closed channel means refreshing gave up for good.
	closed := make(chan error)
	close(closed)
	msg, ok = watchToken(closed)().(tokenErrMsg)
	if !ok {
		t.Fatal("a closed channel did not produce a tokenErrMsg")
	}
	if !msg.stopped {
		t.Error("stopped = false after the channel closed")
	}
}

var errTokenTest = errors.New("token refresh: boom")
