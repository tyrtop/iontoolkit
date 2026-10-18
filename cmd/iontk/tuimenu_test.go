package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

func TestMenuEntries(t *testing.T) {
	cases := map[string]struct {
		cfg       *config.Config
		hasTenant bool
		want      []string
	}{
		"no tenant": {
			cfg:  &config.Config{},
			want: []string{"Add Tenant", "Settings", "Quit"},
		},
		"one tenant": {
			cfg:       &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}}},
			hasTenant: true,
			want: []string{
				"start ION shell for Acme Co",
				"send batch commands for Acme Co",
				"Add Tenant", "Settings", "Quit",
			},
		},
		"two tenants": {
			cfg:       &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}, {Name: "Lab"}}},
			hasTenant: true,
			want: []string{
				"start ION shell for Acme Co",
				"send batch commands for Acme Co",
				"switch tenant", "Add Tenant", "Settings", "Quit",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := tuiModel{cfg: tc.cfg, hasTenant: tc.hasTenant}
			if tc.hasTenant {
				m.tenant = tc.cfg.Tenants[0]
			}
			got := m.menuEntries()
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i].label != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i].label, tc.want[i])
				}
			}
		})
	}
}

var testANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return testANSI.ReplaceAllString(s, "") }

func TestCacheLine(t *testing.T) {
	cases := map[string]struct {
		m    tuiModel
		want string
	}{
		"no tenant": {tuiModel{}, "no tenant configured"},
		"no cache":  {tuiModel{hasTenant: true, cacheState: cacheNone}, "no cached elements"},
		"first fetch has nothing to report yet": {
			tuiModel{hasTenant: true, cacheState: cacheUpdating}, "fetching elements"},
		"updating": {tuiModel{hasTenant: true, cacheState: cacheUpdating, cacheCount: 847}, "847 elements cached (updating)"},
		"just updated": {
			tuiModel{hasTenant: true, cacheState: cacheUpdated, cacheCount: 847, cacheAt: time.Now()},
			"847 elements cached (updated)"},
		"updated a few minutes ago": {
			tuiModel{hasTenant: true, cacheState: cacheUpdated, cacheCount: 847, cacheAt: time.Now().Add(-4 * time.Minute)},
			"847 elements cached (updated)  4m ago"},
		"failed": {tuiModel{hasTenant: true, cacheState: cacheFailed}, "element refresh failed"},
		"loaded from a fresh cache": {
			tuiModel{hasTenant: true, cacheState: cacheCached, cacheCount: 5613, cacheAt: time.Now().Add(-9 * time.Minute)},
			"5613 elements cached  9m ago"},
		// A session left open long enough must stop claiming to be current.
		"an updated cache that went stale stops saying so": {
			tuiModel{hasTenant: true, cacheState: cacheUpdated, cacheCount: 5613, cacheAt: time.Now().Add(-38*time.Minute - 39*time.Second)},
			"5613 elements cached  38m ago"},
		"aged past an hour": {
			tuiModel{hasTenant: true, cacheState: cacheCached, cacheCount: 12, cacheAt: time.Now().Add(-2 * time.Hour)},
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
	m := tuiModel{selected: map[string]bool{}, width: 80, height: 24}
	m.filter = newField("", "", false).input
	m.filter.ShowSuggestions = true
	m.table = newElementTable()

	m.setElements([]api.Element{
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
	m := tuiModel{selected: map[string]bool{}, width: 80, height: 24}
	m.filter = newField("", "", false).input
	m.table = newElementTable()
	m.sites = map[string]string{"s1": "Nashville Hub", "s2": "Dallas"}

	m.setElements([]api.Element{
		{ID: "1", Name: "S0100-RTR-01", SiteID: "s1"},
		{ID: "2", Name: "S0100-RTR-02", SiteID: "s1"},
		{ID: "3", Name: "S9999-RTR-01", SiteID: "s2"},
	})

	m.filter.SetValue("100")
	m.applyFilter()
	if len(m.matches) != 2 {
		t.Errorf("substring filter matched %d, want 2", len(m.matches))
	}

	m.filter.SetValue("nashville")
	m.applyFilter()
	if len(m.matches) != 2 {
		t.Errorf("site filter matched %d, want 2", len(m.matches))
	}

	m.filter.SetValue("dallas")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Errorf("site filter matched %d, want 1", len(m.matches))
	}
}

func TestSiteNameFallsBackToID(t *testing.T) {
	m := tuiModel{sites: map[string]string{"s1": "Nashville Hub"}}
	if got := m.siteName(api.Element{SiteID: "s1"}); got != "Nashville Hub" {
		t.Errorf("siteName = %q, want the resolved name", got)
	}
	if got := m.siteName(api.Element{SiteID: "unknown"}); got != "unknown" {
		t.Errorf("siteName = %q, want the raw id", got)
	}
	if got := m.siteName(api.Element{}); got != "-" {
		t.Errorf("siteName = %q, want -", got)
	}
}

func TestSpinnerCycle(t *testing.T) {
	if got := spinnerName("nonsense"); got != defaultSpinner {
		t.Errorf("unknown name = %q, want %q", got, defaultSpinner)
	}
	seen := map[string]bool{}
	name := defaultSpinner
	for range spinnerChoices {
		if seen[name] {
			t.Fatalf("cycle repeated %q early", name)
		}
		seen[name] = true
		name = nextSpinnerName(name)
	}
	if name != defaultSpinner {
		t.Errorf("cycle ended on %q, want to wrap to %q", name, defaultSpinner)
	}
}

func TestSplitCommands(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want []string
	}{
		"single":           {[]string{"uptime"}, []string{"uptime"}},
		"comma separated":  {[]string{"a,b,c"}, []string{"a", "b", "c"}},
		"repeated flag":    {[]string{"a", "b"}, []string{"a", "b"}},
		"mixed":            {[]string{"a,b", "c"}, []string{"a", "b", "c"}},
		"trims whitespace": {[]string{" a , b "}, []string{"a", "b"}},
		"drops blanks":     {[]string{"a,,b", ""}, []string{"a", "b"}},
		"nil input":        {nil, nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := splitCommands(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
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

func TestBannerDegradesOnSmallTerminals(t *testing.T) {
	if got := bannerHeight(80, 24); got != len(bannerRows)+1 {
		t.Errorf("bannerHeight(80,24) = %d, want %d", got, len(bannerRows)+1)
	}
	if got := bannerHeight(80, 10); got != 1 {
		t.Errorf("short terminal bannerHeight = %d, want 1", got)
	}
	if got := bannerHeight(20, 40); got != 1 {
		t.Errorf("narrow terminal bannerHeight = %d, want 1", got)
	}
	for _, r := range bannerRows {
		if len(r) != bannerWidth {
			t.Errorf("banner row %q is %d wide, want %d", r, len(r), bannerWidth)
		}
	}
}

func TestRenderMenu(t *testing.T) {
	m := tuiModel{
		cfg:        &config.Config{Tenants: []config.Tenant{{Name: "Acme Co"}, {Name: "Lab"}}},
		hasTenant:  true,
		tenant:     config.Tenant{Name: "Acme Co"},
		cacheState: cacheUpdating,
		cacheCount: 847,
		width:      80,
		height:     24,
	}
	t.Log("\n" + m.viewMenu())
}

func TestPasteReachesFocusedInput(t *testing.T) {
	base := tuiModel{
		cfg:      &config.Config{},
		selected: map[string]bool{},
		width:    80,
		height:   24,
	}
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
		m.all = []api.Element{{ID: "1", Name: "S0103-RTR-01"}}

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
		if len(m2.matches) != 1 {
			t.Errorf("paste did not reapply the filter: %d matches", len(m2.matches))
		}
	})
}

func TestRenderSettings(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	m := tuiModel{
		store: st,
		cfg: &config.Config{
			LastTenant: "Acme Co",
			Tenants: []config.Tenant{
				{Name: "Acme Co", TSGID: "1234567890", Users: []config.IONUser{
					{Name: "readonly", Default: true}, {Name: "cisops"},
				}},
				{Name: "Lab", TSGID: "9876543210"},
			},
		},
		spinnerName: defaultSpinner,
		spinner:     newSpinner(defaultSpinner),
		width:       80,
		height:      24,
	}
	t.Log("\n" + m.viewSettings())
}

func TestEnterSavesFromAnyField(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))

	m := tuiModel{store: st, cfg: &config.Config{}, selected: map[string]bool{}}
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
	if !m.hasTenant || m.tenant.Name != "Acme Co" || m.tenant.TSGID != "1234567890" {
		t.Errorf("tenant = %+v", m.tenant)
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

	m := tuiModel{store: st, cfg: &config.Config{}, selected: map[string]bool{}}
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
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = "u"
	if err := st.Secrets.Set(config.IONPassKey("1", "u"), "pw"); err != nil {
		t.Fatal(err)
	}
	m.phase = phasePicking
	m.setElements([]api.Element{
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
	if len(m.matches) != 2 {
		t.Fatalf("filter matched %d, want 2", len(m.matches))
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

func TestSpaceSelectsWhileFiltering(t *testing.T) {
	m := pickModel(t)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	m = typeString(t, m, "s0100")

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mm.(tuiModel)

	if len(m.selected) != 1 || !m.selected["1"] {
		t.Errorf("selected = %v, want element 1", m.selected)
	}
	if got := m.filter.Value(); got != "s0100" {
		t.Errorf("space typed into the filter: %q", got)
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
	if len(m.matches) != 3 {
		t.Errorf("matches = %d, want all 3 back", len(m.matches))
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if got := mm.(tuiModel).phase; got != phaseMenu {
		t.Errorf("second esc phase = %v, want phaseMenu", got)
	}
}

func TestBatchModeEnterGoesToCommandEntry(t *testing.T) {
	m := pickModel(t)
	m.mode = modeBatch

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

	if len(m.selected) != 2 {
		t.Fatalf("selected %d, want the 2 filtered elements", len(m.selected))
	}
	if m.selected["3"] {
		t.Error("ctrl+a selected an element outside the filter")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	if got := len(mm.(tuiModel).selected); got != 0 {
		t.Errorf("second ctrl+a left %d selected, want 0", got)
	}
}

func TestTUIBlocksWriteCommands(t *testing.T) {
	m := pickModel(t)
	m.cfg = &config.Config{}
	m.tenant = config.Tenant{Name: "t", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}}}
	m.ionUser = "u"
	m.selected = map[string]bool{"1": true}

	mm, _ := m.startRun([]string{"config banner hello"})
	blocked := mm.(tuiModel)
	if blocked.phase == phaseRunning {
		t.Error("write command was allowed to run")
	}
	if !strings.Contains(blocked.status, "not read-only") {
		t.Errorf("status = %q, want a read-only complaint", blocked.status)
	}

	m.cfg.AllowWrites = true
	mm, _ = m.startRun([]string{"config banner hello"})
	if got := mm.(tuiModel).status; strings.Contains(got, "not read-only") {
		t.Errorf("AllowWrites did not lift the guard: %q", got)
	}
}
