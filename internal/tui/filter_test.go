package tui

import (
	"fmt"
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

func TestGeneralSectionRowsAreAllReachable(t *testing.T) {
	m := rankModel(t)
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.width, m.height = 110, 30
	m.spinner = ui.NewSpinner("")

	if got := m.sectionRows(); got != generalRows {
		t.Fatalf("sectionRows() = %d, want %d", got, generalRows)
	}
	out := plain(m.viewSettings())
	for _, want := range []string{"Commands", "Mode", "Jump", "Times", "Elements", "views"} {
		if !strings.Contains(out, want) {
			t.Errorf("the General section is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + m.viewSettings())
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

func TestEscapeAfterApplyingTheQueryLeavesTheList(t *testing.T) {
	m := filterModel(t)
	m.phase = phasePicking
	m.filterOn = true
	m.filter.SetValue("s0100")
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	applied := mm.(tuiModel)
	if applied.filterOn || applied.filter.Value() != "s0100" {
		t.Fatalf("enter left on=%v query=%q, want the query applied and the input closed",
			applied.filterOn, applied.filter.Value())
	}

	mm, _ = applied.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}), "esc")
	got := mm.(tuiModel)
	if got.phase != phaseMenu {
		t.Errorf("phase = %v, want esc to go back rather than clear the query", got.phase)
	}
	if got.filter.Value() != "s0100" {
		t.Errorf("filter = %q, want the query left alone", got.filter.Value())
	}
}

func TestEscapeWhileTypingStillClearsTheQuery(t *testing.T) {
	m := filterModel(t)
	m.phase = phasePicking
	m.filterOn = true
	m.filter.SetValue("s0100")
	m.applyFilter()

	mm, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}), "esc")
	got := mm.(tuiModel)
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want to stay on the list", got.phase)
	}
	if got.filterOn || got.filter.Value() != "" {
		t.Errorf("on=%v query=%q, want esc to clear and close the input", got.filterOn, got.filter.Value())
	}
	if len(got.matches) != len(got.all) {
		t.Errorf("matched %d of %d, want the whole list back", len(got.matches), len(got.all))
	}
}

func TestASingleMatchBeatsAnEarlierSelection(t *testing.T) {
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

func TestBackspacingTheQueryEmptiesItThenLeaves(t *testing.T) {
	m := pickModel(t)
	m.phase = phasePicking

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = typeString(t, next.(tuiModel), "s0")
	if !m.filterOn {
		t.Fatal("/ did not open the filter")
	}

	backspace := func(m tuiModel) tuiModel {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
		return next.(tuiModel)
	}

	for range "s0" {
		m = backspace(m)
	}
	if !m.filterOn {
		t.Error("the filter closed on the press that emptied the query")
	}
	if m.filter.Value() != "" {
		t.Errorf("query = %q, want it empty", m.filter.Value())
	}
	if len(m.matches) != len(m.all) {
		t.Errorf("%d matches, want the whole list of %d", len(m.matches), len(m.all))
	}

	m = typeString(t, m, "s9")
	if m.filter.Value() != "s9" {
		t.Errorf("query = %q, want typing to continue in the filter", m.filter.Value())
	}
	for range "s9" {
		m = backspace(m)
	}

	m = backspace(m)
	if m.filterOn {
		t.Error("the filter is still open after backspacing the empty query")
	}
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	if got := next.(tuiModel).table.Cursor(); got != 1 {
		t.Errorf("cursor = %d after j, want the key to move the list again", got)
	}
}

func TestBackspacingTheMacroQueryEmptiesItThenLeaves(t *testing.T) {
	m := macroModel(t)

	next, _ := m.onMacroKey(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}), "/")
	m = typeString(t, next.(tuiModel), "ha")
	if !m.macroFilterOn || len(m.macroRows()) != 1 {
		t.Fatalf("filter on = %v, rows = %d, want a narrowed list", m.macroFilterOn, len(m.macroRows()))
	}

	for range "ha" {
		next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
		m = next.(tuiModel)
	}
	if !m.macroFilterOn {
		t.Error("the macro filter closed on the press that emptied the query")
	}
	if len(m.macroRows()) != 2 {
		t.Errorf("%d rows, want every macro back", len(m.macroRows()))
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if next.(tuiModel).macroFilterOn {
		t.Error("the macro filter is still open after backspacing the empty query")
	}
}

func TestHoldingKWalksBackUpTheList(t *testing.T) {
	m := pickModel(t)
	m.phase = phasePicking
	m.height = 12
	els := make([]api.Element, 40)
	for i := range els {
		els[i] = api.Element{ID: fmt.Sprint(i), Name: fmt.Sprintf("S%04d-RTR-01", i), Connected: true}
	}
	setTestElements(&m, els)

	for i := 0; i < 30; i++ {
		next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
		m = next.(tuiModel)
	}
	if got := m.table.Cursor(); got != 30 {
		t.Fatalf("cursor = %d after 30 j, want 30", got)
	}
	if !strings.Contains(plain(m.table.View()), m.all[30].Name) {
		t.Errorf("the row the cursor is on is not in the window:\n%s", plain(m.table.View()))
	}

	for i := 29; i >= 0; i-- {
		next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
		m = next.(tuiModel)
		if got := m.table.Cursor(); got != i {
			t.Fatalf("cursor = %d after k, want %d", got, i)
		}
		if view := plain(m.table.View()); !strings.Contains(view, m.all[i].Name) {
			t.Fatalf("row %d is highlighted but not in the window:\n%s", i, view)
		}
	}
}

func TestAltJAndAltKJumpTheList(t *testing.T) {
	m := pickModel(t)
	m.cfg.PageStep = 2
	m.table.SetCursor(0)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Mod: tea.ModAlt}))
	m = next.(tuiModel)
	if got := m.table.Cursor(); got != 2 {
		t.Errorf("cursor = %d after alt+j with a step of 2, want 2", got)
	}
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModAlt}))
	if got := next.(tuiModel).table.Cursor(); got != 0 {
		t.Errorf("cursor = %d after alt+k, want back at 0", got)
	}
}

func TestAltHAndAltLSwitchTabs(t *testing.T) {
	m := pickModel(t)
	m.data = []dataTab{{eid: "1", name: "S0100-RTR-01", tsg: "1"}}
	m.dataActive = 0
	m.phase = phasePicking

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'l', Mod: tea.ModAlt}))
	m = next.(tuiModel)
	if m.phase != phaseData {
		t.Fatalf("phase = %v after alt+l, want the data tab", m.phase)
	}
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Mod: tea.ModAlt}))
	if got := next.(tuiModel).phase; got != phasePicking {
		t.Errorf("phase = %v after alt+h, want back on the list", got)
	}
}

func TestTheSerialIsOnTheRowAndSearchable(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s5024-rtr-01", Model: "ion1200", Serial: "ION1200ABC123", Connected: true},
		{ID: "e2", Name: "s0500-rtr-01", Model: "ion1200", Serial: "ION1200XYZ789"},
	})
	m.phase = phasePicking
	m.width, m.height = 160, 30
	m.rebuildRows()

	if !m.showSerialColumn() {
		t.Fatal("the sn column is hidden on a list whose records carry serials")
	}
	out := plain(m.viewPicking())
	for _, want := range []string{"SN", "ION1200ABC123", "ION1200XYZ789"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is missing %q:\n%s", want, out)
		}
	}

	m.filter.SetValue("ION1200XYZ789")
	m.applyFilter()
	if len(m.matches) != 1 {
		t.Fatalf("%d matches for a serial, want the one element", len(m.matches))
	}
	if got := m.all[m.matches[0]].Name; got != "s0500-rtr-01" {
		t.Errorf("matched %s, want the element with that serial", got)
	}
}

func TestConnectedRowsAreGreenAndOfflineKeepsRed(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e2", Name: "s0500-rtr-01", Model: "ion1200"},
		{ID: "e1", Name: "s5024-rtr-01", Model: "ion1200", Connected: true},
	})
	m.phase = phasePicking
	m.width, m.height = 140, 30
	m.rebuildRows()

	out := m.viewPicking()
	if !strings.Contains(out, ui.StyleOK.Render("  s5024-rtr-01")) {
		t.Errorf("a connected element is not green across the line:\n%q", out)
	}
	if !strings.Contains(out, ui.StyleOK.Render("connected")) {
		t.Errorf("the state of a connected element is not green:\n%q", out)
	}
	if strings.Contains(plain(out), "offline") && strings.Contains(out, ui.StyleErr.Render("offline")) {
		t.Error("the row under the cursor kept its own color, which breaks the highlight")
	}

	m.moveCursorTo(1)
	out = m.viewPicking()
	if !strings.Contains(out, ui.StyleErr.Render("offline")) {
		t.Errorf("offline is not red once the cursor is elsewhere:\n%q", out)
	}
	if strings.Contains(out, ui.StyleOK.Render("s5024-rtr-01")) {
		t.Error("the row under the cursor is still green, which breaks the highlight")
	}
	if strings.Contains(out, ui.StyleErr.Render("connected")) {
		t.Error("connected is rendered in the error color")
	}
}

func TestColumnsAreSizedToTheirContent(t *testing.T) {
	cols := []elementColumn{
		{title: "element", min: 12, max: 28, flex: true},
		{title: "site", min: 8, max: 22, flex: true},
		{title: "SN", min: 10, max: 24, flex: true},
		{title: "state", min: 9, max: 9},
	}
	values := [][]string{
		{"store-0003-rtr-01", "Store 0003", "ION1200ABCDEF123456", "connected"},
	}

	wide := fitColumns(cols, values, 200)
	if wide[1] != len("Store 0003") {
		t.Errorf("site = %d, want the width of its widest name (%d)", wide[1], len("Store 0003"))
	}
	if wide[2] != len("ION1200ABCDEF123456") {
		t.Errorf("sn = %d, want the whole serial (%d)", wide[2], len("ION1200ABCDEF123456"))
	}

	narrow := fitColumns(cols, values, 60)
	total := 0
	for _, w := range narrow {
		total += w + colGap
	}
	if total > 60 {
		t.Errorf("columns total %d, want them shaved to 60", total)
	}
	if narrow[3] != 9 {
		t.Errorf("state = %d, want a fixed column left alone", narrow[3])
	}
	for i, c := range cols {
		if narrow[i] < c.min {
			t.Errorf("%s = %d, want never below its minimum %d", c.title, narrow[i], c.min)
		}
	}
}

func TestRenderTheElementListWithSerials(t *testing.T) {
	m := multiModel(t)
	m.mergeTenantElements("111", "Tractor Supply", []api.Element{
		{ID: "e1", Name: "s5024-rtr-01", Model: "ion 1200", Serial: "ION1200ABC123", Connected: true,
			SpokeHA: &api.SpokeHA{ClusterID: "c1"}, SiteID: "s1"},
		{ID: "e2", Name: "s5024-rtr-02", Model: "ion 1200", Serial: "ION1200DEF456", Connected: true,
			SpokeHA: &api.SpokeHA{ClusterID: "c1"}, SiteID: "s1"},
		{ID: "e3", Name: "s0500-rtr-01", Model: "ion 3200", Serial: "ION3200XYZ789"},
	})
	m.phase = phasePicking
	m.width, m.height = 170, 24
	m.ha = map[string]string{"111/e1": haActive}
	m.haCached = map[string]string{"111/e2": haStandby}
	m.haTotal, m.haDone = 2, 1
	m.rebuildRows()
	t.Log("\n" + m.viewPicking())
}

func TestATickedRowCarriesABarAgainstItsName(t *testing.T) {
	m := searchEnterModel(t)
	m.applyFilter()
	if len(m.matches) < 2 {
		t.Fatalf("need two rows to compare, got %d", len(m.matches))
	}

	nameCell := func(row int) string {
		for i, c := range m.listKeep {
			if c == colName {
				return m.table.Rows()[row][i]
			}
		}
		t.Fatal("no name column")
		return ""
	}

	el := m.all[m.matches[1]]
	m.selected = map[string]bool{el.key(): true}
	m.rebuildRows()

	ticked := nameCell(1)
	if !strings.HasPrefix(plain(ticked), "▌ ") {
		t.Errorf("the name cell is %q, want the bar against the name", plain(ticked))
	}
	if !strings.Contains(ticked, "\x1b[1m") {
		t.Errorf("the name is %q, want it bold", ticked)
	}

	plainCell := plain(nameCell(0))
	if strings.Contains(plainCell, "▌") {
		t.Errorf("an unticked row carries the bar: %q", plainCell)
	}
	if !strings.HasPrefix(plainCell, "  ") {
		t.Errorf("an unticked name is %q, want it padded into line", plainCell)
	}
	if a, b := strings.TrimLeft(plainCell, " "), strings.TrimLeft(plain(ticked), "▌ "); a == "" || b == "" {
		t.Error("a name cell came out empty")
	}

	m.selected = map[string]bool{}
	m.rebuildRows()
	if got := plain(nameCell(1)); strings.Contains(got, "▌") {
		t.Errorf("the bar survived unticking: %q", got)
	}
}

func TestTheListAndItsChromeFitTheFrame(t *testing.T) {
	m := searchEnterModel(t)
	for _, h := range []int{24, 30, 44, 60} {
		m.height, m.width = h, 120
		m.applyFilter()
		frame := plain(m.viewPicking())
		lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
		if len(lines) > h-1 {
			t.Errorf("at height %d the list draws %d lines, which the frame clips:\n%s",
				h, len(lines), frame)
		}
	}
}

func TestOpeningTheFilterAgainStartsFresh(t *testing.T) {
	m := searchEnterModel(t)

	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}), "/")
	m = next.(tuiModel)
	for _, r := range "e1" {
		next, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}), string(r))
		m = next.(tuiModel)
	}
	next, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "enter")
	m = next.(tuiModel)

	if m.filterOn {
		t.Error("enter did not leave the filter")
	}
	applied := m.filter.Value()
	if applied == "" {
		t.Fatal("enter dropped the query, which is what it is meant to keep")
	}
	narrowed := len(m.matches)
	if narrowed >= len(m.all) {
		t.Fatalf("the query matched everything (%d of %d), so there is nothing to clear",
			narrowed, len(m.all))
	}

	next, _ = m.onPickKey(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}), "/")
	m = next.(tuiModel)
	if got := m.filter.Value(); got != "" {
		t.Errorf("the filter reopened on %q, want an empty query", got)
	}
	if len(m.matches) != len(m.all) {
		t.Errorf("%d of %d rows after reopening, want the whole list back",
			len(m.matches), len(m.all))
	}
	if !m.filterOn {
		t.Error("the filter did not open")
	}
}

func TestAltAAlsoCyclesTheMode(t *testing.T) {
	m := searchEnterModel(t)
	m.phase = phasePicking

	for _, key := range []string{"alt+m", "alt+a"} {
		before := m.mode
		next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModAlt}), key)
		m = next.(tuiModel)
		if m.mode == before {
			t.Errorf("%s left the mode on %v", key, before)
		}
	}

	m.mode, m.armedMacro = modeMacro, "ha check"
	next, _ := m.onPickKey(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModAlt}), "alt+a")
	m = next.(tuiModel)
	if m.armedMacro != "" {
		t.Errorf("armedMacro = %q after cycling out of macro mode", m.armedMacro)
	}

	if out := plain(m.viewPicking()); !strings.Contains(out, "alt+m/alt+a mode") {
		t.Errorf("the hint does not offer both:\n%s", out)
	}
}
