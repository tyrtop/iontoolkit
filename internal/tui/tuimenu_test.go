package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

var testANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return testANSI.ReplaceAllString(s, "") }

func TestMenuEntries(t *testing.T) {
	want := []string{"Elements", "Sign In with Browser", "Macros", "Workflows", "Settings", "Quit"}

	cases := map[string]struct {
		cfg       *config.Config
		hasTenant bool
	}{
		"no tenant":   {cfg: &config.Config{}},
		"one tenant":  {cfg: &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}}}, hasTenant: true},
		"two tenants": {cfg: &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}, {Name: "Lab"}}}, hasTenant: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			m := withWorkspace(withStore(tuiModel{}, config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json")), tc.cfg), config.Tenant{}, tc.hasTenant, "", authBrowser)
			if tc.hasTenant {
				m.runtime().ReplaceTenant(tc.cfg.Tenants[0])
			}
			got := m.menuEntries()
			if len(got) != len(want) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
			}
			for i, w := range want {
				if got[i].label != w {
					t.Errorf("[%d] = %q, want %q", i, got[i].label, w)
				}
			}
		})
	}
}

func TestMenuLabelDoesNotCarryTheMode(t *testing.T) {
	root := t.TempDir()
	for _, mode := range []string{modeShell, modeBatch} {
		m := withStore(tuiModel{}, config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json")), &config.Config{})
		m.setMode(mode)
		if got := m.menuEntries()[0].label; got != "Elements" {
			t.Errorf("first entry in %v mode = %q, want %q", mode, got, "Elements")
		}
	}
}

func TestCacheLine(t *testing.T) {
	cases := map[string]struct {
		m    tuiModel
		want string
	}{
		"no tenant": {tuiModel{}, "no tenant configured"},
		"no cache":  {withWorkspace(withFleet(tuiModel{}, cacheNone, 0, time.Time{}), config.Tenant{}, true, "", authBrowser), "no cached elements"},
		"first fetch has nothing to report yet": {
			withWorkspace(withFleet(tuiModel{}, cacheUpdating, 0, time.Time{}), config.Tenant{}, true, "", authBrowser), "fetching elements"},
		"updating": {withWorkspace(withFleet(tuiModel{}, cacheUpdating, 847, time.Time{}), config.Tenant{}, true, "", authBrowser), "847 elements cached (updating)"},
		"just updated": {
			withWorkspace(withFleet(tuiModel{}, cacheUpdated, 847, time.Now()), config.Tenant{}, true, "", authBrowser),
			"847 elements cached (updated)"},
		"updated a few minutes ago": {
			withWorkspace(withFleet(tuiModel{}, cacheUpdated, 847, time.Now().Add(-4*time.Minute)), config.Tenant{}, true, "", authBrowser),
			"847 elements cached (updated)  4m ago"},
		"failed": {withWorkspace(withFleet(tuiModel{}, cacheFailed, 0, time.Time{}), config.Tenant{}, true, "", authBrowser), "element refresh failed"},
		"loaded from a fresh cache": {
			withWorkspace(withFleet(tuiModel{}, cacheCached, 5613, time.Now().Add(-9*time.Minute)), config.Tenant{}, true, "", authBrowser),
			"5613 elements cached  9m ago"},
		"an updated cache that went stale stops saying so": {
			withWorkspace(withFleet(tuiModel{}, cacheUpdated, 5613, time.Now().Add(-38*time.Minute-39*time.Second)), config.Tenant{}, true, "", authBrowser),
			"5613 elements cached  38m ago"},
		"aged past an hour": {
			withWorkspace(withFleet(tuiModel{}, cacheCached, 12, time.Now().Add(-2*time.Hour)), config.Tenant{}, true, "", authBrowser),
			"12 elements cached  2h ago"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := plain(tc.m.cacheLine()); got != tc.want {
				t.Errorf("cacheLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func typeString(t *testing.T, m tuiModel, s string) tuiModel {
	t.Helper()
	for _, r := range s {
		mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = mm.(tuiModel)
	}
	return m
}

func TestFilterSuggestions(t *testing.T) {
	m := tuiModel{width: 80, height: 24}
	m.filter = newField("", "", false).input
	m.filter.ShowSuggestions = true
	m.table = newElementTable()

	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", SiteID: "s1"},
		{ID: "2", Name: "S0100-RTR-02", SiteID: "s1"},
		{ID: "3", Name: "S0100-RTR-03", SiteID: "s1"},
		{ID: "4", Name: "S9999-RTR-01", SiteID: "s2"},
	})

	m.phase = phasePicking
	m.filterOn = true
	m.filter.Focus()

	m = typeString(t, m, "s0100")
	if got := m.filter.Value(); got != "s0100" {
		t.Fatalf("typed value = %q, want s0100", got)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)

	got := m.filter.Value()
	if got == "s0100" {
		t.Fatal("tab did not accept any suggestion")
	}
	if !strings.HasPrefix(strings.ToUpper(got), "S0100-RTR-0") {
		t.Errorf("accepted suggestion = %q, want an S0100-RTR-0* completion", got)
	}
}

func TestFilterMatchesSubstringAndSite(t *testing.T) {
	m := tuiModel{width: 80, height: 24}
	m.filter = newField("", "", false).input
	m.table = newElementTable()
	m.runtime().MergeSites(map[string]string{"1/s1": "Nashville Hub", "1/s2": "Dallas"})

	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", SiteID: "s1"},
		{ID: "2", Name: "S0100-RTR-02", SiteID: "s1"},
		{ID: "3", Name: "S9999-RTR-01", SiteID: "s2"},
	})

	m.filter.SetValue("100")
	m.applyFilter()
	if len(m.matched()) != 2 {
		t.Errorf("substring filter matched %d, want 2", len(m.matched()))
	}

	m.filter.SetValue("nashville")
	m.applyFilter()
	if len(m.matched()) != 2 {
		t.Errorf("site filter matched %d, want 2", len(m.matched()))
	}

	m.filter.SetValue("dallas")
	m.applyFilter()
	if len(m.matched()) != 1 {
		t.Errorf("site filter matched %d, want 1", len(m.matched()))
	}
}

func TestSiteNameFallsBackToID(t *testing.T) {
	m := tuiModel{}
	m.runtime().MergeSites(map[string]string{"1/s1": "Nashville Hub", "2/s1": "Dallas Hub"})
	row := func(tsg, site string) elementRow {
		return elementRow{Element: api.Element{SiteID: site}, TSG: tsg}
	}
	if got := m.runtime().SiteName(row("1", "s1")); got != "Nashville Hub" {
		t.Errorf("siteName = %q, want the resolved name", got)
	}
	if got := m.runtime().SiteName(row("2", "s1")); got != "Dallas Hub" {
		t.Errorf("siteName = %q, want the other tenant's site of the same id", got)
	}
	if got := m.runtime().SiteName(row("1", "unknown")); got != "unknown" {
		t.Errorf("siteName = %q, want the raw id", got)
	}
	if got := m.runtime().SiteName(row("1", "")); got != "-" {
		t.Errorf("siteName = %q, want -", got)
	}
}

func TestNormalizeErr(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"run error":    {"element 1234567890: run: waiting for shell: context deadline exceeded", "run: waiting for shell: context deadline exceeded"},
		"lookup error": {"element 1234567890: status 404: not found", "status 404: not found"},
		"no prefix":    {"dial tcp: i/o timeout", "dial tcp: i/o timeout"},
		"empty":        {"", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := normalizeErr(tc.in); got != tc.want {
				t.Errorf("normalizeErr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderMenu(t *testing.T) {
	m := withWorkspace(withFleet(withStore(tuiModel{
		width:  80,
		height: 24,
	}, nil, &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}, {Name: "Lab"}}}), cacheUpdating, 847, time.Time{}), config.Tenant{Name: "Acme Co"}, true, "", authBrowser)
	t.Log("\n" + m.viewMenu())
}

func TestPasteReachesFocusedInput(t *testing.T) {
	base := withStore(tuiModel{
		width:  80,
		height: 24,
	}, nil, &config.Config{})
	base.filter = newField("", "", false).input
	base.cmdInput = newField("", "", false).input
	base.table = newElementTable()

	t.Run("add tenant form", func(t *testing.T) {
		mm, _ := base.openTenantForm(config.AuthServiceAccount)
		m := mm.(tuiModel)

		mm, _ = m.Update(tea.PasteMsg{Content: "1234567890"})
		m = mm.(tuiModel)

		if got := m.formValue(0); got != "1234567890" {
			t.Errorf("field 0 = %q, want the pasted text", got)
		}
	})

	t.Run("second form field after tab", func(t *testing.T) {
		mm, _ := base.openTenantForm(config.AuthServiceAccount)
		m := mm.(tuiModel)

		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
		m = mm.(tuiModel)
		mm, _ = m.Update(tea.PasteMsg{Content: "9876543210"})
		m = mm.(tuiModel)

		if m.formCursor != 1 {
			t.Fatalf("formCursor = %d, want 1", m.formCursor)
		}
		if got := m.formValue(1); got != "9876543210" {
			t.Errorf("field 1 = %q, want the pasted text", got)
		}
		if got := m.formValue(0); got != "" {
			t.Errorf("field 0 = %q, want empty", got)
		}
	})

	t.Run("command input", func(t *testing.T) {
		m := base
		m.phase = phaseCommand
		m.cmdInput.Focus()

		mm, _ := m.Update(tea.PasteMsg{Content: "dump interface status 1"})
		m = mm.(tuiModel)

		if got := m.cmdInput.Value(); got != "dump interface status 1" {
			t.Errorf("cmdInput = %q, want the pasted text", got)
		}
	})

	t.Run("filter only when focused", func(t *testing.T) {
		m := base
		m.phase = phasePicking
		m.runtime().SetElements([]elementRow{{Element: api.Element{ID: "1", Name: "S0103-RTR-01"}, TSG: "1"}})

		mm, _ := m.Update(tea.PasteMsg{Content: "103"})
		if got := mm.(tuiModel).filter.Value(); got != "" {
			t.Errorf("unfocused filter took the paste: %q", got)
		}

		m.filterOn = true
		m.filter.Focus()
		mm, _ = m.Update(tea.PasteMsg{Content: "103"})
		m2 := mm.(tuiModel)
		if got := m2.filter.Value(); got != "103" {
			t.Errorf("focused filter = %q, want 103", got)
		}
		if len(m2.matched()) != 1 {
			t.Errorf("paste did not reapply the filter: %d matches", len(m2.matched()))
		}
	})
}

func TestRenderSettings(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	m := withStore(tuiModel{
		spinner: ui.NewSpinner(ui.DefaultSpinner),
		width:   80,
		height:  24,
	}, st, &config.Config{
		LastTenant: "Acme Co",
		Tenants: []config.Tenant{
			{Name: "Acme Co", TSGID: "1234567890", Users: []config.IONUser{
				{Name: "readonly", Default: true}, {Name: "ops"},
			}},
			{Name: "Lab", TSGID: "9876543210"},
		},
	})
	t.Log("\n" + m.viewSettings())
}

func TestEnterSavesFromAnyField(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	m := withStore(tuiModel{}, st, &config.Config{})
	m.table = newElementTable()
	m.cmdInput = newField("", "", false).input

	mm, _ := m.openTenantForm(config.AuthServiceAccount)
	m = mm.(tuiModel)

	m = typeString(t, m, "Acme Co")
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mm.(tuiModel)
	m = typeString(t, m, "1234567890")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)

	if m.phase != phaseMenu {
		t.Fatalf("phase = %v, want phaseMenu after enter on field 1 of 4 (status: %q)", m.phase, m.status)
	}
	if !m.hasTenant() || m.tenant().Name != "Acme Co" || m.tenant().TSGID != "1234567890" {
		t.Errorf("tenant = %+v", m.tenant())
	}

	saved, err := st.Config()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Tenant("Acme Co"); !ok {
		t.Error("tenant was not persisted")
	}
}

func TestEnterReportsMissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	m := withStore(tuiModel{}, st, &config.Config{})
	mm, _ := m.openTenantForm(config.AuthServiceAccount)
	m = mm.(tuiModel)

	m = typeString(t, m, "Acme Co")
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)

	if m.phase != phaseAddTenant {
		t.Errorf("phase = %v, want to stay on the form", m.phase)
	}
	if m.status == "" {
		t.Error("want an error about the missing tsg id")
	}
}

func TestPlaceholdersRenderInFull(t *testing.T) {
	m := newTUIModel(context.Background(), nil, &config.Config{})

	if got := plain(m.filter.View()); !strings.Contains(got, "filter") {
		t.Errorf("filter placeholder = %q, want it to contain %q", got, "filter")
	}
	if got := plain(m.cmdInput.View()); !strings.Contains(got, "dump interface status 1") {
		t.Errorf("cmdInput placeholder = %q, want the full hint", got)
	}

	mm, _ := m.openTenantForm(config.AuthServiceAccount)
	fm := mm.(tuiModel)
	wants := []string{"Acme Co", "1234567890", "svc@1234.iam.panserviceaccount.com"}
	for i, want := range wants {
		if got := plain(fm.form[i].input.View()); !strings.Contains(got, want) {
			t.Errorf("form field %d placeholder = %q, want it to contain %q", i, got, want)
		}
	}
}

func pickModel(t *testing.T) tuiModel {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "t", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}},
	}}}
	m := newTUIModel(context.Background(), st, cfg)
	m.runtime().UseTenant(cfg.Tenants[0])
	m.runtime().SetIONUser("u")
	if err := st.Secrets.Set(config.IONPassKey("1", "u"), "pw"); err != nil {
		t.Fatal(err)
	}
	m.phase = phasePicking
	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", Connected: true},
		{ID: "2", Name: "S0100-RTR-02", Connected: true},
		{ID: "3", Name: "S9999-RTR-01"},
	})
	return m
}

func TestArrowsMoveTableWhileFiltering(t *testing.T) {
	m := pickModel(t)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	if !m.filterOn {
		t.Fatal("/ did not enter filter mode")
	}

	m = typeString(t, m, "s0100")
	if len(m.matched()) != 2 {
		t.Fatalf("filter matched %d, want 2", len(m.matched()))
	}

	if got := m.table.Cursor(); got != 0 {
		t.Fatalf("cursor starts at %d, want 0", got)
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mm.(tuiModel)

	if !m.filterOn {
		t.Error("down key dropped out of filter mode")
	}
	if got := m.table.Cursor(); got != 1 {
		t.Errorf("cursor = %d, want 1 — arrows should move the table while filtering", got)
	}
	if got := m.filter.Value(); got != "s0100" {
		t.Errorf("filter value changed to %q", got)
	}

	el, ok := m.current()
	if !ok || el.Name != "S0100-RTR-02" {
		t.Errorf("current() = %+v, want S0100-RTR-02", el)
	}
}

func TestFilteringAndSelectingAreSeparateModes(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	m = typeString(t, m, "s0100")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mm.(tuiModel)
	if got := m.filter.Value(); got != "s0100 " {
		t.Errorf("filter = %q, want the space typed into it", got)
	}
	if m.selectedCount() != 0 {
		t.Errorf("selected = %v, want no selection while filtering", m.runtime().Selection())
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.filterOn {
		t.Error("enter did not leave filter mode")
	}
	if got := m.filter.Value(); got != "s0100 " {
		t.Errorf("filter = %q, want the query kept after applying", got)
	}
	if m.phase != phasePicking {
		t.Errorf("phase = %v, want to stay on the list rather than act on it", m.phase)
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mm.(tuiModel)
	if m.selectedCount() != 1 {
		t.Errorf("selected = %v, want space to select once the filter is applied", m.runtime().Selection())
	}
	if got := m.filter.Value(); got != "s0100 " {
		t.Errorf("filter = %q, want selecting to leave the query alone", got)
	}
}

func TestEscClearsTheQueryRatherThanApplyingIt(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	m = typeString(t, m, "s0100")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	got := mm.(tuiModel)
	if got.filterOn {
		t.Error("esc did not leave filter mode")
	}
	if got.filter.Value() != "" {
		t.Errorf("filter = %q, want esc to clear it", got.filter.Value())
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want to stay on the list", got.phase)
	}
}

func TestFilterHintNamesOnlyTheKeysThatEndIt(t *testing.T) {
	m := pickModel(t)
	m.width, m.height = 200, 40
	m.spinner = ui.NewSpinner("")
	m.filterOn = true

	out := plain(m.viewPicking())
	for _, want := range []string{"enter apply", "esc clear", "tab complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("the filter hint is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "space select") {
		t.Errorf("the filter hint still offers space as select:\n%s", out)
	}
}

func TestSpaceStillSelectsWithTheFilterClosed(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	got := mm.(tuiModel)
	if got.selectedCount() != 1 {
		t.Errorf("selected = %v, want space to select when not filtering", got.runtime().Selection())
	}
}

func TestEscClearsFilterThenLeaves(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	m = typeString(t, m, "s0100")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = mm.(tuiModel)

	if m.filterOn || m.filter.Value() != "" {
		t.Errorf("first esc should clear the filter: on=%v value=%q", m.filterOn, m.filter.Value())
	}
	if m.phase != phasePicking {
		t.Errorf("first esc left the picker, phase = %v", m.phase)
	}
	if len(m.matched()) != 3 {
		t.Errorf("matches = %d, want all 3 back", len(m.matched()))
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phaseMenu {
		t.Errorf("second esc phase = %v, want phaseMenu", got)
	}
}

func TestBatchModeEnterGoesToCommandEntry(t *testing.T) {
	m := pickModel(t)
	m.setMode(modeBatch)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mm.(tuiModel)
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if got := mm.(tuiModel).phase; got != phaseCommand {
		t.Errorf("phase = %v, want phaseCommand in batch mode", got)
	}
}

func TestEnterWithNoSelectionDoesNotEnterCommandMode(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)

	if m.phase == phaseCommand {
		t.Error("enter with nothing selected should open a shell, not command mode")
	}
}

func TestCtrlAToggleSelectsFilteredOnly(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	m = typeString(t, m, "s0100")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	m = mm.(tuiModel)

	if m.selectedCount() != 2 {
		t.Fatalf("selected %d, want the 2 filtered elements", m.selectedCount())
	}
	if m.isSelected("3") {
		t.Error("ctrl+a selected an element outside the filter")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	if got := mm.(tuiModel).selectedCount(); got != 0 {
		t.Errorf("second ctrl+a left %d selected, want 0", got)
	}
}

func TestTUIBlocksWriteCommands(t *testing.T) {
	m := pickModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{})
	m.runtime().ReplaceTenant(config.Tenant{Name: "t", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}}})
	m.runtime().SetIONUser("u")
	selectOnly(&m, "1")

	mm, cmd := m.startRun([]string{"config banner hello"})
	blocked := afterStart(t, mm.(tuiModel), cmd)
	if blocked.phase == phaseRunning {
		t.Error("write command was allowed to run")
	}
	if !strings.Contains(blocked.status, "not read-only") {
		t.Errorf("status = %q, want a read-only complaint", blocked.status)
	}

	m.cfg().AllowWrites = true
	mm, cmd = m.startRun([]string{"config banner hello"})
	if got := afterStart(t, mm.(tuiModel), cmd).status; strings.Contains(got, "not read-only") {
		t.Errorf("AllowWrites did not lift the guard: %q", got)
	}
}

func TestTheListHasARowUnderTheCursorOnceItLoads(t *testing.T) {
	m := pickModel(t)

	setTestElements(&m, nil)
	if got := m.table.Cursor(); got >= 0 {
		t.Fatalf("cursor = %d on an empty list, want the widget's own -1", got)
	}

	setTestElements(&m, []api.Element{
		{ID: "1", Name: "S0100-RTR-01", Connected: true},
		{ID: "2", Name: "S0100-RTR-02", Connected: true},
	})

	if _, ok := m.current(); !ok {
		t.Errorf("no row under the cursor after the list loaded (cursor = %d), so enter does nothing until an arrow key is pressed",
			m.table.Cursor())
	}
}

func TestNoFormGhostsARealUsername(t *testing.T) {
	m := pickModel(t)
	tenant := m.tenant()

	tests := []struct {
		name string
		open func() (tea.Model, tea.Cmd)
	}{
		{"adding an ION user", func() (tea.Model, tea.Cmd) { return m.openUserFormFor(tenant) }},
		{"asked for credentials", func() (tea.Model, tea.Cmd) { return m.openPasswordForm(tenant, "") }},
		{"editing a user", func() (tea.Model, tea.Cmd) { return m.openEditUserForm("someone") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, _ := tt.open()
			for _, f := range next.(tuiModel).form {
				if f.label != "ion user" {
					continue
				}
				if got := f.input.Placeholder; got != "" {
					t.Errorf("the ion user field ghosts %q; a username is somebody's, so there is no example to give", got)
				}
			}
		})
	}
}
