package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
)

const haFixtureYAML = `
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
        ha: true
      - name: store-0003-rtr-02
        site: Store 0003
        ha: true
`

// The whole mode end to end over the fixture transport: pick an element in
// rich-data mode and read the page. Every view is fetched at once and laid
// out one after another, so this is also the check that nothing is behind a
// view switch any more.
func TestRichDataModeLoadsEveryViewOntoOnePage(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{
		{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID},
	})
	m = next.(tuiModel)
	if cmd == nil {
		t.Fatal("openDataTabs queued nothing")
	}
	if m.phase != phaseData {
		t.Fatalf("phase = %v, want the data pane", m.phase)
	}
	if len(m.data) != 1 {
		t.Fatalf("tabs = %+v, want one", m.data)
	}
	// One pane per view, all of them reading.
	if got, want := len(m.data[0].panes), len(m.viewNames()); got != want {
		t.Fatalf("%d panes, want %d — one per view", got, want)
	}
	if !m.data[0].loading() {
		t.Error("the tab is not loading, so nothing was queued")
	}
	// The ids the views are written against come off the element row, not a
	// second lookup.
	if m.data[0].siteID == "" || m.data[0].cluster == "" {
		t.Errorf("tab = %+v, want the site and cluster ids filled in", m.data[0])
	}

	m = runDataLoads(t, m, cmd)
	if m.data[0].loading() {
		t.Fatal("still loading after every fetch answered")
	}

	// The whole page, which is longer than the pane: the window onto it is
	// checked below.
	page := plain(m.dataPage(m.data[0]).String())
	for _, want := range []string{
		"Overview", "Device", "ion 1200", "SPOKE", // the record
		"Controller", "Rebooted", "2026-10-01", // status, through the utc helper
		"HA", "Cluster", // gated on the element being clustered, which it is
		"Interfaces", "PORT", "STATE", "MAC", "CIRCUIT", // the per-row fetch
		"Circuits", "LABEL", "NETWORK", "ISP 1", // the circuits view
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}

	// The pane shows the top of it, says which element and how much there is.
	got := plain(m.viewData())
	// The window onto the page, not its length: the page grows whenever a
	// view does.
	for _, want := range []string{"store-0003-rtr-01", "Store 0003", "4 views", "Overview", "1-24 of"} {
		if !strings.Contains(got, want) {
			t.Errorf("the pane is missing %q:\n%s", want, got)
		}
	}
	for _, pane := range m.data[0].panes {
		if pane.err != "" {
			t.Errorf("view %s failed: %s", pane.view, pane.err)
		}
	}
}

// The MAC comes off the interface status, since the interface record carries
// the field and leaves it null on every real element.
func TestInterfacesShowTheMACFromTheStatus(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	port := tableRow(t, paneFor(t, m.data[0], "Interfaces"), "1")
	joined := strings.Join(port, " | ")
	mac := ""
	for _, cell := range port {
		if strings.Count(cell, ":") == 5 {
			mac = cell
		}
	}
	if mac == "" {
		t.Fatalf("no MAC on the port row: %s", joined)
	}
	if len(mac) != len("ec:b9:07:09:cb:e9") {
		t.Errorf("MAC = %q, want the shape a real status answers with", mac)
	}
}

// Which port carries which circuit, from both sides of the join.
func TestThePageTiesPortsToCircuits(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	// The port names the circuit it carries rather than printing the id out
	// of site_wan_interface_ids.
	port := tableRow(t, paneFor(t, m.data[0], "Interfaces"), "1")
	if !strings.Contains(strings.Join(port, " "), "ISP 1") {
		t.Errorf("port 1 does not name its circuit: %q", port)
	}

	circuit := tableRow(t, paneFor(t, m.data[0], "Circuits"), "ISP 1")
	joined := strings.Join(circuit, " | ")
	for _, want := range []string{
		"Circuit to ISP 1", // the label, looked up by label_id
		"Internet 1",       // the network, looked up by network_id
		"ctrl",             // and that this is the controller's circuit
		"up",               // the circuit's own status
		"operational",      // and this element's view of it
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the circuit row is missing %q: %s", want, joined)
		}
	}
	// The other direction of the same join: the circuit names its port.
	if !containsCell(circuit, "1") {
		t.Errorf("the circuit does not name the port carrying it: %s", joined)
	}
}

// alt+up and alt+down move a heading at a time, which is how a page this long
// gets read.
func TestAltUpDownJumpsBetweenHeadings(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.height = 20
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	page := m.dataPage(m.data[0])
	if len(page.heads) < len(m.viewNames()) {
		t.Fatalf("%d headings on the page, want at least one per view", len(page.heads))
	}
	rows := paneRows(m)

	// Down lands on the first heading past where the pane is held.
	nx, _ := m.onDataKey("alt+down")
	m = nx.(tuiModel)
	if got, want := m.data[0].scroll, clampScroll(page.heads[0], len(page.lines), rows); got != want {
		t.Fatalf("one alt+down put the pane at %d, want the first heading at %d", got, want)
	}
	nx, _ = m.onDataKey("alt+down")
	m = nx.(tuiModel)
	if got, want := m.data[0].scroll, clampScroll(page.heads[1], len(page.lines), rows); got != want {
		t.Fatalf("two alt+downs put the pane at %d, want the second heading at %d", got, want)
	}

	// And back up again.
	nx, _ = m.onDataKey("alt+up")
	m = nx.(tuiModel)
	if got, want := m.data[0].scroll, clampScroll(page.heads[0], len(page.lines), rows); got != want {
		t.Errorf("alt+up put the pane at %d, want back at %d", got, want)
	}

	// Past the last heading it holds rather than wrapping: a page that jumps
	// to the top when you press down again has lost your place.
	m.data[0].scroll = len(page.lines)
	nx, _ = m.onDataKey("alt+down")
	if got := nx.(tuiModel).data[0].scroll; got == 0 {
		t.Error("alt+down wrapped to the top from the end of the page")
	}
}

// A refresh refetches every view but keeps the place in the page: the point of
// refreshing is to watch one value change.
func TestRefreshKeepsTheScroll(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	m.data[0].scroll = 12
	next, cmd = m.onDataKey("r")
	m = next.(tuiModel)
	if !m.data[0].loading() {
		t.Fatal("r did not start a refetch")
	}
	if got := m.data[0].scroll; got != 12 {
		t.Errorf("scroll = %d after r, want it kept at 12", got)
	}
	m = runDataLoads(t, m, cmd)
	if got := m.data[0].scroll; got != 12 {
		t.Errorf("scroll = %d once the refetch landed, want it kept", got)
	}
}

// The point of the format: a view nobody shipped, from a file of the user's
// own, on the same page as the built-ins.
func TestAUserViewFromTheViewsFlagIsOffered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "views.yaml")
	body := `
views:
  - name: Ports only
    fetch:
      ifs: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces
    sections:
      - title: Just the ports
        for_each: ifs.items
        columns:
          - header: NAME
            value: "{{ .item.name }}"
          - header: MTU
            value: "{{ .item.mtu }}"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	Views = path
	t.Cleanup(func() { Views = "" })

	m, set := fixtureTUIWith(t, haFixtureYAML)
	names := m.viewNames()
	if len(names) < 2 || names[len(names)-1] != "Ports only" {
		t.Fatalf("views = %v, want the user's appended to the built-ins", names)
	}

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	page := plain(m.dataPage(m.data[0]).String())
	for _, want := range []string{"Overview", "Ports only", "Just the ports", "NAME", "MTU", "1500"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}
}

// Opening the same element twice reuses its tab rather than stacking a second
// page of identical data.
func TestReopeningAnElementReusesItsTab(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	target := []sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}}

	next, cmd := m.openDataTabs(target)
	m = runDataLoads(t, next.(tuiModel), cmd)
	m.data[0].scroll = 7

	next, cmd = m.openDataTabs(target)
	m = next.(tuiModel)
	if len(m.data) != 1 {
		t.Fatalf("%d tabs after reopening the same element, want 1", len(m.data))
	}
	if got := m.data[0].scroll; got != 7 {
		t.Errorf("scroll = %d after reopening, want the place it was at", got)
	}
	if cmd == nil {
		t.Error("reopening queued no fetch, so the page would keep stale data")
	}

	// A second element gets a tab of its own.
	other := set.Tenants[0].Elements[1]
	next, _ = m.openDataTabs([]sessionTarget{{eid: other.ID, name: other.Name, tsg: m.tenant.TSGID}})
	if got := len(next.(tuiModel).data); got != 2 {
		t.Errorf("%d tabs for two elements, want 2", got)
	}
}

// A data pane joins the tab ring after the shells, so alt+←/→ walks all of
// them and the session numbers do not move as data tabs come and go.
func TestDataTabsJoinTheTabRing(t *testing.T) {
	m := pickModel(t)
	m.data = []dataTab{{eid: "1", name: "S0100-RTR-01", tsg: "1"}}
	m.dataActive = 0
	m.phase = phaseData

	if got := m.tabCount(); got != 2 {
		t.Fatalf("tabCount() = %d, want the list plus the pane", got)
	}
	if got := m.currentTab(); got != 1 {
		t.Errorf("currentTab() = %d, want 1", got)
	}

	// alt+left from the pane lands on the element list.
	next, _ := m.onDataKey("alt+left")
	if got := next.(tuiModel).phase; got != phasePicking {
		t.Errorf("phase after alt+left = %v, want the list", got)
	}

	// alt+k closes it; a pane has no connection to detach, so alt+w does too.
	closed := m.closeDataTab()
	if len(closed.data) != 0 || closed.phase != phasePicking {
		t.Errorf("after close: %d tabs, phase %v", len(closed.data), closed.phase)
	}

	bar := plain(m.tabBar())
	for _, want := range []string{"elements", "S0100-RTR-01 · data"} {
		if !strings.Contains(bar, want) {
			t.Errorf("tab bar missing %q: %q", want, bar)
		}
	}
}

// Rich-data mode reads the portal, so it must not ask for a device password
// the way shell and batch do.
func TestRichDataModeDoesNotAskForADeviceLogin(t *testing.T) {
	m := pickModel(t)
	m.mode = modeData
	// No stored device password for this tenant: shell mode would open the
	// password form here.
	if err := m.store.Secrets.Delete(config.IONPassKey("1", "u")); err != nil {
		t.Fatal(err)
	}

	next, _ := m.onPickTargets([]sessionTarget{{eid: "1", name: "S0100-RTR-01", tsg: "1"}})
	if got := next.(tuiModel).phase; got == phasePassword {
		t.Fatal("rich-data mode asked for an ION password")
	}
}

func TestScrollingAPaneStaysInsideIt(t *testing.T) {
	m := pickModel(t)
	m.height = 12
	m.data = []dataTab{{eid: "1", name: "e", tsg: "1"}}
	m.dataActive = 0
	m.phase = phaseData

	// Nothing loaded yet, so there is nothing to scroll and end must not run
	// the offset off into blank space.
	next, _ := m.onDataKey("end")
	if got := next.(tuiModel).data[0].scroll; got != 0 {
		t.Errorf("scroll = %d with nothing loaded, want 0", got)
	}
	next, _ = next.(tuiModel).onDataKey("up")
	if got := next.(tuiModel).data[0].scroll; got != 0 {
		t.Errorf("scroll = %d after up at the top, want 0", got)
	}
}

func paneRows(m tuiModel) int {
	_, rows := m.dataPaneSize()
	return rows
}

func paneFor(t *testing.T, tab dataTab, view string) dataPane {
	t.Helper()
	for _, p := range tab.panes {
		if p.view != view {
			continue
		}
		if p.err != "" {
			t.Fatalf("view %s failed: %s", view, p.err)
		}
		return p
	}
	t.Fatalf("no pane for view %q", view)
	return dataPane{}
}

// tableRow is the row of a pane's tables whose first cell is want.
func tableRow(t *testing.T, p dataPane, want string) []string {
	t.Helper()
	for _, sec := range p.rendered.Sections {
		for _, row := range sec.Table {
			if len(row.Cells) > 0 && row.Cells[0] == want {
				return row.Cells
			}
		}
	}
	t.Fatalf("no row starting %q in view %s", want, p.view)
	return nil
}

func containsCell(cells []string, want string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) == want {
			return true
		}
	}
	return false
}

// runDataLoads runs every fetch the tab queued and feeds each answer back in,
// the way the runtime would as they arrive.
func runDataLoads(t *testing.T, m tuiModel, cmd tea.Cmd) tuiModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	msgs := []tea.Msg{cmd()}
	if batch, ok := msgs[0].(tea.BatchMsg); ok {
		msgs = nil
		for _, c := range batch {
			msgs = append(msgs, c())
		}
	}
	got := 0
	for _, msg := range msgs {
		loaded, ok := msg.(dataLoadedMsg)
		if !ok {
			continue
		}
		got++
		next, _ := m.Update(loaded)
		m = next.(tuiModel)
	}
	if got == 0 {
		t.Fatalf("none of the %d commands produced data", len(msgs))
	}
	return m
}

// alt+up and alt+down jump through the element list by a configurable step,
// which is what makes a list of thousands navigable without the filter.
func TestAltUpDownJumpsTheElementList(t *testing.T) {
	m := pickModel(t)
	m.cfg.PageStep = 2
	m.table.SetCursor(0)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModAlt}))
	m = next.(tuiModel)
	if got := m.table.Cursor(); got != 2 {
		t.Errorf("cursor = %d after alt+down with a step of 2, want 2", got)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModAlt}))
	if got := next.(tuiModel).table.Cursor(); got != 0 {
		t.Errorf("cursor = %d after alt+up, want back at 0", got)
	}

	// Nothing saved means the default, not a step of zero.
	m.cfg.PageStep = 0
	if got := m.pageStep(); got != DefaultPageStep {
		t.Errorf("pageStep() = %d with nothing saved, want %d", got, DefaultPageStep)
	}
}

// The setting cycles and sticks, since the right step depends on the fleet.
func TestThePageStepSettingCycles(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.setCursor = jumpRow

	first := m.pageStep()
	next, _ := m.onGeneralKey("enter")
	m = next.(tuiModel)
	if m.pageStep() == first {
		t.Fatalf("pageStep() = %d after enter, want it moved on", m.pageStep())
	}

	// Saved, so the next run opens on it.
	cfg, err := m.store.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PageStep != m.pageStep() {
		t.Errorf("saved step = %d, in-memory %d", cfg.PageStep, m.pageStep())
	}

	// Round the rest of the cycle and back to where it started.
	for i := 1; i < len(pageSteps); i++ {
		nx, _ := m.onGeneralKey("enter")
		m = nx.(tuiModel)
	}
	if got := m.pageStep(); got != first {
		t.Errorf("pageStep() = %d after a full cycle, want %d", got, first)
	}
}

// The security zone a port is in, joined through two fetches: the element's
// bindings hold the interface ids, and the tenant's zone list has the names.
// The join is the same one the circuit column uses.
func TestInterfacesShowTheSecurityZone(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	pane := paneFor(t, m.data[0], "Interfaces")
	if pane.err != "" {
		t.Fatalf("the Interfaces view failed: %s", pane.err)
	}
	// The zone fetches must not have failed either: a broken one shows as a
	// warn line rather than taking the view down.
	for _, e := range pane.rendered.Errs {
		t.Errorf("a fetch failed: %s", e)
	}

	var headers []string
	for _, sec := range pane.rendered.Sections {
		if len(sec.Headers) > 0 {
			headers = sec.Headers
		}
	}
	var zoneAt = -1
	for i, h := range headers {
		if h == "ZONE" {
			zoneAt = i
		}
	}
	if zoneAt < 0 {
		t.Fatalf("no ZONE column in %v", headers)
	}

	// The zone hangs off the vlan interface, which is where a real element
	// carries it: in interface.har every vlan row has a zone and every port,
	// switch_port and bypasspair row has none.
	vlan := tableRow(t, pane, "vlan1")
	if got := vlan[zoneAt]; got != "Store - LAN" {
		t.Errorf("vlan1 zone = %q, want the zone name", got)
	}
	for _, port := range []string{"1", "3"} {
		if got := tableRow(t, pane, port)[zoneAt]; got != "" {
			t.Errorf("port %s zone = %q, want nothing: zones are on the vlans", port, got)
		}
	}
}

// The data page is long -- four views, a 27-row interface table on a real
// element -- so `/` narrows it to the lines that match, keeping each line's
// view heading and its table header so a row is still readable.
func TestTheDataPageCanBeFiltered(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase, m.mode = phasePicking, modeData
	m.width, m.height = 120, 30
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	whole := len(m.dataPage(m.data[0]).lines)
	if whole < 20 {
		t.Fatalf("the page is %d lines, want a long one to filter", whole)
	}

	// / opens it and typing narrows the page.
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	if !m.dataFilterOn {
		t.Fatal("/ did not open the data filter")
	}
	for _, r := range "vlan1" {
		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = mm.(tuiModel)
	}
	if got := m.dataFilter.Value(); got != "vlan1" {
		t.Fatalf("query = %q, want the typing to reach the input", got)
	}

	page := m.visiblePage(m.data[0])
	if len(page.lines) >= whole {
		t.Errorf("%d lines of %d after filtering, want fewer", len(page.lines), whole)
	}
	text := plain(page.String())
	if !strings.Contains(text, "vlan1") {
		t.Errorf("the filtered page lost its match:\n%s", text)
	}
	// The view heading and the table's header come with it, or the row is a
	// line of unlabelled columns.
	for _, want := range []string{"Interfaces", "PORT"} {
		if !strings.Contains(text, want) {
			t.Errorf("the filtered page is missing %q:\n%s", want, text)
		}
	}
	// And nothing from the other views is left.
	if strings.Contains(text, "Rebooted") {
		t.Errorf("the filtered page kept lines that do not match:\n%s", text)
	}

	// The pane says how much matched.
	if got := plain(m.viewData()); !strings.Contains(got, "of") || !strings.Contains(got, "lines") {
		t.Errorf("the pane does not say how much matched:\n%s", got)
	}

	// Backspacing to nothing keeps the filter open; one more closes it.
	for range "vlan1" {
		mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
		m = mm.(tuiModel)
	}
	if !m.dataFilterOn {
		t.Error("the filter closed on the press that emptied the query")
	}
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if mm.(tuiModel).dataFilterOn {
		t.Error("the filter is still open after backspacing the empty query")
	}
	// The whole page is back.
	if got := len(m.visiblePage(m.data[0]).lines); got != whole {
		t.Errorf("%d lines with an empty query, want the whole page of %d", got, whole)
	}
}
