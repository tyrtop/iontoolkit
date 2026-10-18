package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

func filterModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.sites = map[string]string{
		"111/s100": "Store 0100",
		"111/s200": "Store 0200",
		"222/s9":   "ORD Plant",
	}
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s0100-rtr-01", SiteID: "s100", Model: "ion1200", Software: "6.5.1", HWID: "HW-AAA", Connected: true},
		{ID: "e2", Name: "s0100-rtr-02", SiteID: "s100", Model: "ion3200", Software: "6.5.1", HWID: "HW-BBB"},
		{ID: "e3", Name: "s0200-rtr-01", SiteID: "s200", Model: "ion1200", Software: "6.4.3", HWID: "HW-CCC", Connected: true},
	})
	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{
		{ID: "e4", Name: "ord-gw-01", SiteID: "s9", Model: "ion1200", Software: "6.5.1", HWID: "HW-DDD", Connected: true},
	})
	return m
}

func matchedNames(m tuiModel) []string {
	var out []string
	for _, i := range m.matches {
		out = append(out, m.all[i].Name)
	}
	return out
}

func TestFilterCombinesTermsAcrossFields(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			// The case that prompted this: a term each for site, model and tenant.
			name:  "site, model and tenant together",
			query: "100 1200 tract",
			want:  []string{"s0100-rtr-01"},
		},
		{"nothing typed matches everything", "", []string{"ord-gw-01", "s0100-rtr-01", "s0100-rtr-02", "s0200-rtr-01"}},
		{"one term still works", "1200", []string{"ord-gw-01", "s0100-rtr-01", "s0200-rtr-01"}},
		{"tenant name alone", "niagara", []string{"ord-gw-01"}},
		{"model and tenant", "1200 niagara", []string{"ord-gw-01"}},
		{"site name words", "store 0200", []string{"s0200-rtr-01"}},
		{"software version", "6.4.3", []string{"s0200-rtr-01"}},
		{"hardware id", "hw-ccc", []string{"s0200-rtr-01"}},
		{"connection state", "offline", []string{"s0100-rtr-02"}},
		{"state and model", "connected ion3200", nil},
		{"element id", "e4", []string{"ord-gw-01"}},
		{"terms in any order", "tract 1200 100", []string{"s0100-rtr-01"}},
		{"a term matching nothing excludes everything", "1200 nosuchtenant", nil},
		{"case is ignored", "TRACT ION1200", []string{"s0100-rtr-01", "s0200-rtr-01"}},
		{"extra spaces are ignored", "  1200   niagara  ", []string{"ord-gw-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := filterModel(t)
			m.filter.SetValue(tt.query)
			m.applyFilter()

			got := matchedNames(m)
			if len(got) != len(tt.want) {
				t.Fatalf("query %q matched %v, want %v", tt.query, got, tt.want)
			}
			have := map[string]bool{}
			for _, n := range got {
				have[n] = true
			}
			for _, w := range tt.want {
				if !have[w] {
					t.Errorf("query %q matched %v, missing %q", tt.query, got, w)
				}
			}
		})
	}
}

func TestFilterDoesNotGetLooseWithShortTerms(t *testing.T) {
	m := filterModel(t)
	// Two characters are too few to match out of order: nearly everything would
	// qualify, which makes the filter useless rather than forgiving.
	m.filter.SetValue("or")
	m.applyFilter()
	for _, i := range m.matches {
		if !strings.Contains(strings.ToLower(m.all[i].Name+m.all[i].Tenant), "or") {
			t.Errorf("%q matched a two-character term it does not contain", m.all[i].Name)
		}
	}
}

func TestFilterNarrowsAcrossTenantsAtOnce(t *testing.T) {
	m := filterModel(t)
	m.filter.SetValue("1200 connected")
	m.applyFilter()

	tenants := map[string]bool{}
	for _, i := range m.matches {
		tenants[m.all[i].TSG] = true
	}
	if len(tenants) != 2 {
		t.Errorf("matches came from %d tenants, want both: %v", len(tenants), matchedNames(m))
	}
}

func TestCountLabelShowsTheNarrowing(t *testing.T) {
	m := filterModel(t)
	m.applyFilter()
	if got := m.countLabel(); !strings.Contains(got, "4 elements") || strings.Contains(got, " of ") {
		t.Errorf("countLabel() = %q, want a plain total with no filter running", got)
	}

	m.filter.SetValue("1200 tract")
	m.applyFilter()
	if got := m.countLabel(); !strings.Contains(got, "2 of 4 elements") {
		t.Errorf("countLabel() = %q, want the match count against the total", got)
	}
}

func TestFilterKeepsTheCursorInRange(t *testing.T) {
	m := filterModel(t)
	m.applyFilter()
	m.table.SetCursor(3)

	m.filter.SetValue("niagara")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Fatalf("matched %d, want 1", len(m.matches))
	}
	if c := m.table.Cursor(); c != 0 {
		t.Errorf("cursor = %d, want it pulled back into range", c)
	}
}

func TestFilterWithNoTenantColumnStillSearchesTenant(t *testing.T) {
	// A single tenant hides the column, but its name should still be a usable
	// search term.
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "e1", Name: "s0100-rtr-01"}})
	if m.showTenantColumn() {
		t.Fatal("one tenant should not show the column")
	}
	m.filter.SetValue("tractor")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Errorf("matched %d, want the tenant name to still apply", len(m.matches))
	}
}

func TestRenderFilteredCrossTenantList(t *testing.T) {
	m := filterModel(t)
	m.width, m.height = 110, 30
	m.spinner = ui.NewSpinner("")
	m.cacheState = cacheUpdated
	m.phase = phasePicking
	m.filter.SetValue("1200 connected")
	m.applyFilter()

	out := plain(m.viewPicking())
	for _, want := range []string{"s0100-rtr-01", "ord-gw-01", "3 of 4 elements"} {
		if !strings.Contains(out, want) {
			t.Errorf("the filtered list is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s0100-rtr-02") {
		t.Errorf("an offline element survived a \"connected\" filter:\n%s", out)
	}
	t.Log("\n" + m.viewPicking())
}

var _ = config.AuthBrowser

// rankModel holds the store numbers that exposed the ranking problem: s5024 is
// what is wanted, and s0500 is the name that an in-order match drags in.
func rankModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s5024-rtr-01", Model: "ion1200", Connected: true},
		{ID: "e2", Name: "s0500-rtr-01", Model: "ion1200", Connected: true},
		{ID: "e3", Name: "s0502-rtr-01", Model: "ion1200", Connected: true},
		{ID: "e4", Name: "s5024-rtr-02", Model: "ion1200", Connected: true},
	})
	return m
}

func TestEveryTermStillHasToMatch(t *testing.T) {
	m := rankModel(t)
	m.filter.SetValue("s5024 ion9999")
	m.applyFilter()
	if got := matchedNames(m); len(got) != 0 {
		t.Errorf("matched %v, want nothing when one term matches nothing", got)
	}
}

func TestGeneralSectionRowsAreAllReachable(t *testing.T) {
	m := rankModel(t)
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.width, m.height = 110, 30
	m.spinner = ui.NewSpinner("")

	if got := m.sectionRows(); got != 4 {
		t.Fatalf("sectionRows() = %d, want 4", got)
	}
	out := plain(m.viewSettings())
	for _, want := range []string{"spinner", "commands", "mode", "elements"} {
		if !strings.Contains(out, want) {
			t.Errorf("the General section is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewSettings())
}

// storeModel reproduces the names behind the second ranking report: store
// numbers are padded, so "s502" contains-matches none of them and every match
// is out of order.
func storeModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s0502-rtr-01", Model: "ion1200", Connected: true},
		{ID: "e2", Name: "s1405-rtr-02", Model: "ion1200", Connected: true},
		{ID: "e3", Name: "s1405-rtr-01", Model: "ion1200", Connected: true},
		{ID: "e4", Name: "s0502-rtr-02", Model: "ion1200", Connected: true},
		{ID: "e5", Name: "s2500-rtr-02", Model: "ion1200", Connected: true},
	})
	return m
}

func TestYourStoreQueryWorks(t *testing.T) {
	// The query that has to work: a store number, a model split over two words,
	// and nothing matching out of order.
	m := multiModel(t)
	m.sites = map[string]string{"111/s50": "Store 5024"}
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "1695401228372008796", Name: "s5024-rtr-01", SiteID: "s50", Model: "ion 1200", Connected: true},
		{ID: "1695401228372008797", Name: "s1405-rtr-01", SiteID: "x", Model: "ion 2000", Connected: true},
		{ID: "1695401228372008798", Name: "s0502-rtr-01", SiteID: "x", Model: "ion 3200", Connected: true},
	})

	m.filter.SetValue("s50 ion 1200")
	m.applyFilter()
	got := matchedNames(m)
	if len(got) != 1 || got[0] != "s5024-rtr-01" {
		t.Errorf("matched %v, want only s5024-rtr-01", got)
	}
}

func TestOnlyNamesThatContainTheTermMatch(t *testing.T) {
	// No out-of-order matching at all now: s502 must not reach s1405.
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s1405-rtr-02", Model: "ion 2000"},
		{ID: "e2", Name: "s0502-rtr-01", Model: "ion 1200"},
		{ID: "e3", Name: "s502-rtr-01", Model: "ion 1200"},
	})

	m.filter.SetValue("s502")
	m.applyFilter()
	got := matchedNames(m)
	if len(got) != 1 || got[0] != "s502-rtr-01" {
		t.Errorf("matched %v, want only the name that contains s502", got)
	}

	// And a padded store number is found by typing it as written.
	m.filter.SetValue("s0502")
	m.applyFilter()
	if got := matchedNames(m); len(got) != 1 || got[0] != "s0502-rtr-01" {
		t.Errorf("matched %v, want only s0502-rtr-01", got)
	}
}

func TestOpaqueIdentifiersNeedTheWholeValue(t *testing.T) {
	// An element id is a 19-digit number, so a partial match in one is
	// coincidence. "1200" found an ion 2000 this way.
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "1695401228372008796", Name: "s1405-rtr-01", SiteID: "1695401228372008796", Model: "ion 2000"},
	})

	for _, partial := range []string{"16954", "372008", "228372"} {
		m.filter.SetValue(partial)
		m.applyFilter()
		if got := matchedNames(m); len(got) != 0 {
			t.Errorf("query %q matched %v, want nothing for a partial identifier", partial, got)
		}
	}

	// The whole value still works, for pasting one in.
	m.filter.SetValue("1695401228372008796")
	m.applyFilter()
	if got := matchedNames(m); len(got) != 1 {
		t.Errorf("matched %v, want the element found by its full id", got)
	}
}

func TestAnUnresolvedSiteIsTreatedAsOpaque(t *testing.T) {
	// When site names do not resolve the column shows a raw id, which must not
	// become a substring haystack.
	m := multiModel(t)
	m.sites = map[string]string{} // nothing resolved
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s1405-rtr-01", SiteID: "1695401228372008796", Model: "ion 2000"},
	})

	m.filter.SetValue("12283")
	m.applyFilter()
	if got := matchedNames(m); len(got) != 0 {
		t.Errorf("matched %v, want nothing from inside an unresolved site id", got)
	}

	// A resolved site name is searchable as normal.
	m.sites = map[string]string{"111/1695401228372008796": "Store 1405"}
	m.applyFilter()
	m.filter.SetValue("store 1405")
	m.applyFilter()
	if got := matchedNames(m); len(got) != 1 {
		t.Errorf("matched %v, want the resolved site name to be searchable", got)
	}
}

func TestResultsStayInNameOrder(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s5024-rtr-02", Model: "ion 1200"},
		{ID: "e2", Name: "s5024-rtr-01", Model: "ion 1200"},
	})
	m.filter.SetValue("s5024")
	m.applyFilter()
	got := matchedNames(m)
	if len(got) != 2 || got[0] != "s5024-rtr-01" {
		t.Errorf("matched %v, want them in name order", got)
	}
}

// searchEnterModel is a connected, credentialled single tenant so that acting
// on a search result reaches the dial rather than a credential prompt.
func searchEnterModel(t *testing.T) tuiModel {
	t.Helper()
	m := multiModel(t)
	m.cfg.Tenants = m.cfg.Tenants[:1]
	m.cfg.Tenants[0].Users = []config.IONUser{{Name: "ops", Default: true}}
	m.tenant = m.cfg.Tenants[0]
	if err := m.store.Secrets.Set(config.IONPassKey("111", "ops"), "pw"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s5024-rtr-01", Model: "ion 1200", Connected: true},
		{ID: "e2", Name: "s5024-rtr-02", Model: "ion 1200", Connected: true},
		{ID: "e3", Name: "s0100-rtr-01", Model: "ion 1200", Connected: true},
	})
	setTestClient(&m, liveClient())
	m.sessions = ionterm.NewManager()
	m.spinner = ui.NewSpinner("")
	m.phase = phasePicking
	m.width, m.height = 110, 30
	return m
}

func TestOneMatchInShellModeGoesStraightIn(t *testing.T) {
	m := searchEnterModel(t)
	m.mode = modeShell
	m.filterOn = true
	m.filter.SetValue("rtr-02")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Fatalf("query matched %d, want exactly 1", len(m.matches))
	}

	mm, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	got := mm.(tuiModel)

	if got.filterOn {
		t.Error("still in search mode")
	}
	if got.phase != phaseSession {
		t.Fatalf("phase = %v, want the session opened", got.phase)
	}
	if got.connecting != "s5024-rtr-02" {
		t.Errorf("connecting = %q, want the single match", got.connecting)
	}
	if cmd == nil {
		t.Error("no command returned, want the dial started")
	}
}

func TestOneMatchInBatchModeSelectsAndStays(t *testing.T) {
	m := searchEnterModel(t)
	m.mode = modeBatch
	m.filterOn = true
	m.filter.SetValue("rtr-02")
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	got := mm.(tuiModel)

	if got.filterOn {
		t.Error("still in search mode")
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want to stay on the list in batch mode", got.phase)
	}
	if len(got.selected) != 1 || !got.selected["111/e2"] {
		t.Errorf("selected = %v, want the single match selected", got.selected)
	}
	if got.connecting != "" {
		t.Error("batch mode started a session")
	}
}

func TestSeveralMatchesJustApplyTheFilter(t *testing.T) {
	for _, mode := range []pickMode{modeShell, modeBatch} {
		t.Run(mode.String(), func(t *testing.T) {
			m := searchEnterModel(t)
			m.mode = mode
			m.filterOn = true
			m.filter.SetValue("s5024")
			m.applyFilter()
			if len(m.matches) != 2 {
				t.Fatalf("query matched %d, want 2 so the choice is ambiguous", len(m.matches))
			}

			mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
			got := mm.(tuiModel)

			if got.filterOn {
				t.Error("still in search mode")
			}
			if got.phase != phasePicking {
				t.Errorf("phase = %v, want to stay on the narrowed list", got.phase)
			}
			if len(got.selected) != 0 {
				t.Errorf("selected = %v, want nothing chosen for an ambiguous query", got.selected)
			}
			if got.filter.Value() != "s5024" {
				t.Errorf("filter = %q, want the query kept", got.filter.Value())
			}
		})
	}
}

func TestASingleMatchBeatsAnEarlierSelection(t *testing.T) {
	// Selections survive filtering, so a search that lands on one ION has to
	// act on that one rather than on what was ticked before.
	m := searchEnterModel(t)
	m.mode = modeShell
	m.selected["111/e3"] = true
	m.filterOn = true
	m.filter.SetValue("rtr-02")
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	got := mm.(tuiModel)
	if got.connecting != "s5024-rtr-02" {
		t.Errorf("connecting = %q, want the searched-for ION, not the earlier selection", got.connecting)
	}
}

func TestASingleMatchStillChecksCredentials(t *testing.T) {
	m := searchEnterModel(t)
	m.mode = modeShell
	if err := m.store.Secrets.Delete(config.IONPassKey("111", "ops")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	m.filterOn = true
	m.filter.SetValue("rtr-02")
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	got := mm.(tuiModel)
	if got.phase != phasePassword {
		t.Errorf("phase = %v, want the credential prompt rather than a doomed dial", got.phase)
	}
}

func TestFilterHintSaysWhatEnterWillDo(t *testing.T) {
	tests := []struct {
		name  string
		mode  pickMode
		query string
		want  string
	}{
		{"ambiguous", modeShell, "s5024", "enter apply"},
		{"single match in shell", modeShell, "rtr-02", "enter open"},
		{"single match in batch", modeBatch, "rtr-02", "enter select"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := searchEnterModel(t)
			m.mode = tt.mode
			m.filterOn = true
			m.filter.SetValue(tt.query)
			m.applyFilter()

			if out := plain(m.viewPicking()); !strings.Contains(out, tt.want) {
				t.Errorf("the hint is missing %q:\n%s", tt.want, out)
			}
		})
	}
}

func TestClearingTheSelection(t *testing.T) {
	m := searchEnterModel(t)
	for _, r := range m.all {
		m.selected[r.key()] = true
	}

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	got := mm.(tuiModel)
	if len(got.selected) != 0 {
		t.Errorf("selected = %v, want it emptied", got.selected)
	}
	if !strings.Contains(got.status, "cleared 3") {
		t.Errorf("status = %q, want the count reported", got.status)
	}
	// Every element is listed again.
	if len(got.matches) != 3 {
		t.Errorf("matched %d, want the full list back", len(got.matches))
	}

	mm, _ = got.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	if again := mm.(tuiModel); !strings.Contains(again.status, "nothing selected") {
		t.Errorf("status = %q, want it to say there was nothing to clear", again.status)
	}
}

func TestShowingOnlyTheSelection(t *testing.T) {
	m := searchEnterModel(t)
	m.selected["111/e2"] = true

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModAlt}), "alt+s")
	got := mm.(tuiModel)
	if !got.showSelected {
		t.Fatal("alt+s did not switch to the selection")
	}
	if len(got.matches) != 1 {
		t.Fatalf("matched %d, want only the selected element", len(got.matches))
	}
	if got.all[got.matches[0]].ID != "e2" {
		t.Errorf("listed %q, want the selected element", got.all[got.matches[0]].ID)
	}
	if out := plain(got.viewPicking()); !strings.Contains(out, "selected only") {
		t.Errorf("the list does not say it is narrowed:\n%s", out)
	}

	// And back again.
	mm, _ = got.onPickKey(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModAlt}), "alt+s")
	back := mm.(tuiModel)
	if back.showSelected {
		t.Error("alt+s did not switch back")
	}
	if len(back.matches) != 3 {
		t.Errorf("matched %d, want the full list back", len(back.matches))
	}
}

func TestShowingTheSelectionNeedsASelection(t *testing.T) {
	m := searchEnterModel(t)
	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModAlt}), "alt+s")
	got := mm.(tuiModel)
	if got.showSelected {
		t.Error("switched to an empty selection, which would be an empty list")
	}
	if !strings.Contains(got.status, "nothing selected") {
		t.Errorf("status = %q, want an explanation", got.status)
	}
}

func TestClearingWhileShowingTheSelectionRestoresTheList(t *testing.T) {
	// Clearing the selection while only it is shown would otherwise leave an
	// empty list with no obvious way back.
	m := searchEnterModel(t)
	m.selected["111/e2"] = true
	m.showSelected = true
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	got := mm.(tuiModel)
	if got.showSelected {
		t.Error("still showing only the selection after clearing it")
	}
	if len(got.matches) != 3 {
		t.Errorf("matched %d, want the full list", len(got.matches))
	}
}

func TestSelectedOnlyStillHonoursTheQuery(t *testing.T) {
	m := searchEnterModel(t)
	m.selected["111/e1"] = true
	m.selected["111/e3"] = true
	m.showSelected = true
	m.filter.SetValue("s5024")
	m.applyFilter()

	// e1 is s5024-rtr-01 and is selected; e3 is s0100 and is selected but does
	// not match the query.
	if len(m.matches) != 1 {
		t.Fatalf("matched %d, want the intersection of query and selection", len(m.matches))
	}
	if m.all[m.matches[0]].ID != "e1" {
		t.Errorf("listed %q, want e1", m.all[m.matches[0]].ID)
	}
}

func TestJAndKMoveTheList(t *testing.T) {
	m := searchEnterModel(t)
	m.applyFilter()
	if m.table.Cursor() != 0 {
		t.Fatalf("cursor = %d, want 0 to start", m.table.Cursor())
	}

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}), "j")
	down := mm.(tuiModel)
	if down.table.Cursor() != 1 {
		t.Errorf("cursor = %d after j, want 1", down.table.Cursor())
	}

	mm, _ = down.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}), "k")
	if up := mm.(tuiModel); up.table.Cursor() != 0 {
		t.Errorf("cursor = %d after k, want 0", up.table.Cursor())
	}
}

func TestScopeIsComputedOnceNotPerRender(t *testing.T) {
	// Walking every element on each render is what made holding a movement key
	// lag, so the label and the tenant count come from the list changing.
	m := multiModel(t)
	if got := m.scopeLabel(); got != "Tractor Supply" {
		t.Errorf("scopeLabel() = %q with nothing loaded, want the current tenant", got)
	}

	m.mergeTenantElements("111", "Tractor Supply", []api.Element{{ID: "a"}})
	if m.tenantCount != 1 {
		t.Errorf("tenantCount = %d, want 1", m.tenantCount)
	}
	if got := m.scopeLabel(); got != "Tractor Supply" {
		t.Errorf("scopeLabel() = %q, want the single tenant", got)
	}
	if m.showTenantColumn() {
		t.Error("the tenant column showed for one tenant")
	}

	m.mergeTenantElements("222", "Niagara Bottling", []api.Element{{ID: "c"}})
	if m.tenantCount != 2 {
		t.Errorf("tenantCount = %d, want 2", m.tenantCount)
	}
	if got := m.scopeLabel(); got != "2 tenants" {
		t.Errorf("scopeLabel() = %q, want a count", got)
	}
	if !m.showTenantColumn() {
		t.Error("the tenant column did not show for two tenants")
	}

	// Emptying one tenant brings it back down.
	m.mergeTenantElements("222", "Niagara Bottling", nil)
	if m.tenantCount != 1 {
		t.Errorf("tenantCount = %d, want 1 after the second tenant emptied", m.tenantCount)
	}
}

func TestUndoingAClearedSelection(t *testing.T) {
	for _, key := range []string{"alt+z", "ctrl+z"} {
		t.Run(key, func(t *testing.T) {
			m := searchEnterModel(t)
			m.selected["111/e1"] = true
			m.selected["111/e2"] = true
			m.applyFilter()

			mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
			cleared := mm.(tuiModel)
			if len(cleared.selected) != 0 {
				t.Fatalf("selected = %v, want it cleared", cleared.selected)
			}
			if !strings.Contains(cleared.status, "alt+z to undo") {
				t.Errorf("status = %q, want the undo offered", cleared.status)
			}

			mm, _ = cleared.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), key)
			got := mm.(tuiModel)
			if len(got.selected) != 2 {
				t.Fatalf("selected = %v, want both restored", got.selected)
			}
			if !got.selected["111/e1"] || !got.selected["111/e2"] {
				t.Errorf("selected = %v, want the same two elements", got.selected)
			}
			if !strings.Contains(got.status, "restored 2") {
				t.Errorf("status = %q, want the count reported", got.status)
			}
		})
	}
}

func TestUndoIsOnlyOfferedWhenThereIsSomethingToUndo(t *testing.T) {
	m := searchEnterModel(t)
	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), "alt+z")
	got := mm.(tuiModel)
	if !strings.Contains(got.status, "nothing to undo") {
		t.Errorf("status = %q, want it to say so", got.status)
	}

	// And it is spent after one use.
	m.selected["111/e1"] = true
	mm, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	mm, _ = mm.(tuiModel).onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), "alt+z")
	again := mm.(tuiModel)
	mm, _ = again.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), "alt+z")
	if twice := mm.(tuiModel); !strings.Contains(twice.status, "nothing to undo") {
		t.Errorf("status = %q, want the undo spent after one use", twice.status)
	}
}

func TestUndoRestoresRatherThanReplaces(t *testing.T) {
	// Anything picked after the clear has to survive the undo.
	m := searchEnterModel(t)
	m.selected["111/e1"] = true

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	m = mm.(tuiModel)
	m.selected["111/e3"] = true

	mm, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), "alt+z")
	got := mm.(tuiModel)
	if len(got.selected) != 2 || !got.selected["111/e1"] || !got.selected["111/e3"] {
		t.Errorf("selected = %v, want the restored and the newly picked", got.selected)
	}
}

func TestUndoBringsBackTheSelectionOnlyView(t *testing.T) {
	m := searchEnterModel(t)
	m.selected["111/e2"] = true
	m.showSelected = true
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	cleared := mm.(tuiModel)
	if cleared.showSelected {
		t.Fatal("clearing left the selection-only view on an empty list")
	}

	mm, _ = cleared.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModAlt}), "alt+z")
	got := mm.(tuiModel)
	if !got.showSelected {
		t.Error("undo did not restore the selection-only view")
	}
	if len(got.matches) != 1 {
		t.Errorf("matched %d, want the restored selection listed", len(got.matches))
	}
}

func TestUndoDoesNotAliasTheClearedSelection(t *testing.T) {
	// The model is copied by value, so keeping a reference to the live map
	// would make the undo see later edits instead of what was cleared.
	m := searchEnterModel(t)
	m.selected["111/e1"] = true

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	cleared := mm.(tuiModel)
	cleared.selected["111/e3"] = true

	if cleared.lastCleared["111/e3"] {
		t.Error("the stored clear picked up a later selection, so the maps are shared")
	}
	if !cleared.lastCleared["111/e1"] {
		t.Error("the stored clear lost what was actually cleared")
	}
}

func TestHintOffersUndoAfterAClear(t *testing.T) {
	m := searchEnterModel(t)
	m.selected["111/e1"] = true

	if out := plain(m.viewPicking()); !strings.Contains(out, "alt+c clear") {
		t.Errorf("the hint does not offer the clear:\n%s", out)
	}

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModAlt}), "alt+c")
	got := mm.(tuiModel)
	out := plain(got.viewPicking())
	if !strings.Contains(out, "alt+z undo clear") {
		t.Errorf("the hint does not offer the undo:\n%s", out)
	}
}

func typeIntoFilter(t *testing.T, m tuiModel, query string) tuiModel {
	t.Helper()
	next, cmd := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}), "/")
	m = next.(tuiModel)
	if cmd != nil {
		cmd()
	}
	if !m.filterOn {
		t.Fatal("filterOn = false after /, want the search open")
	}
	for _, r := range query {
		msg := tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)})
		next, _ := m.onPickKey(msg, string(r))
		m = next.(tuiModel)
	}
	return m
}

func TestSearchAcceptsJAndK(t *testing.T) {
	m := filterModel(t)
	m.mergeTenantElements("333", "Kroger", []api.Element{
		{ID: "e9", Name: "jfk-gw-01", SiteID: "s1", Model: "ion1200"},
	})

	m = typeIntoFilter(t, m, "jfk")
	if got := m.filter.Value(); got != "jfk" {
		t.Fatalf("filter.Value() = %q, want %q", got, "jfk")
	}
	if got := matchedNames(m); len(got) != 1 || got[0] != "jfk-gw-01" {
		t.Errorf("matches = %q, want just %q", got, "jfk-gw-01")
	}
}

func TestSearchAcceptsHomeAndEndAsText(t *testing.T) {
	m := filterModel(t)
	m = typeIntoFilter(t, m, "100")

	cursor := 0
	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}), "home")
	m = next.(tuiModel)
	if got := m.filter.Position(); got != cursor {
		t.Errorf("filter.Position() = %d after home, want %d", got, cursor)
	}
	next, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}), "end")
	m = next.(tuiModel)
	if got := m.filter.Position(); got != len("100") {
		t.Errorf("filter.Position() = %d after end, want %d", got, len("100"))
	}
}

func TestJAndKStillMoveTheListWhenNotSearching(t *testing.T) {
	m := filterModel(t)
	m.applyFilter()
	if m.filterOn {
		t.Fatal("filterOn = true before any search, want it closed")
	}

	m.table.SetCursor(1)
	start := m.table.Cursor()
	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}), "j")
	m = next.(tuiModel)
	if got := m.table.Cursor(); got == start {
		t.Errorf("table cursor = %d after j, want it to have moved from %d", got, start)
	}
	next, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}), "k")
	m = next.(tuiModel)
	if got := m.table.Cursor(); got != start {
		t.Errorf("table cursor = %d after k, want it back at %d", got, start)
	}
	if got := m.filter.Value(); got != "" {
		t.Errorf("filter.Value() = %q, want it untouched", got)
	}
}

func TestArrowsStillMoveTheListWhileSearching(t *testing.T) {
	m := filterModel(t)
	m = typeIntoFilter(t, m, "1200")

	// Arrows are not text, so a narrowed list stays navigable mid-query.
	m.table.SetCursor(0)
	start := m.table.Cursor()
	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}), "down")
	m = next.(tuiModel)
	if got := m.table.Cursor(); got == start {
		t.Errorf("table cursor = %d after down, want it to have moved from %d", got, start)
	}
	if got := m.filter.Value(); got != "1200" {
		t.Errorf("filter.Value() = %q, want the query intact", got)
	}
}
