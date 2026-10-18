package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

func settingsModel(t *testing.T) tuiModel {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{
		LastTenant: "Acme Co",
		Tenants: []config.Tenant{
			{Name: "Acme Co", TSGID: "1", Users: []config.IONUser{
				{Name: "readonly", Default: true}, {Name: "cisops"},
			}},
			{Name: "Lab", TSGID: "2"},
		},
	}
	if err := st.Secrets.Set(config.IONPassKey("1", "readonly"), "old"); err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = "readonly"
	m.phase = phaseSettings
	m.width, m.height = 100, 30
	return m
}

// navTo walks the nav pane down to section want, so these tests do not depend
// on which sections happen to sit next to each other.
func navTo(t *testing.T, m tuiModel, want int) tuiModel {
	t.Helper()
	for i := 0; i < len(setSections); i++ {
		if m.setSection == want {
			return m
		}
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		m = mm.(tuiModel)
	}
	t.Fatalf("never reached section %d; stopped at %d", want, m.setSection)
	return m
}

func TestSettingsNavAndContentFocus(t *testing.T) {
	m := settingsModel(t)

	if m.setFocus != focusNav {
		t.Fatal("settings should start on the nav pane")
	}

	m = navTo(t, m, secTenants)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	if m.setFocus != focusContent {
		t.Fatal("tab should move into the content pane")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mm.(tuiModel)
	if m.setCursor != 1 {
		t.Errorf("content cursor = %d, want 1", m.setCursor)
	}
	if m.setSection != secTenants {
		t.Error("moving in the content pane changed the section")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = mm.(tuiModel)
	if m.setFocus != focusNav {
		t.Error("esc from content should return to the nav pane")
	}
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phaseMenu {
		t.Errorf("esc from nav gave phase %v, want phaseMenu", got)
	}
}

func TestSettingsEverySectionRenders(t *testing.T) {
	m := settingsModel(t)
	wants := map[int]string{
		secGeneral: "spinner",
		secTenants: "Acme Co",
		secUsers:   "readonly",
		secSession: "buffer cols",
	}
	for sec, want := range wants {
		m.setSection = sec
		got := plain(m.viewSettings())
		if !strings.Contains(got, want) {
			t.Errorf("section %d missing %q:\n%s", sec, want, got)
		}
		for _, name := range setSections {
			if !strings.Contains(got, name) {
				t.Errorf("section %d is missing nav entry %q", sec, name)
			}
		}
	}
}

func TestSettingsGeneralTogglesWriteGuard(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.setCursor = 1

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if !m.cfg.AllowWrites {
		t.Error("enter did not toggle the write guard")
	}

	saved, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.AllowWrites {
		t.Error("toggle was not persisted")
	}
}

func TestSettingsSessionGeometryAdjusts(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secSession
	m.setFocus = focusContent
	m.setCursor = 0

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '+', Text: "+"}))
	m = mm.(tuiModel)
	cols, _ := m.sessionGeometry()
	if cols != defaultSessionCols+10 {
		t.Errorf("cols = %d, want %d", cols, defaultSessionCols+10)
	}

	m.setCursor = 1
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '-', Text: "-"}))
	_, rows := mm.(tuiModel).sessionGeometry()
	if rows != defaultSessionRows-10 {
		t.Errorf("rows = %d, want %d", rows, defaultSessionRows-10)
	}
}

func TestEditPasswordFromSettingsUsers(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.setFocus = focusContent
	m.setCursor = 0

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	f := mm.(tuiModel)
	if f.formKind != formEditPass {
		t.Fatalf("formKind = %d, want formEditPass", f.formKind)
	}
	if len(f.form) != 1 {
		t.Fatalf("form has %d fields, want just the password", len(f.form))
	}
	if f.formUserName != "readonly" {
		t.Errorf("formUserName = %q, want readonly", f.formUserName)
	}

	f = typeString(t, f, "brandnew")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if done.phase != phaseSettings {
		t.Errorf("phase = %v, want to return to settings", done.phase)
	}
	got, err := done.store.Secrets.Get(config.IONPassKey("1", "readonly"))
	if err != nil || got != "brandnew" {
		t.Errorf("stored password = %q %v, want brandnew", got, err)
	}
}

func TestEditPasswordFromElementsReturnsToElements(t *testing.T) {
	m := settingsModel(t)
	m.phase = phaseUsers
	m.userCursor = 1

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	f := mm.(tuiModel)
	if f.formKind != formEditPass || f.formUserName != "cisops" {
		t.Fatalf("form = %d/%q", f.formKind, f.formUserName)
	}
	if f.formReturn != phasePicking {
		t.Errorf("formReturn = %v, want phasePicking", f.formReturn)
	}

	f = typeString(t, f, "pw2")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if done.phase != phasePicking {
		t.Errorf("phase = %v, want to drop back into elements", done.phase)
	}
	if got, _ := done.store.Secrets.Get(config.IONPassKey("1", "cisops")); got != "pw2" {
		t.Errorf("password = %q, want pw2", got)
	}
}

func TestAddUserFromElementsReturnsToElements(t *testing.T) {
	m := settingsModel(t)
	m.phase = phaseUsers

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	f := mm.(tuiModel)
	if f.formKind != formUser {
		t.Fatalf("formKind = %d, want formUser", f.formKind)
	}
	if f.formReturn != phasePicking {
		t.Errorf("formReturn = %v, want phasePicking", f.formReturn)
	}

	f = typeString(t, f, "netadmin")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if done.phase != phasePicking {
		t.Errorf("phase = %v, want to drop back into elements", done.phase)
	}
	if !done.tenant.HasUser("netadmin") {
		t.Error("user was not added")
	}
}

func TestAddUserFromSettingsReturnsToSettings(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.setFocus = focusContent

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	f := mm.(tuiModel)
	if f.formReturn != phaseSettings {
		t.Errorf("formReturn = %v, want phaseSettings", f.formReturn)
	}

	f = typeString(t, f, "ops2")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := mm.(tuiModel).phase; got != phaseSettings {
		t.Errorf("phase = %v, want phaseSettings", got)
	}
}

func TestSettingsFrameFitsTheTerminal(t *testing.T) {
	for _, h := range []int{8, 14, 30} {
		for sec := range setSections {
			m := settingsModel(t)
			m.height = h
			m.setSection = sec
			if n := len(strings.Split(ui.ClipFrame(m.viewSettings(), h), "\n")); n > h {
				t.Errorf("height %d section %d: frame is %d lines", h, sec, n)
			}
		}
	}
}

func TestRenderSettingsPanes(t *testing.T) {
	m := settingsModel(t)
	for _, sec := range []int{secSession, secDebug} {
		m.setSection = sec
		m.setFocus = focusContent
		t.Log("\n--- " + setSections[sec] + " ---\n" + m.viewSettings())
	}
}

func TestLabelsPadVisibleWidthNotEscapeBytes(t *testing.T) {
	for _, s := range []string{"spinner", "commands", "a"} {
		if got := len(plain(ui.Label(s))); got != 13 {
			t.Errorf("ui.Label(%q) visible width = %d, want 13", s, got)
		}
	}

	m := settingsModel(t)
	m.setSection = secGeneral
	got := plain(m.viewSettings())
	if strings.Contains(got, "commandsread-only") {
		t.Error("label ran into its value; padding is counting escape bytes")
	}
	if !strings.Contains(got, "commands     read-only") {
		t.Errorf("label not padded:\n%s", got)
	}
}

func TestSettingsUsersPaneKeepsTheWholeRow(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.width = 100

	got := plain(m.viewSettings())
	if strings.Contains(got, "saved…") {
		t.Error("row was truncated mid-content; truncate is counting escape bytes")
	}
	if !strings.Contains(got, "saved") || !strings.Contains(got, "no password") {
		t.Errorf("pane lost content:\n%s", got)
	}
}

func TestDefaultUserSwitchesRepeatedlyInSettings(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.setFocus = focusContent

	for _, step := range []struct {
		cursor int
		want   string
	}{
		{1, "cisops"}, {0, "readonly"}, {1, "cisops"},
	} {
		m.setCursor = step.cursor
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
		m = mm.(tuiModel)

		if got := m.tenant.DefaultUser(); got != step.want {
			t.Errorf("cursor %d: model default = %q, want %q (users=%+v)", step.cursor, got, step.want, m.tenant.Users)
		}
		saved, err := m.store.Config()
		if err != nil {
			t.Fatal(err)
		}
		tn, _ := saved.Tenant("Acme Co")
		if got := tn.DefaultUser(); got != step.want {
			t.Errorf("cursor %d: persisted default = %q, want %q", step.cursor, got, step.want)
		}
		if n := len(saved.Tenants); n != 2 {
			t.Errorf("tenant count grew to %d", n)
		}
	}
}

func TestDefaultUserSwitchesRepeatedlyInUserPanel(t *testing.T) {
	m := settingsModel(t)
	m.phase = phaseUsers

	for _, step := range []struct {
		cursor int
		want   string
	}{
		{1, "cisops"}, {0, "readonly"}, {1, "cisops"},
	} {
		m.userCursor = step.cursor
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
		m = mm.(tuiModel)

		if got := m.tenant.DefaultUser(); got != step.want {
			t.Errorf("cursor %d: default = %q, want %q (users=%+v)", step.cursor, got, step.want, m.tenant.Users)
		}
	}
}

func TestDefaultUserSwitchViaNavigationKeys(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.setFocus = focusContent
	m.setCursor = 0

	press := func(mm tuiModel, k tea.Key) tuiModel {
		out, _ := mm.Update(tea.KeyPressMsg(k))
		return out.(tuiModel)
	}

	m = press(m, tea.Key{Code: tea.KeyDown})
	t.Logf("after down: setCursor=%d focus=%d rows=%d", m.setCursor, m.setFocus, m.sectionRows())
	m = press(m, tea.Key{Code: 'd', Text: "d"})
	t.Logf("after d: default=%q active=%q", m.tenant.DefaultUser(), m.ionUser)
	if got := m.tenant.DefaultUser(); got != "cisops" {
		t.Errorf("default = %q, want cisops", got)
	}

	m = press(m, tea.Key{Code: tea.KeyUp})
	t.Logf("after up: setCursor=%d", m.setCursor)
	m = press(m, tea.Key{Code: 'd', Text: "d"})
	t.Logf("after d: default=%q", m.tenant.DefaultUser())
	if got := m.tenant.DefaultUser(); got != "readonly" {
		t.Errorf("second switch default = %q, want readonly", got)
	}
}

func TestUserPanelStaysOpenForRepeatedSwitches(t *testing.T) {
	m := settingsModel(t)
	m.phase = phaseUsers

	for _, step := range []struct {
		cursor int
		want   string
	}{{1, "cisops"}, {0, "readonly"}, {1, "cisops"}} {
		m.userCursor = step.cursor
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		m = mm.(tuiModel)

		if m.phase != phaseUsers {
			t.Fatalf("enter left the panel (phase %v); you could only switch once per visit", m.phase)
		}
		if m.ionUser != step.want {
			t.Errorf("ionUser = %q, want %q", m.ionUser, step.want)
		}
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phasePicking {
		t.Errorf("esc phase = %v, want phasePicking", got)
	}
}

func TestEnterOpensSectionFromNav(t *testing.T) {
	m := settingsModel(t)
	if m.setFocus != focusNav {
		t.Fatal("expected nav focus")
	}

	m = navTo(t, m, secTenants)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.setFocus != focusContent {
		t.Fatal("enter from the nav pane should open the section")
	}
	if m.setCursor != 0 {
		t.Errorf("setCursor = %d, want 0 on entry", m.setCursor)
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if got := mm.(tuiModel).setFocus; got != focusContent {
		t.Errorf("right arrow should also open, focus = %d", got)
	}
}

func TestEnterInContentStillActs(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.setCursor = 1

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !mm.(tuiModel).cfg.AllowWrites {
		t.Error("enter in the content pane must still perform the row action")
	}
}

func TestEnterOnAnEmptySectionDoesNotTrap(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	m := newTUIModel(context.Background(), st, &config.Config{})
	m.phase = phaseSettings
	m.setSection = secTenants
	m.width, m.height = 100, 30

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := mm.(tuiModel).setFocus; got != focusNav {
		t.Errorf("focus = %d; an empty section should not take focus", got)
	}
}

func TestDebugSectionIsReadOnly(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secDebug

	if m.sectionRows() != 0 {
		t.Errorf("sectionRows() = %d, want 0 — Debug takes no cursor", m.sectionRows())
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := mm.(tuiModel).setFocus; got != focusNav {
		t.Error("Debug should not take content focus")
	}

	got := plain(m.viewSettings())
	for _, want := range []string{"config", "secrets", "version", "catalog", "terminal", "sessions"} {
		if !strings.Contains(got, want) {
			t.Errorf("Debug missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "read only") {
		t.Error("hint should say the section is read only")
	}
}

func TestSessionGeometryWarningFits(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secSession
	m.width = 100

	got := plain(m.viewSettings())
	if !strings.Contains(got, "do not change unless you know what you are doing!") {
		t.Errorf("warning truncated at 100 cols:\n%s", got)
	}
	n := strings.Count(got, "do not change unless you know what you are doing!")
	if n != 2 {
		t.Errorf("warning appears %d times, want once per line", n)
	}
}

func TestEditTenantRenameMovesSecretsAndConfig(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secTenants
	m.setFocus = focusContent
	m.setCursor = 0

	if err := m.store.Secrets.Set(config.ClientSecretKey("1"), "sa-secret"); err != nil {
		t.Fatal(err)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	f := mm.(tuiModel)
	if f.formKind != formEditTenant {
		t.Fatalf("formKind = %d, want formEditTenant", f.formKind)
	}
	if f.formValue(0) != "Acme Co" || f.formValue(1) != "1" {
		t.Fatalf("form not prefilled: %q / %q", f.formValue(0), f.formValue(1))
	}

	f.form[0].input.SetValue("Acme Prod")
	f.form[1].input.SetValue("99")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	saved, err := done.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Tenant("Acme Co"); ok {
		t.Error("old tenant name survived the rename")
	}
	tn, ok := saved.Tenant("Acme Prod")
	if !ok {
		t.Fatal("renamed tenant missing")
	}
	if tn.TSGID != "99" {
		t.Errorf("TSGID = %q, want 99", tn.TSGID)
	}
	if len(tn.Users) != 2 {
		t.Errorf("users lost in the rename: %+v", tn.Users)
	}
	if saved.LastTenant != "Acme Prod" {
		t.Errorf("LastTenant = %q, want the new name", saved.LastTenant)
	}

	if got, _ := done.store.Secrets.Get(config.ClientSecretKey("99")); got != "sa-secret" {
		t.Errorf("client secret did not move to the new tsg: %q", got)
	}
	if _, err := done.store.Secrets.Get(config.ClientSecretKey("1")); err == nil {
		t.Error("old client secret key left behind")
	}
	if got, _ := done.store.Secrets.Get(config.IONPassKey("99", "readonly")); got != "old" {
		t.Errorf("ion password did not move to the new tsg: %q", got)
	}
}

func TestEditUserRenameKeepsDefaultAndMovesPassword(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secUsers
	m.setFocus = focusContent
	m.setCursor = 0

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	f := mm.(tuiModel)
	if f.formKind != formEditUser {
		t.Fatalf("formKind = %d, want formEditUser", f.formKind)
	}
	if f.formValue(0) != "readonly" {
		t.Fatalf("form not prefilled: %q", f.formValue(0))
	}

	f.form[0].input.SetValue("ro")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if done.tenant.DefaultUser() != "ro" {
		t.Errorf("DefaultUser() = %q, want the renamed user to stay default", done.tenant.DefaultUser())
	}
	if done.tenant.HasUser("readonly") {
		t.Error("old user name survived")
	}
	if got, _ := done.store.Secrets.Get(config.IONPassKey("1", "ro")); got != "old" {
		t.Errorf("password did not follow the rename: %q", got)
	}
	if _, err := done.store.Secrets.Get(config.IONPassKey("1", "readonly")); err == nil {
		t.Error("old password key left behind")
	}
}

func TestGeometryFormTypesAValue(t *testing.T) {
	m := settingsModel(t)
	m.setSection = secSession
	m.setFocus = focusContent
	m.setCursor = 1

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	f := mm.(tuiModel)
	if f.formKind != formGeometry {
		t.Fatalf("formKind = %d, want formGeometry", f.formKind)
	}

	f.form[0].input.SetValue("120")
	mm, _ = f.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	done := mm.(tuiModel)

	if _, rows := done.sessionGeometry(); rows != 120 {
		t.Errorf("rows = %d, want 120", rows)
	}
	if done.phase != phaseSettings {
		t.Errorf("phase = %v, want to return to settings", done.phase)
	}

	m.setCursor = 0
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	bad := mm.(tuiModel)
	bad.form[0].input.SetValue("abc")
	mm, _ = bad.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := mm.(tuiModel).status; !strings.Contains(got, "number") {
		t.Errorf("status = %q, want a complaint about the value", got)
	}
}
