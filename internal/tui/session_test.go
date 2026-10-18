package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
)

func TestSessionGeometryAsksBigByDefault(t *testing.T) {
	m := tuiModel{cfg: &config.Config{}}
	cols, rows := m.sessionGeometry()
	if cols != defaultSessionCols || rows != defaultSessionRows {
		t.Errorf("default geometry = %dx%d, want %dx%d", cols, rows, defaultSessionCols, defaultSessionRows)
	}

	m.width, m.height = 100, 30
	pc, pr := m.paneSize()
	if pc >= cols {
		t.Errorf("pane %d should be smaller than the dialed buffer %d", pc, cols)
	}
	if pr >= rows {
		t.Errorf("pane rows %d should be smaller than the dialed buffer %d", pr, rows)
	}
}

func TestSessionGeometryHonorsConfig(t *testing.T) {
	m := tuiModel{cfg: &config.Config{SessionCols: 320, SessionRows: 80}}
	if cols, rows := m.sessionGeometry(); cols != 320 || rows != 80 {
		t.Errorf("geometry = %dx%d, want 320x80", cols, rows)
	}
}

func TestPaneSizeClampsOnTinyTerminals(t *testing.T) {
	m := tuiModel{cfg: &config.Config{}, width: 2, height: 1}
	cols, rows := m.paneSize()
	if cols < 20 || rows < 3 {
		t.Errorf("paneSize = %dx%d, want clamped to a drawable minimum", cols, rows)
	}
}

func TestFrameNeverExceedsTerminalHeight(t *testing.T) {
	for _, h := range []int{6, 10, 24, 60} {
		for _, sl := range []slashMode{slashOff, slashHelp, slashDebug, slashCommands} {
			m := pickModel(t)
			m.width, m.height = 80, h
			m.phase = phaseSession
			m.slash = sl

			content, _ := m.renderSession()
			if n := len(strings.Split(ui.ClipFrame(content, h), "\n")); n > h {
				t.Errorf("height %d slash %d: frame is %d lines", h, sl, n)
			}
		}
	}
}

func TestEnterWithNoSelectionOpensASession(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, &api.Client{})

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := mm.(tuiModel)

	if got.phase != phaseSession {
		t.Fatalf("phase = %v, want phaseSession", got.phase)
	}
	if got.connecting != "S0100-RTR-01" {
		t.Errorf("connecting = %q, want the highlighted element name", got.connecting)
	}
	if cmd == nil {
		t.Error("want a dial command")
	}
}

func TestEnterWithoutClientReportsRatherThanPanics(t *testing.T) {
	m := pickModel(t)
	m.client = nil

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := mm.(tuiModel)

	if got.status == "" {
		t.Error("want a status message when there is no client")
	}
	if got.phase == phaseSession {
		t.Error("should not enter a session without a client")
	}
}

func TestCtrlCQuitsEverywhereExceptInSession(t *testing.T) {
	m := pickModel(t)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Error("ctrl+c in the picker should quit")
	}

	m.phase = phaseSession
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if got := mm.(tuiModel).phase; got != phaseSession {
		t.Errorf("ctrl+c left the session, phase = %v — it must reach the device", got)
	}
}

func TestSessionKeysDoNotLeakToTheModel(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	for _, k := range []rune{'q', 'a', '/'} {
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)}))
		got := mm.(tuiModel)
		if got.phase != phaseSession {
			t.Errorf("%q changed phase to %v; session keys belong to the device", k, got.phase)
		}
		if got.filterOn {
			t.Errorf("%q switched on the filter inside a session", k)
		}
	}
}

func TestSessionEscapeHatches(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ']', Mod: tea.ModCtrl}))
	if got := mm.(tuiModel).phase; got != phasePicking {
		t.Errorf("ctrl+] phase = %v, want phasePicking", got)
	}
}

func TestTabBarAlwaysHasElementsTab(t *testing.T) {
	m := pickModel(t)

	got := plain(m.tabBar())
	if !strings.Contains(got, "1 elements") {
		t.Errorf("tabBar = %q, want an elements tab", got)
	}
	if m.currentTab() != 0 {
		t.Errorf("currentTab() = %d in the picker, want 0", m.currentTab())
	}
	if m.tabCount() != 1 {
		t.Errorf("tabCount() = %d with no sessions, want 1", m.tabCount())
	}
}

func TestSelectTabWrapsThroughElements(t *testing.T) {
	m := pickModel(t)

	m2 := m.selectTab(1)
	if m2.phase != phasePicking {
		t.Errorf("with no sessions, every tab is the elements tab; phase = %v", m2.phase)
	}

	m3 := m.selectTab(-1)
	if m3.phase != phasePicking {
		t.Errorf("negative index should wrap, phase = %v", m3.phase)
	}
}

func TestNilManagerBehavesAsEmpty(t *testing.T) {
	m := tuiModel{cfg: &config.Config{}}
	if m.tabCount() != 1 {
		t.Errorf("tabCount() = %d with a nil manager, want 1", m.tabCount())
	}
	if got := plain(m.tabBar()); !strings.Contains(got, "1 elements") {
		t.Errorf("tabBar = %q", got)
	}
	m.sessions.CloseAll()
	m.sessions.Next()
}

func TestRenderSessionConnecting(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession
	m.connecting = "S0100-RTR-01"
	t.Log("\n" + m.viewSession())

	if got := plain(m.viewSession()); !strings.Contains(got, "connecting to S0100-RTR-01") {
		t.Errorf("view = %q", got)
	}
}

func TestSlashOnlyOpensOnAnEmptyLine(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	if m.slash != slashOff {
		t.Fatal("slash should start closed")
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	if got := mm.(tuiModel).slash; got != slashOff {
		t.Errorf("slash opened with no active session, mode = %d", got)
	}
}

func TestSlashMenuFiltersAndNavigates(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := mm.(tuiModel)
	if s.slash != slashMenu {
		t.Fatalf("mode = %d, want slashMenu", s.slash)
	}
	if len(s.slashItems) != len(slashVerbs) {
		t.Errorf("menu has %d items, want %d", len(s.slashItems), len(slashVerbs))
	}

	s2 := typeString(t, s, "c")
	if len(s2.slashItems) == 0 || s2.slashItems[0] != "commands" {
		t.Errorf("typing c gave %v, want commands first", s2.slashItems)
	}

	mm, _ = s2.activateSlash()
	s3 := mm.(tuiModel)
	if s3.slash != slashCommands {
		t.Fatalf("mode = %d, want slashCommands", s3.slash)
	}
	if len(s3.slashItems) < 100 {
		t.Errorf("catalog has %d entries, want the full read-only set", len(s3.slashItems))
	}
	for _, c := range s3.slashItems {
		if !ioncli.IsReadOnly(c) {
			t.Fatalf("write command %q offered while the guard is on", c)
			break
		}
	}
}

func TestSlashCommandsOffersWritesWhenAllowed(t *testing.T) {
	m := pickModel(t)
	m.cfg.AllowWrites = true

	all := m.catalog()
	found := false
	for _, c := range all {
		if !ioncli.IsReadOnly(c) {
			found = true
			break
		}
	}
	if !found {
		t.Error("AllowWrites should widen the catalog to include write commands")
	}
}

func TestSlashEscapeBacksOutOfSubviews(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession
	mm, _ := m.openSlash()
	s := mm.(tuiModel)

	s.slash = slashHelp
	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}), "esc")
	if got := mm.(tuiModel).slash; got != slashMenu {
		t.Errorf("esc from help gave mode %d, want slashMenu", got)
	}

	mm, _ = mm.(tuiModel).onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}), "esc")
	if got := mm.(tuiModel).slash; got != slashOff {
		t.Errorf("esc from menu gave mode %d, want closed", got)
	}
}

func TestSpinnerOnlyTicksWhereVisible(t *testing.T) {
	m := pickModel(t)

	cases := map[tuiPhase]bool{
		phaseLoading:  true,
		phaseRunning:  true,
		phaseSettings: true,
		phasePicking:  false,
		phaseResults:  false,
		phaseSession:  false,
	}
	for phase, want := range cases {
		m.phase = phase
		m.connecting = ""
		m.cacheState = cacheUpdated
		if got := m.wantsSpinner(); got != want {
			t.Errorf("phase %d wantsSpinner = %v, want %v", phase, got, want)
		}
	}

	// The element list is the exception: it carries a "refreshing" marker, so
	// the spinner has to tick there while a refresh is in flight and stop once
	// it is not.
	m.phase = phasePicking
	m.cacheState = cacheUpdating
	if !m.wantsSpinner() {
		t.Error("the element list does not tick while refreshing, so its marker would sit still")
	}
	m.cacheState = cacheCached
	if m.wantsSpinner() {
		t.Error("the element list keeps ticking when idle")
	}

	m.phase = phaseSession
	m.connecting = "S0100-RTR-01"
	if !m.wantsSpinner() {
		t.Error("a connecting session should animate")
	}
}

func TestRenderSlashViews(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession
	m.width, m.height = 80, 24

	mm, _ := m.openSlash()
	s := mm.(tuiModel)
	t.Log("\n--- menu ---\n" + s.viewSlash())

	s.slash = slashHelp
	t.Log("\n--- help ---\n" + s.viewSlash())

	s.slash = slashDebug
	t.Log("\n--- debug ---\n" + s.viewSlash())

	s.slash = slashCommands
	s = s.refilterSlash()
	s.slashInput.SetValue("dump int stat")
	s = s.refilterSlash()
	t.Log("\n--- commands ---\n" + s.viewSlash())
}

func TestShellModeEnterOpensOneTabPerSelection(t *testing.T) {
	m := pickModel(t)
	m.mode = modeShell
	setTestClient(&m, &api.Client{})

	for i := 0; i < 2; i++ {
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
		m = mm.(tuiModel)
		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		m = mm.(tuiModel)
	}
	if len(m.selected) != 2 {
		t.Fatalf("selected %d, want 2", len(m.selected))
	}
	if got := len(m.pickTargets()); got != 2 {
		t.Errorf("pickTargets() = %d, want 2", got)
	}

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := mm.(tuiModel)
	if got.phase != phaseSession {
		t.Errorf("phase = %v, want phaseSession in shell mode", got.phase)
	}
	if cmd == nil {
		t.Error("want dial commands")
	}
	if got.connecting != "2 sessions" {
		t.Errorf("connecting = %q, want 2 sessions", got.connecting)
	}
}

func TestModeToggle(t *testing.T) {
	m := pickModel(t)
	if m.mode != modeShell {
		t.Fatalf("default mode = %v, want shell", m.mode)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModAlt}))
	m2 := mm.(tuiModel)
	if m2.mode != modeBatch {
		t.Errorf("after alt+m mode = %v, want batch", m2.mode)
	}

	mm, _ = m2.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModAlt}))
	if got := mm.(tuiModel).mode; got != modeShell {
		t.Errorf("toggling back gave %v, want shell", got)
	}
}

func TestPickTargetsFallsBackToTheHighlightedElement(t *testing.T) {
	m := pickModel(t)
	got := m.pickTargets()
	if len(got) != 1 {
		t.Fatalf("pickTargets() = %d with nothing selected, want 1", len(got))
	}
	if got[0].name != "S0100-RTR-01" {
		t.Errorf("target = %q, want the highlighted element", got[0].name)
	}
}

func TestTabColorsAreNeutral(t *testing.T) {
	reserved := map[string]string{"1": "red", "2": "green", "3": "yellow", "9": "bright red", "10": "bright green", "11": "bright yellow"}
	for _, c := range tabColors {
		if name, bad := reserved[c]; bad {
			t.Errorf("tab palette uses %s (%s), which carries status meaning", c, name)
		}
	}
	if len(tabColors) < 4 {
		t.Errorf("palette has %d colors, want enough to tell tabs apart", len(tabColors))
	}
}

func TestFirstLoginAsksForCredentialsThenProceeds(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{Name: "t", TSGID: "1"}}}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.phase = phasePicking
	m.mode = modeShell
	setTestClient(&m, &api.Client{})
	m.setElements([]elementRow{{Element: api.Element{ID: "1", Name: "S0100-RTR-01"}, TSG: "1", Tenant: "Acme Co"}})

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	f := mm.(tuiModel)
	if f.phase != phasePassword {
		t.Fatalf("phase = %v, want the credential form on first login", f.phase)
	}
	if len(f.form) != 2 {
		t.Fatalf("form has %d fields, want user and password", len(f.form))
	}

	f = typeString(t, f, "cisops")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	f = mm.(tuiModel)
	f = typeString(t, f, "pw")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if done.phase != phaseSession {
		t.Errorf("phase = %v, want the shell to open after saving credentials", done.phase)
	}
	if got, err := st.Secrets.Get(config.IONPassKey("1", "cisops")); err != nil || got != "pw" {
		t.Errorf("password not stored: %q %v", got, err)
	}

	m2 := newTUIModel(context.Background(), st, cfg)
	m2.tenant, m2.hasTenant = done.tenant, true
	m2.ionUser = "cisops"
	if app.NeedsIONPassword(st, m2.tenant, m2.ionUser) {
		t.Error("second login should not ask again")
	}
}

func TestUserMenuOpensFromElementsTab(t *testing.T) {
	m := pickModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModAlt}))
	um := mm.(tuiModel)
	if um.phase != phaseUsers {
		t.Fatalf("alt+u phase = %v, want phaseUsers", um.phase)
	}
	if got := plain(um.viewUsers()); !strings.Contains(got, "ion users") {
		t.Errorf("view = %q", got)
	}

	mm, _ = um.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phasePicking {
		t.Errorf("esc phase = %v, want phasePicking", got)
	}
}

func TestUserMenuSelectsRunAsUser(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "t", TSGID: "1",
		Users: []config.IONUser{{Name: "readonly", Default: true}, {Name: "cisops"}},
	}}}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = "readonly"
	m.phase = phaseUsers

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mm.(tuiModel)
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := mm.(tuiModel)

	if got.ionUser != "cisops" {
		t.Errorf("ionUser = %q, want cisops", got.ionUser)
	}
	if got.phase != phaseUsers {
		t.Errorf("phase = %v, want to stay in the panel for repeated switches", got.phase)
	}

	mm, _ = got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if p := mm.(tuiModel).phase; p != phasePicking {
		t.Errorf("esc phase = %v, want phasePicking", p)
	}
}

func TestUserMenuShowsWhetherAPasswordIsSaved(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "t", TSGID: "1",
		Users: []config.IONUser{{Name: "saved", Default: true}, {Name: "bare"}},
	}}}
	if err := st.Secrets.Set(config.IONPassKey("1", "saved"), "pw"); err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.phase = phaseUsers

	got := plain(m.viewUsers())
	if !strings.Contains(got, "saved") || !strings.Contains(got, "no password") {
		t.Errorf("view should distinguish stored from missing passwords:\n%s", got)
	}
}

func TestSessionCredentialsComeFromTheStore(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "t", TSGID: "1", Users: []config.IONUser{{Name: "cisops", Default: true}},
	}}}
	if err := st.Secrets.Set(config.IONPassKey("1", "cisops"), "s3cret"); err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = "cisops"

	creds := m.credsFor("1")
	if creds.User != "cisops" || creds.Pass != "s3cret" {
		t.Errorf("sessionCreds = %+v, want the stored pair", creds)
	}
}

func TestReopenWorksFromTheElementsList(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "t", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}},
	}}}
	if err := st.Secrets.Set(config.IONPassKey("1", "u"), "pw"); err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = "u"
	setTestClient(&m, &api.Client{})
	m.phase = phasePicking
	m.setElements([]elementRow{{Element: api.Element{ID: "1", Name: "S0100-RTR-01"}, TSG: "1", Tenant: "Acme Co"}})

	live, err := m.sessions.Open(context.Background(), stubDialer{}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if m.sessions.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", m.sessions.Count())
	}

	m.phase = phaseSession
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModAlt}))
	closed := mm.(tuiModel)
	if closed.sessions.Count() != 0 {
		t.Fatalf("alt+w left %d visible sessions", closed.sessions.Count())
	}
	if closed.sessions.DetachedCount() != 1 {
		t.Fatalf("DetachedCount() = %d, want 1 — alt+w should detach, not close", closed.sessions.DetachedCount())
	}
	if live.Closed() {
		t.Error("detaching closed the session; it must stay alive in the background")
	}
	if closed.phase != phasePicking {
		t.Fatalf("closing the last tab left phase %v, want phasePicking", closed.phase)
	}

	mm, _ = closed.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModAlt}))
	reopened := mm.(tuiModel)
	if reopened.phase != phaseSession {
		t.Errorf("alt+t from the elements list gave phase %v, want phaseSession", reopened.phase)
	}
	if reopened.sessions.Count() != 1 {
		t.Fatalf("Count() = %d after reattach, want 1", reopened.sessions.Count())
	}
	if reopened.connecting != "" {
		t.Errorf("connecting = %q; reattaching a live session must not redial", reopened.connecting)
	}
	if reopened.sessions.Active() != live {
		t.Error("reattach produced a different session; the original should have stayed alive")
	}
}

func TestReopenWithNothingClosedReports(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModAlt}))
	got := mm.(tuiModel)

	if got.status == "" {
		t.Error("want a status message when there is no closed tab")
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want to stay in the picker", got.phase)
	}
}

func TestReopenFocusesAnAlreadyOpenTab(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, &api.Client{})

	for _, eid := range []string{"1", "2"} {
		if _, err := m.sessions.Open(context.Background(), stubDialer{}, eid, "e"+eid, 200, 50, ionterm.Credentials{}); err != nil {
			t.Fatal(err)
		}
	}
	m.sessions.Select(0)
	m.sessions.CloseActive()

	if _, err := m.sessions.Open(context.Background(), stubDialer{}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.reopenLastTab()
	got := mm.(tuiModel)
	if got.sessions.Count() != 2 {
		t.Errorf("Count() = %d, want 2 — reopen must not duplicate", got.sessions.Count())
	}
	if got.phase != phaseSession {
		t.Errorf("phase = %v, want phaseSession", got.phase)
	}
}

type stubDialer struct{}

func (stubDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return stubConn{}, nil
}

type stubConn struct{}

func (stubConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}
func (stubConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error { return nil }
func (stubConn) Close(code websocket.StatusCode, reason string) error                 { return nil }

func TestAltWOnElementsReturnsToTheMenu(t *testing.T) {
	// alt+w closes whatever you are in. On the element list that is the list,
	// so it falls back to the menu; only the menu's alt+w leaves the app.
	m := pickModel(t)

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModAlt}))
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want the menu", got.phase)
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("alt+w on the element list quit the app")
		}
	}

	hint := plain(m.viewPicking())
	if !strings.Contains(hint, "alt+w menu") {
		t.Errorf("the element list does not advertise alt+w as the way back:\n%s", hint)
	}
}

type recConn struct {
	mu      sync.Mutex
	written []byte
	done    chan struct{}
	once    sync.Once
}

func newRecConn() *recConn { return &recConn{done: make(chan struct{})} }

func (c *recConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-c.done:
		return 0, nil, context.Canceled
	}
}

func (c *recConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written = append(c.written, p...)
	return nil
}

func (c *recConn) Close(code websocket.StatusCode, reason string) error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *recConn) sent() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.written)
}

type recDialer struct{ conn *recConn }

func (d recDialer) DialToolkit(ctx context.Context, eid string, cols, rows int) (ionterm.Conn, error) {
	return d.conn, nil
}

func TestSlashCommandsSendsToTheSession(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.openSlash()
	s := mm.(tuiModel)
	s = typeString(t, s, "commands")
	mm, _ = s.activateSlash()
	s = mm.(tuiModel)
	if s.slash != slashCommands {
		t.Fatalf("mode = %d, want slashCommands", s.slash)
	}

	s.slashInput.SetValue("dump device status")
	s = s.refilterSlash()
	if len(s.slashItems) == 0 {
		t.Fatal("no catalog matches")
	}
	t.Logf("top match: %q", s.slashItems[0])

	mm, _ = s.activateSlash()
	after := mm.(tuiModel)
	if after.slash != slashOff {
		t.Errorf("slash mode = %d, want closed", after.slash)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(conn.sent(), "dump device status") {
			t.Logf("reached the wire: %q", conn.sent())
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("command never reached the connection; sent=%q", conn.sent())
}

func TestFuzzyQueryWithSpaces(t *testing.T) {
	m := pickModel(t)
	m.slash = slashCommands
	m.slashInput = newSlashInput()
	m.slashInput.SetValue("du interf")
	m = m.refilterSlash()

	if len(m.slashItems) == 0 {
		t.Fatal("'du interf' matched nothing")
	}
	t.Logf("top 3: %v", m.slashItems[:min(3, len(m.slashItems))])
	if !strings.HasPrefix(m.slashItems[0], "dump interface") {
		t.Errorf("best match = %q, want a dump interface command", m.slashItems[0])
	}
}

func TestSlashOneLinePalette(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.openSlash()
	s := mm.(tuiModel)

	s = typeString(t, s, "commands du interf")

	if s.slash != slashCommands {
		t.Fatalf("mode = %d, want slashCommands — the verb should resolve as you type", s.slash)
	}
	if got := s.slashInput.Value(); got != "du interf" {
		t.Errorf("query = %q, want the text after the verb typed literally", got)
	}
	if len(s.slashItems) == 0 {
		t.Fatal("no matches for 'du interf'")
	}
	if !strings.HasPrefix(s.slashItems[0], "dump interface") {
		t.Errorf("top match = %q", s.slashItems[0])
	}

	mm, _ = s.activateSlash()
	after := mm.(tuiModel)
	if after.slash != slashOff {
		t.Errorf("slash mode = %d, want closed", after.slash)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(conn.sent(), "dump interface") {
			sent := conn.sent()
			if strings.Contains(sent, "\r") || strings.Contains(sent, "\n") {
				t.Errorf("sent %q — must not include a newline so the command stays editable", sent)
			}
			if !strings.HasSuffix(sent, " ") {
				t.Errorf("sent %q — want a trailing space so arguments can follow", sent)
			}
			if sess := after.sessions.Active(); sess != nil && sess.LineEmpty() {
				t.Error("line buffer still reports empty after inserting a command")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("command never reached the connection; sent=%q", conn.sent())
}

func TestSlashShortVerb(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c dump dev")

	if s.slash != slashCommands {
		t.Fatalf("mode = %d; 'c' should resolve to commands", s.slash)
	}
	if got := s.slashInput.Value(); got != "dump dev" {
		t.Errorf("query = %q", got)
	}
	if len(s.slashItems) == 0 || !strings.HasPrefix(s.slashItems[0], "dump dev") {
		t.Errorf("items = %v", s.slashItems[:min(3, len(s.slashItems))])
	}
}

func TestSlashUnknownVerbStaysInTheMenu(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "zzz nope")
	if s.slash != slashMenu {
		t.Errorf("mode = %d, want to stay in the menu for an unknown verb", s.slash)
	}
}

func TestSlashEnterOnTypedVerbWithoutSpace(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "help")
	mm, _ = s.activateSlash()
	if got := mm.(tuiModel).slash; got != slashHelp {
		t.Errorf("mode = %d, want slashHelp", got)
	}
}

func TestSlashFullyTypedCommandIsSentVerbatim(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "commands dump interface status 4")

	if len(s.slashItems) != 0 {
		t.Logf("note: %d catalog matches for the concrete command", len(s.slashItems))
	}

	mm, _ = s.activateSlash()
	if got := mm.(tuiModel).slash; got != slashOff {
		t.Errorf("slash mode = %d, want closed", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(conn.sent(), "dump interface status 4") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("typed command never reached the wire; sent=%q", conn.sent())
}

func TestSlashTypedWriteCommandIsBlocked(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "commands config banner oops")
	mm, _ = s.activateSlash()
	got := mm.(tuiModel)

	if !strings.Contains(got.status, "not read-only") {
		t.Errorf("status = %q, want a read-only complaint", got.status)
	}
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(conn.sent(), "config banner") {
		t.Errorf("write command reached the wire: %q", conn.sent())
	}
}

func TestElementsListAlwaysDialsFresh(t *testing.T) {
	m := pickModel(t)
	m.mode = modeShell
	setTestClient(&m, &api.Client{})

	first, err := m.sessions.Open(context.Background(), stubDialer{}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	m.phase = phaseSession
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModAlt}))
	m = mm.(tuiModel)
	if m.sessions.DetachedCount() != 1 {
		t.Fatalf("DetachedCount() = %d, want 1", m.sessions.DetachedCount())
	}

	m.phase = phasePicking
	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	picked := mm.(tuiModel)

	if cmd == nil {
		t.Error("selecting from the elements list should dial, not reattach")
	}
	if picked.connecting == "" {
		t.Error("want a connecting state for a fresh dial")
	}
	if picked.sessions.DetachedCount() != 1 {
		t.Errorf("DetachedCount() = %d — the elements list must not consume the detached session", picked.sessions.DetachedCount())
	}
	if first.Closed() {
		t.Error("the detached session should still be alive")
	}
}

func TestReattachSkipsDeadSessions(t *testing.T) {
	m := pickModel(t)

	dead, err := m.sessions.Open(context.Background(), stubDialer{}, "1", "dead", 200, 50, ionterm.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	m.sessions.Detach()
	dead.Close()

	if _, ok := m.sessions.Reattach(); ok {
		t.Error("Reattach returned a closed session")
	}
	if m.sessions.DetachedCount() != 0 {
		t.Errorf("DetachedCount() = %d, want the dead session dropped", m.sessions.DetachedCount())
	}
}

func TestStatusSitsAboveTheKeybindLine(t *testing.T) {
	m := pickModel(t)
	m.status = "detached S0100-RTR-01 — alt+t to reattach"

	got := plain(m.viewPicking())
	if !strings.Contains(got, "detached S0100-RTR-01") {
		t.Fatalf("status missing:\n%s", got)
	}
	if !strings.Contains(got, "alt+w menu") {
		t.Fatalf("keybind line was overwritten by the status:\n%s", got)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	statusAt, hintAt := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "detached S0100-RTR-01") {
			statusAt = i
		}
		if strings.Contains(ln, "alt+w menu") {
			hintAt = i
		}
	}
	if statusAt < 0 || hintAt < 0 || statusAt >= hintAt {
		t.Errorf("status at %d, hint at %d — status must sit above the hint", statusAt, hintAt)
	}
}

func TestTabCompletesTheHighlightedCommand(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c dump interface status")
	if s.slash != slashCommands {
		t.Fatalf("mode = %d", s.slash)
	}
	if len(s.slashItems) < 3 {
		t.Fatalf("only %d matches, want several", len(s.slashItems))
	}

	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}), "down")
	mm, _ = mm.(tuiModel).onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}), "down")
	picked := mm.(tuiModel)
	want := picked.slashItems[picked.slashCursor]
	t.Logf("highlighted: %q", want)

	mm, _ = picked.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	done := mm.(tuiModel)

	if got := strings.TrimRight(done.slashInput.Value(), " "); got != want {
		t.Errorf("after tab query = %q, want the highlighted item %q", got, want)
	}
	if !strings.HasSuffix(done.slashInput.Value(), " ") {
		t.Error("completion should leave a trailing space")
	}
	if done.slash != slashCommands {
		t.Errorf("tab closed the palette (mode %d)", done.slash)
	}
}

func TestTabCompletesEvenWhenNotAPrefixExtension(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c dmpifst")
	if len(s.slashItems) == 0 {
		t.Skip("abbreviation matched nothing")
	}
	want := s.slashItems[0]

	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	got := strings.TrimRight(mm.(tuiModel).slashInput.Value(), " ")
	if got != want {
		t.Errorf("tab gave %q, want %q — an explicit selection should fill in regardless", got, want)
	}
}

func TestTabInMenuEntersTheVerb(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "comm")
	if s.slash != slashMenu {
		t.Fatalf("mode = %d, want to still be in the menu", s.slash)
	}

	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	if got := mm.(tuiModel).slash; got != slashCommands {
		t.Errorf("tab in the menu gave mode %d, want slashCommands", got)
	}
}

func TestTabWithNoMatchesDoesNothing(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c zzzqqq")
	if len(s.slashItems) != 0 {
		t.Skip("query unexpectedly matched")
	}

	before := s.slashInput.Value()
	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	after := mm.(tuiModel)
	if after.slashInput.Value() != before {
		t.Errorf("tab changed the query to %q with no matches", after.slashInput.Value())
	}
	if after.slash != slashCommands {
		t.Errorf("tab closed the palette (mode %d)", after.slash)
	}
}

func TestSpaceTypesLiterallyForFuzzySearch(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c mp int sta")

	if s.slash != slashCommands {
		t.Fatalf("mode = %d", s.slash)
	}
	if got := s.slashInput.Value(); got != "mp int sta" {
		t.Fatalf("query = %q; spaces must be typed literally so fuzzy search works", got)
	}
	if len(s.slashItems) == 0 {
		t.Fatal("'mp int sta' matched nothing")
	}
	t.Logf("top 3: %v", s.slashItems[:min(3, len(s.slashItems))])
	if !strings.HasPrefix(s.slashItems[0], "dump interface stat") {
		t.Errorf("top match = %q, want a dump interface status command", s.slashItems[0])
	}
}

func TestTabTakesTopMatchByDefault(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c mp int sta")
	if s.slashCursor != 0 {
		t.Fatalf("slashCursor = %d, want 0 so tab takes the top match", s.slashCursor)
	}
	want := s.slashItems[0]

	mm, _ = s.onSlashKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), "tab")
	got := strings.TrimRight(mm.(tuiModel).slashInput.Value(), " ")
	if got != want {
		t.Errorf("tab gave %q, want the top match %q", got, want)
	}
}

func TestInsertedCommandEndsWithASpace(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSession

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "e1", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.openSlash()
	s := typeString(t, mm.(tuiModel), "c dump device status")
	mm, _ = s.activateSlash()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := conn.sent(); strings.HasPrefix(got, "dump device status") {
			if got != "dump device status " {
				t.Errorf("sent %q, want exactly the command plus one space", got)
			}
			if sess := mm.(tuiModel).sessions.Active(); sess != nil && sess.Line() != "dump device status " {
				t.Errorf("line buffer = %q, want it to match what was sent", sess.Line())
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("nothing reached the wire; sent=%q", conn.sent())
}
