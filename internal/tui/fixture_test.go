package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/fixture"
)

const fixtureYAML = `
tenants:
  - name: Acme Co
    tsg_id: "000000000"
    users:
      - name: admin
        password: ion
    elements:
      - name: store-0003-rtr-01
        model: ion 1200
        site: Store 0003
        connected: true
        responses:
          "dump device status": |
            Serial     : FAKE0001
      - name: store-0100-rtr-01
        site: Store 0100
`

// fixtureTUI is the whole startup path in fixture mode: a seeded store, a real
// client over the fixture transport, and the tenant load the TUI itself runs.
func fixtureTUI(t *testing.T) (tuiModel, *fixture.Set) {
	t.Helper()
	return fixtureTUIWith(t, fixtureYAML)
}

func fixtureTUIWith(t *testing.T, yaml string) (tuiModel, *fixture.Set) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	app.Fixture = set
	t.Cleanup(func() { app.Fixture = nil })

	st, err := app.Store()
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	cfg, err := st.Config()
	if err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(t.Context(), st, cfg)
	m.width, m.height = 100, 30
	if !m.hasTenant {
		t.Fatal("the seeded fixture tenant did not resolve, so the list would never load")
	}

	flags := app.TenantFlags(st, m.tenant, m.ionUser)
	msg := loadTenant(t.Context(), flags, m.tenant, m.pool, st)()
	next, _ := m.Update(msg)
	return next.(tuiModel), set
}

func TestFixtureModeFillsTheElementList(t *testing.T) {
	m, _ := fixtureTUI(t)
	m.phase = phasePicking
	m.rebuildRows()

	if len(m.all) != 2 {
		t.Fatalf("got %d elements, want 2: %+v", len(m.all), m.all)
	}
	got := plain(m.viewPicking())
	for _, want := range []string{"store-0003-rtr-01", "ion 1200", "connected"} {
		if !strings.Contains(got, want) {
			t.Errorf("the list is missing %q:\n%s", want, got)
		}
	}
	// Site names come from the sites endpoint, so a fixture that did not answer
	// it would show a raw 19-digit id here.
	if !strings.Contains(got, "Store 0003") {
		t.Errorf("site name unresolved:\n%s", got)
	}
	if strings.Contains(got, m.all[0].SiteID) {
		t.Errorf("a raw site id is on screen:\n%s", got)
	}
}

func TestFixtureModeOpensASessionThroughTheRealPane(t *testing.T) {
	m, set := fixtureTUI(t)
	el := set.Tenants[0].Elements[0]
	t.Cleanup(m.sessions.CloseAll)

	next, cmd := m.openSession(el.ID, el.Name, m.tenant.TSGID)
	m = next.(tuiModel)
	if cmd == nil {
		t.Fatal("openSession queued nothing")
	}
	// The command dials and opens; running it is what a real tick would do.
	runCmd(t, cmd)

	waitForTUI(t, func() bool {
		s := m.sessions.Find(el.ID)
		return s != nil && strings.Contains(s.Render(), el.Name+"#")
	})

	s := m.sessions.Find(el.ID)
	if s == nil {
		t.Fatal("no session was opened")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("session error = %v", err)
	}
	// Autologin typed both answers; a pane still sitting on the password prompt
	// is the failure this is here to catch.
	if got := s.Render(); strings.Contains(got, "Login incorrect") {
		t.Errorf("autologin was rejected:\n%s", got)
	}

	s.SendText("dump device status\r")
	waitForTUI(t, func() bool { return strings.Contains(s.Render(), "FAKE0001") })
}

// runCmd runs a tea.Cmd, and any batch it is made of, the way the runtime does.
func runCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			go c()
		}
	}
}

func waitForTUI(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the fixture device")
}
