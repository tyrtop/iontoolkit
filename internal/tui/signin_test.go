package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

func signInModel(t *testing.T, tenant config.Tenant) tuiModel {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg := &config.Config{Tenants: []config.Tenant{tenant}, LastTenant: tenant.Name}
	return tuiModel{
		cfg:       cfg,
		store:     st,
		tenant:    tenant,
		hasTenant: true,
		selected:  map[string]bool{},
		width:     80,
		height:    24,
	}
}

func menuLabels(m tuiModel) []string {
	var out []string
	for _, e := range m.menuEntries() {
		out = append(out, e.label)
	}
	return out
}

func TestSignInEntryIsAlwaysOffered(t *testing.T) {
	// It is the way in to the tenant list, not a per-tenant re-auth, so it does
	// not depend on which tenant happens to be active.
	for _, tenant := range []config.Tenant{
		{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser},
		{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount},
		{},
	} {
		m := signInModel(t, tenant)
		var found bool
		for _, e := range m.menuEntries() {
			if e.action == actSignIn {
				found = true
			}
		}
		if !found {
			t.Errorf("no sign-in entry for tenant %+v (menu: %v)", tenant, menuLabels(m))
		}
	}
}

func TestBrowserTenantIsSavedWithoutAClientSecret(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Old", TSGID: "9"})
	mm, _ := m.openTenantForm(config.AuthBrowser)
	m = mm.(tuiModel)

	m.form[0].input.SetValue("Acme Co")
	m.form[1].input.SetValue("1984021017")

	mm, _ = m.submitForm()
	m = mm.(tuiModel)
	if m.status != "" {
		t.Fatalf("status = %q, want the form to submit cleanly", m.status)
	}

	saved, ok := m.cfg.Tenant("Acme Co")
	if !ok {
		t.Fatal("tenant was not saved")
	}
	if !saved.UsesBrowser() {
		t.Errorf("Auth = %q, want %q", saved.Auth, config.AuthBrowser)
	}
	if saved.ClientID != "" {
		t.Errorf("ClientID = %q, want empty for a browser tenant", saved.ClientID)
	}
	if v, err := m.store.Secrets.Get(config.ClientSecretKey("1984021017")); err == nil && v != "" {
		t.Errorf("a client secret was stored for a browser tenant: %q", v)
	}
}

func TestNeedsSignInOpensTheWaitingScreen(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseLoading

	mm, cmd := m.Update(needsSignInMsg{})
	got := mm.(tuiModel)
	if got.phase != phaseSignIn {
		t.Fatalf("phase = %v, want phaseSignIn", got.phase)
	}
	if !got.signInResume {
		t.Error("signInResume = false, want the interrupted connect to resume after signing in")
	}
	if cmd == nil {
		t.Error("no command returned, want the browser to be opened")
	}
}

func TestSignInWaitingScreenSaysWhatItIsWaitingFor(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.spinner = ui.NewSpinner("")

	out := m.viewSignIn()
	if !strings.Contains(out, "waiting for browser") {
		t.Errorf("viewSignIn() = %q, want it to say what it is waiting for", out)
	}
	if !strings.Contains(out, "Acme Co") {
		t.Error("viewSignIn() does not name the tenant being signed in to")
	}
	if !m.wantsSpinner() {
		t.Error("wantsSpinner() = false, want the spinner animating while waiting")
	}
	t.Log("\n" + out)
}

func TestSignedInLandsOnAConfirmation(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.spinner = ui.NewSpinner("")

	// An explicit sign-in has nothing to replay, so the result is shown.
	mm, cmd := m.Update(signedInMsg{
		client: liveClient(),
		ident:  saseauth.Identity{Name: "Ben Hall", Email: "ben@example.test", TSG: "1"},
		ttl:    899 * time.Second,
	})
	got := mm.(tuiModel)
	if got.phase != phaseSignedIn {
		t.Fatalf("phase = %v, want phaseSignedIn so the result is actually visible", got.phase)
	}
	if got.client == nil {
		t.Error("the verified client was discarded, so the next step would authenticate again")
	}
	if got.ident.Name != "Ben Hall" {
		t.Errorf("ident.Name = %q, want it kept for the confirmation", got.ident.Name)
	}
	if got.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want elements fetched behind the confirmation", got.cacheState)
	}
	if cmd == nil {
		t.Error("no command returned, want the element fetch started")
	}
}

func TestSignInInTheMiddleOfAnActionSkipsTheConfirmation(t *testing.T) {
	t.Run("an interrupted element list resumes straight away", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phaseSignIn
		m.signInResume = true
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}

		mm, _ := m.Update(signedInMsg{client: liveClient()})
		got := mm.(tuiModel)
		if got.phase == phaseSignedIn {
			t.Error("a mid-action sign-in stopped to show a confirmation")
		}
		if got.signInResume {
			t.Error("signInResume stayed set after resuming")
		}
	})

	t.Run("interrupted sessions are reopened", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phaseSignIn
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}
		m.sessions = ionterm.NewManager()
		m.resumeSessions = []sessionTarget{{eid: "e1", name: "s0100-rtr-01", tsg: "1"}}
		setTestClient(&m, liveClient())

		mm, cmd := m.Update(signedInMsg{client: liveClient()})
		got := mm.(tuiModel)
		if got.phase == phaseSignedIn {
			t.Error("a mid-action sign-in stopped to show a confirmation")
		}
		if len(got.resumeSessions) != 0 {
			t.Error("resumeSessions was not consumed, so it would replay again")
		}
		if got.connecting != "s0100-rtr-01" {
			t.Errorf("connecting = %q, want the interrupted session reopened", got.connecting)
		}
		if cmd == nil {
			t.Error("no command returned, want the session dial started")
		}
	})
}

func TestDismissingTheConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		resume    bool
		wantPhase tuiPhase
	}{
		{"an explicit sign-in returns to the menu", false, phaseMenu},
		// A browser tenant needs no stored secret to validate, so resuming goes
		// on to load elements rather than bouncing back to the menu.
		{"an interrupted action resumes", true, phaseLoading},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
			m.phase = phaseSignedIn
			m.signInResume = tt.resume
			m.spinner = ui.NewSpinner("")
			m.selected = map[string]bool{}

			mm, _ := m.onKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			got := mm.(tuiModel)
			if got.phase == phaseSignedIn {
				t.Error("enter left the confirmation on screen")
			}
			if got.phase != tt.wantPhase {
				t.Errorf("phase = %v, want %v", got.phase, tt.wantPhase)
			}
			if got.signInResume {
				t.Error("signInResume stayed set after the confirmation was dismissed")
			}
		})
	}
}

func TestSignedInConfirmationReportsWhatWasVerified(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1984021017", Auth: config.AuthBrowser})
	m.phase = phaseSignedIn
	m.spinner = ui.NewSpinner("")
	m.width, m.height = 100, 30
	m.ident = saseauth.Identity{
		Name:           "Ben Hall",
		Email:          "ben@example.test",
		TSG:            "1984021017",
		SessionExpires: time.Now().Add(8 * time.Hour),
	}
	m.profile = api.Profile{Region: "hood", Roles: []string{"tenant_super"}}
	m.tokenTTL = 899 * time.Second
	m.cacheState, m.cacheCount, m.cacheAt = cacheUpdated, 5613, time.Now()

	out := plain(m.viewSignedIn())
	for _, want := range []string{
		"signed in to Acme Co",
		"Ben Hall <ben@example.test>",
		"1984021017",
		"hood",
		"tenant_super",
		"no browser needed for 7h59m",
		"5613 elements cached",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewSignedIn())
}

func TestSignedInConfirmationWhenTheTokenIsSilentAboutExpiry(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignedIn
	m.spinner = ui.NewSpinner("")
	m.width, m.height = 100, 30
	m.ident = saseauth.Identity{Name: "Ben Hall"}

	out := plain(m.viewSignedIn())
	if !strings.Contains(out, "not stated") {
		t.Errorf("want the unknown session lifetime said plainly rather than guessed:\n%s", out)
	}
}

func TestSignInFailureReturnsToTheMenu(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.signInResume = true

	mm, _ := m.Update(signedInMsg{err: errors.New("browser closed before sign-in completed")})
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want phaseMenu", got.phase)
	}
	if !strings.Contains(got.status, "browser closed") {
		t.Errorf("status = %q, want the browser error surfaced", got.status)
	}
	if got.signInResume {
		t.Error("signInResume stayed set after a failed sign-in, so a later success would connect unexpectedly")
	}
}

func TestEscStopsWaitingForTheBrowser(t *testing.T) {
	m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
	m.phase = phaseSignIn
	m.signInResume = true

	mm, _ := m.onKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want phaseMenu", got.phase)
	}
	if got.signInResume {
		t.Error("signInResume stayed set after cancelling")
	}
}

func TestEditingATenantKeepsItsRefreshToken(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "rt-for-tenant"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}

	tenant := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	m := signInModel(t, tenant)
	m.store = st

	mm, _ := m.openEditTenantForm(tenant)
	m = mm.(tuiModel)
	if len(m.form) != 2 {
		t.Fatalf("edit form has %d fields, want 2 for a browser tenant", len(m.form))
	}
	m.form[1].input.SetValue("2")

	mm, _ = m.submitForm()
	m = mm.(tuiModel)

	if v, err := st.Secrets.Get(config.RefreshTokenKey("2")); err != nil || v != "rt-for-tenant" {
		t.Errorf("refresh token under the new tsg = %q (err %v), want it migrated", v, err)
	}
	if v, _ := st.Secrets.Get(config.RefreshTokenKey("1")); v != "" {
		t.Errorf("refresh token still under the old tsg: %q", v)
	}
}

func TestForgettingATenantDropsItsRefreshToken(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	tenant := config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser}
	if err := st.Secrets.Set(config.RefreshTokenKey("1"), "rt"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &config.Config{Tenants: []config.Tenant{tenant}}
	if err := forgetTenant(st, cfg, tenant); err != nil {
		t.Fatalf("forgetTenant() error = %v", err)
	}
	if v, _ := st.Secrets.Get(config.RefreshTokenKey("1")); v != "" {
		t.Errorf("refresh token survived forgetTenant: %q", v)
	}
}

func TestLabelKeysFitTheGutter(t *testing.T) {
	// Every key rendered through ui.Label() has to fit ui.LabelWidth, or it collides
	// with its value instead of being padded away from it.
	keys := []string{
		"account", "tenant", "region", "roles", "api token", "sign-in",
		"spinner", "commands", "elements", "secrets",
		"format", "output", "folder",
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			if len(k) > ui.LabelWidth {
				t.Fatalf("key %q is %d chars, want at most %d", k, len(k), ui.LabelWidth)
			}
			if got := ansi.StringWidth(plain(ui.Label(k))); got != ui.LabelWidth {
				t.Errorf("ui.Label(%q) renders %d columns, want exactly %d", k, got, ui.LabelWidth)
			}
		})
	}
}

func TestMenuKeybinds(t *testing.T) {
	t.Run("alt+w quits from the menu", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
		m.phase = phaseMenu
		_, cmd := m.onMenuKey("alt+w")
		if cmd == nil {
			t.Fatal("alt+w returned no command, want a quit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("alt+w produced %T, want tea.QuitMsg", cmd())
		}
	})

	t.Run("alt+w is advertised on the menu", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
		m.width, m.height = 100, 30
		m.spinner = ui.NewSpinner("")
		if out := plain(m.viewMenu()); !strings.Contains(out, "alt+w quit") {
			t.Errorf("the menu does not advertise alt+w:\n%s", out)
		}
	})

	t.Run("alt+t with nothing to reattach says so", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
		m.phase = phaseMenu
		m.sessions = ionterm.NewManager()
		mm, _ := m.onMenuKey("alt+t")
		got := mm.(tuiModel)
		if !strings.Contains(got.status, "nothing to reattach") {
			t.Errorf("status = %q, want it to say there is nothing to reattach", got.status)
		}
		if got.phase != phaseMenu {
			t.Errorf("phase = %v, want to stay on the menu", got.phase)
		}
	})

	t.Run("alt+t restores the elements list behind tab 1", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1"})
		m.phase = phaseMenu
		m.sessions = ionterm.NewManager()
		m.selected = map[string]bool{}
		m.table = newElementTable()
		m.filter = newField("", "", false).input
		m.width, m.height = 100, 30

		m.cfg = &config.Config{Tenants: []config.Tenant{
			{Name: "Acme Co", TSGID: "1", Added: true},
		}}
		els := []api.Element{{ID: "e1", Name: "s0100-rtr-01"}, {ID: "e2", Name: "s0100-rtr-02"}}
		if err := m.store.SaveCache(&config.ElementCache{TSGID: "1", FetchedAt: time.Now(), Elements: els}); err != nil {
			t.Fatalf("SaveCache() error = %v", err)
		}

		mm, _ := m.onMenuKey("alt+t")
		got := mm.(tuiModel)
		// Nothing was open to reattach to, but the elements that own tab 1 must
		// still have been restored, or a reattach would land on an empty list.
		if len(got.all) != len(els) {
			t.Errorf("loaded %d elements, want %d restored from cache", len(got.all), len(els))
		}
	})
}

func TestExpiredTokenReopensTheBrowserAtTheActionThatFailed(t *testing.T) {
	t.Run("a rejected dial re-auths and replays the same elements", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phaseSession
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}
		m.sessions = ionterm.NewManager()
		m.lastTargets = []sessionTarget{{eid: "e1", name: "s0100-rtr-01", tsg: "1"}, {eid: "e2", name: "s0100-rtr-02", tsg: "1"}}

		mm, cmd := m.Update(sessionErrMsg{
			target: sessionTarget{eid: "e1", name: "s0100-rtr-01", tsg: "1"},
			err:    "dial: failed to WebSocket dial: expected handshake response status code 101 but got 401",
		})
		got := mm.(tuiModel)
		if got.phase != phaseSignIn {
			t.Fatalf("phase = %v, want phaseSignIn so the browser opens", got.phase)
		}
		if len(got.resumeSessions) != 2 {
			t.Errorf("resumeSessions = %v, want both interrupted elements queued", got.resumeSessions)
		}
		if cmd == nil {
			t.Error("no command returned, want the browser opened")
		}
	})

	t.Run("an ordinary dial failure is left alone", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phaseSession
		m.sessions = ionterm.NewManager()
		m.lastTargets = []sessionTarget{{eid: "e1", name: "s0100-rtr-01", tsg: "1"}}

		mm, _ := m.Update(sessionErrMsg{
			target: sessionTarget{eid: "e1", name: "s0100-rtr-01", tsg: "1"},
			err:    "element e1: run: login failed after 1 attempt",
		})
		got := mm.(tuiModel)
		if got.phase == phaseSignIn {
			t.Error("a device-side failure opened a browser")
		}
		if len(got.resumeSessions) != 0 {
			t.Error("a device-side failure queued a replay")
		}
	})

	t.Run("a rejected element fetch re-auths and resumes", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phaseLoading
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}

		mm, cmd := m.Update(elementsErrMsg("status 401 Unauthorized: "))
		got := mm.(tuiModel)
		if got.phase != phaseSignIn {
			t.Fatalf("phase = %v, want phaseSignIn", got.phase)
		}
		if !got.signInResume {
			t.Error("signInResume = false, want the list reload queued")
		}
		if cmd == nil {
			t.Error("no command returned, want the browser opened")
		}
	})

	t.Run("a known-dead credential re-auths before dialling at all", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthBrowser})
		m.phase = phasePicking
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}
		m.sessions = ionterm.NewManager()
		setTestClient(&m, liveClient())
		m.tokenDead = "sign-in expired"

		mm, cmd := m.openSessions([]sessionTarget{{eid: "e1", name: "s0100-rtr-01", tsg: "1"}})
		got := mm.(tuiModel)
		if got.phase != phaseSignIn {
			t.Fatalf("phase = %v, want the browser opened instead of a doomed dial", got.phase)
		}
		if got.connecting != "" {
			t.Errorf("connecting = %q, want no dial attempted", got.connecting)
		}
		if len(got.resumeSessions) != 1 {
			t.Errorf("resumeSessions = %v, want the request queued", got.resumeSessions)
		}
		if cmd == nil {
			t.Error("no command returned")
		}
	})

	t.Run("a service account tenant never gets a browser", func(t *testing.T) {
		m := signInModel(t, config.Tenant{Name: "Acme Co", TSGID: "1", Auth: config.AuthServiceAccount})
		m.phase = phasePicking
		m.spinner = ui.NewSpinner("")
		m.selected = map[string]bool{}
		m.sessions = ionterm.NewManager()
		setTestClient(&m, liveClient())
		m.tokenDead = "sign-in expired"

		mm, _ := m.openSessions([]sessionTarget{{eid: "e1", name: "s0100-rtr-01", tsg: "1"}})
		got := mm.(tuiModel)
		if got.phase == phaseSignIn {
			t.Error("a service account tenant was sent to a browser sign-in")
		}
	})
}
