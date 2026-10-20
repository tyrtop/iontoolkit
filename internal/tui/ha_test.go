package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/sase/api"
)

func TestHAStatesAreFilledIntoTheList(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	if !m.showHAColumn() {
		t.Fatal("the HA column is hidden on a list with a cluster in it")
	}
	if got := plain(m.viewPicking()); !strings.Contains(got, "HA") {
		t.Errorf("the list has no ha column:\n%s", got)
	}
	first := set.Tenants[0].Elements[0]
	if got := plain(m.haCell(rowFor(t, m, first.ID))); got != "loading" {
		t.Errorf("cell before the fetch = %q, want it to say it is loading", got)
	}

	w := haClusters(elementsOf(m))
	if w.clusters() != 1 {
		t.Fatalf("%d clusters to read for a pair sharing one, want 1", w.clusters())
	}

	cmd := loadHA(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, w.pairs)
	if cmd == nil {
		t.Fatal("nothing queued for a tenant with a cluster")
	}
	msg, ok := cmd().(haStatesMsg)
	if !ok {
		t.Fatalf("message = %T, want the HA states", msg)
	}
	next, _ := m.Update(msg)
	m = next.(tuiModel)

	active := plain(m.haCell(rowFor(t, m, first.ID)))
	peer := plain(m.haCell(rowFor(t, m, set.Tenants[0].Elements[1].ID)))
	if active != "active" {
		t.Errorf("the connected member reads %q, want active", active)
	}
	if peer != "standby" {
		t.Errorf("the other member reads %q, want standby derived from the active one", peer)
	}
	if got := plain(m.viewPicking()); !strings.Contains(got, "active") {
		t.Errorf("the list does not show the state:\n%s", got)
	}
}

func TestTheHAColumnStaysOffAListWithoutClusters(t *testing.T) {
	m, _ := fixtureTUI(t)
	m.phase = phasePicking
	m.rebuildRows()

	if m.showHAColumn() {
		t.Error("the HA column is on a list with no clusters in it")
	}
	if cmd := loadHA(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, nil); cmd != nil {
		t.Error("something was queued for a tenant with no clusters")
	}
}

func TestHAClustersAreCountedOncePerCluster(t *testing.T) {
	els := []api.Element{
		{ID: "1", SiteID: "s1", SpokeHA: &api.SpokeHA{ClusterID: "c1"}},
		{ID: "2", SiteID: "s1", SpokeHA: &api.SpokeHA{ClusterID: "c1"}},
		{ID: "3", SiteID: "s2", SpokeHA: &api.SpokeHA{ClusterID: "c2"}},
		{ID: "4", SiteID: "s3"},
		{ID: "5", SpokeHA: &api.SpokeHA{ClusterID: "c3"}},
	}
	got := haClusters(els)
	if len(got.pairs) != 2 {
		t.Errorf("pairs = %v, want one per cluster that can be read one at a time", got.pairs)
	}
	if len(got.noSite) != 1 {
		t.Errorf("noSite = %v, want the cluster with no site in it", got.noSite)
	}
	if len(got.elements) != 4 {
		t.Errorf("elements = %v, want every clustered element for the aggregate", got.elements)
	}
}

func rowFor(t *testing.T, m tuiModel, eid string) elementRow {
	t.Helper()
	for _, r := range m.all {
		if r.ID == eid {
			return r
		}
	}
	t.Fatalf("no row for %s", eid)
	return elementRow{}
}

func elementsOf(m tuiModel) []api.Element {
	out := make([]api.Element, 0, len(m.all))
	for _, r := range m.all {
		out = append(out, r.Element)
	}
	return out
}

func manyClusterFixture(sites int) string {
	var b strings.Builder
	b.WriteString("tenants:\n  - name: Acme Co\n    tsg_id: \"000000000\"\n    users:\n      - name: admin\n        password: ion\n    elements:\n")
	for i := 1; i <= sites; i++ {
		fmt.Fprintf(&b, "      - name: store-%04d-rtr-01\n        model: ion 1200\n        site: Store %04d\n        connected: true\n        ha: true\n", i, i)
		fmt.Fprintf(&b, "      - name: store-%04d-rtr-02\n        model: ion 1200\n        site: Store %04d\n        connected: true\n        ha: true\n", i, i)
	}
	return b.String()
}

func TestEveryClusterResolvesNotJustTheFirstBatch(t *testing.T) {
	sites := haBatch + 2
	m, _ := fixtureTUIWith(t, manyClusterFixture(sites))
	m.phase = phasePicking
	m.rebuildRows()

	pairs := haClusters(elementsOf(m)).pairs
	if len(pairs) != sites {
		t.Fatalf("%d clusters, want one per site (%d)", len(pairs), sites)
	}

	cmd := loadHA(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, pairs)
	passes := 0
	for {
		passes++
		if passes > 10 {
			t.Fatal("the sweep did not finish")
		}
		msg, ok := cmd().(haStatesMsg)
		if !ok {
			t.Fatalf("message = %T, want the HA states", msg)
		}
		next, c := m.Update(msg)
		m = next.(tuiModel)
		if len(msg.rest) == 0 {
			break
		}
		if c == nil {
			t.Fatalf("the chain stopped with %d clusters left", len(msg.rest))
		}
		cmd = c
	}
	if passes < 2 {
		t.Fatalf("%d pass for %d clusters, want the batches to chain", passes, sites)
	}

	for _, r := range m.all {
		got := plain(m.haCell(r))
		if got != "active" && got != "standby" {
			t.Errorf("%s reads %q after the whole sweep, want a state", r.Name, got)
		}
	}
}

func TestARefusingClusterIsReportedNotSwallowed(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	next, _ := m.Update(haStatesMsg{
		tsg:    m.tenant.TSGID,
		failed: map[string]string{first.ClusterID(): "403 Forbidden"},
	})
	m = next.(tuiModel)

	if got := plain(m.haCell(first)); got != "ha?" {
		t.Errorf("cell = %q for a cluster that refused, want it to stop looking like it is loading", got)
	}
	if !strings.Contains(m.status, "403 Forbidden") || !strings.Contains(m.status, "1 cluster") {
		t.Errorf("status = %q, want the count and the reason", m.status)
	}
}

func TestAnAnsweredClusterThatNamesNobodyReadsUnknown(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	next, _ := m.Update(haStatesMsg{
		tsg:      m.tenant.TSGID,
		answered: []string{first.ClusterID()},
	})
	m = next.(tuiModel)

	if got := plain(m.haCell(first)); got != "unknown" {
		t.Errorf("cell = %q, want the API's word for a member it did not name", got)
	}
	if m.status != "" {
		t.Errorf("status = %q; a cluster that answered is not a failure", m.status)
	}
}

func TestAnElementWithNoSiteSaysItCannotBeAsked(t *testing.T) {
	m, _ := fixtureTUI(t)
	r := elementRow{Element: api.Element{
		ID:      "9",
		Name:    "no-site-rtr-01",
		SpokeHA: &api.SpokeHA{ClusterID: "c9"},
	}, TSG: m.tenant.TSGID}

	if got := plain(m.haCell(r)); got != "ha?" {
		t.Errorf("cell = %q for an element with no site, want it to admit it cannot be read", got)
	}
}

func TestTheAggregateQueryFillsTheWholeColumnAtOnce(t *testing.T) {
	sites := haBatch + 2
	m, _ := fixtureTUIWith(t, manyClusterFixture(sites))
	m.phase = phasePicking
	m.rebuildRows()

	w := haClusters(elementsOf(m))
	m.haTotal = w.clusters()
	cmd := loadHABulk(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, w)
	if cmd == nil {
		t.Fatal("nothing queued for a tenant with clusters")
	}
	msg, ok := cmd().(haStatesMsg)
	if !ok {
		t.Fatalf("message = %T, want the HA states", msg)
	}
	if msg.bulk != "used" {
		t.Fatalf("bulk = %q, want the aggregate to have answered", msg.bulk)
	}
	if len(msg.rest) != 0 {
		t.Errorf("%d clusters left over, want the one call to have covered them", len(msg.rest))
	}

	next, _ := m.Update(msg)
	m = next.(tuiModel)
	for _, r := range m.all {
		if got := plain(m.haCell(r)); got != "active" && got != "standby" {
			t.Errorf("%s reads %q after the aggregate, want a state", r.Name, got)
		}
	}
	if got := m.haPath(); !strings.Contains(got, "one tenant-wide query") {
		t.Errorf("haPath() = %q, want it to say which path filled the column", got)
	}
}

func TestARefusedAggregateFallsBackToThePerClusterSweep(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	next, cmd := m.Update(haStatesMsg{
		tsg:  m.tenant.TSGID,
		bulk: "status 403 Forbidden",
		rest: haClusters(elementsOf(m)).pairs,
	})
	m = next.(tuiModel)
	if cmd == nil {
		t.Fatal("the refusal queued nothing, so the column would never fill")
	}
	if got := m.haPath(); !strings.Contains(got, "one at a time") || !strings.Contains(got, "403") {
		t.Errorf("haPath() = %q, want it to say the query was refused and what took over", got)
	}

	msg, ok := cmd().(haStatesMsg)
	if !ok {
		t.Fatalf("message = %T, want the per-cluster states", msg)
	}
	next, _ = m.Update(msg)
	m = next.(tuiModel)
	if got := plain(m.haCell(rowFor(t, m, set.Tenants[0].Elements[0].ID))); got != "active" {
		t.Errorf("cell = %q after the fallback sweep, want the state", got)
	}
}

func TestCachedHAIsShownWithAnAsteriskUntilItIsRefreshed(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	m.haCached = map[string]string{first.key(): haActive}

	if got := plain(m.haCell(first)); got != "active*" {
		t.Errorf("cell = %q, want the cached state marked", got)
	}

	next, _ := m.Update(haStatesMsg{
		tsg:      m.tenant.TSGID,
		states:   map[string]string{first.key(): haStandby},
		answered: []string{first.ClusterID()},
	})
	m = next.(tuiModel)
	if got := plain(m.haCell(first)); got != "standby" {
		t.Errorf("cell = %q after a fresh sweep, want it unmarked", got)
	}
}

func TestASweepIsSavedForTheNextLaunch(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	tsg := m.tenant.TSGID
	eid := set.Tenants[0].Elements[0].ID

	cmd := saveHA(m.store, tsg, map[string]string{tsg + "/" + eid: haActive})
	if cmd == nil {
		t.Fatal("nothing was queued to save the sweep")
	}
	cmd()

	c, err := m.store.Cache(tsg)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("no cache file for the tenant")
	}
	if got := c.HA[eid]; got != haActive {
		t.Errorf("cached HA for %s = %q, want %q", eid, got, haActive)
	}
	if len(c.Elements) == 0 {
		t.Error("saving the sweep dropped the cached elements")
	}
}

func TestTheAggregateAsksAboutTheVisibleRowsFirst(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(20))
	m.phase = phasePicking
	m.width, m.height = 120, 24
	m.rebuildRows()

	w := haClusters(elementsOf(m))
	if len(w.elements) < 20 {
		t.Fatalf("%d clustered elements, want the fleet", len(w.elements))
	}

	ordered := m.visibleFirst(m.tenant.TSGID, w.elements)
	top := m.all[m.matches[0]].ID
	if ordered[0] != top {
		t.Errorf("first id asked about = %s, want the row under the cursor (%s)", ordered[0], top)
	}

	m.moveCursorTo(len(m.matches) - 1)
	ordered = m.visibleFirst(m.tenant.TSGID, w.elements)
	bottom := m.all[m.matches[len(m.matches)-1]].ID
	if ordered[0] != bottom {
		t.Errorf("first id asked about = %s, want the row the cursor moved to (%s)", ordered[0], bottom)
	}

	m.filter.SetValue("store-0007")
	m.applyFilter()
	if len(m.matches) == 0 || len(m.matches) == len(m.all) {
		t.Fatalf("%d matches, want the query to have narrowed the list", len(m.matches))
	}
	wanted := map[string]bool{}
	for _, i := range m.matches {
		wanted[m.all[i].ID] = true
	}
	ordered = m.visibleFirst(m.tenant.TSGID, w.elements)
	for i := range len(wanted) {
		if !wanted[ordered[i]] {
			t.Errorf("id %d asked about (%s) is not one of the searched rows", i, ordered[i])
		}
	}
}

func TestASearchAsksForItsRowsWithoutWaitingForTheFill(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(20))
	m.phase = phasePicking
	m.width, m.height = 120, 24
	m.rebuildRows()

	w := haClusters(elementsOf(m))
	next, cmd := m.Update(haStatesMsg{
		tsg:          m.tenant.TSGID,
		bulk:         "used",
		states:       map[string]string{},
		restElements: w.elements,
	})
	m = next.(tuiModel)
	if cmd == nil {
		t.Fatal("the fill queued nothing to continue with")
	}
	if len(m.haQueue[m.tenant.TSGID]) == 0 {
		t.Fatal("the rest of the fleet was not queued")
	}

	m.filter.SetValue("store-0012")
	m.applyFilter()
	if len(m.matches) == 0 {
		t.Fatal("the query matched nothing")
	}
	wanted := m.haWanted()[m.tenant.TSGID]
	if len(wanted) == 0 {
		t.Fatal("nothing was wanted for the searched rows")
	}
	for _, id := range wanted {
		var found bool
		for _, i := range m.matches {
			if m.all[i].ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is wanted but is not one of the searched rows", id)
		}
	}

	ask := m.askVisibleHA()
	if ask == nil {
		t.Fatal("the search did not ask for its rows")
	}
	if again := m.askVisibleHA(); again != nil {
		t.Error("a second ask went out while the first was still in flight")
	}

	msg, ok := ask().(haStatesMsg)
	if !ok {
		t.Fatalf("message = %T, want the HA states", msg)
	}
	next, _ = m.Update(msg)
	m = next.(tuiModel)
	for _, i := range m.matches {
		r := m.all[i]
		if r.ClusterID() == "" {
			continue
		}
		if got := plain(m.haCell(r)); got == "loading" {
			t.Errorf("%s still reads loading after the search asked for it", r.Name)
		}
	}
}

func TestEachChunkIsCachedAsItLands(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	tsg := m.tenant.TSGID
	eid := set.Tenants[0].Elements[0].ID

	_, cmd := m.Update(haStatesMsg{
		tsg:          tsg,
		bulk:         "used",
		states:       map[string]string{tsg + "/" + eid: haActive},
		restElements: []string{"something-else"},
	})
	if cmd == nil {
		t.Fatal("nothing came back from a chunk with more to do")
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			if c != nil {
				c()
			}
		}
	}

	c, err := m.store.Cache(tsg)
	if err != nil || c == nil {
		t.Fatalf("cache = %v, %v", c, err)
	}
	if got := c.HA[eid]; got != haActive {
		t.Errorf("cached HA = %q after one chunk, want it saved as it landed", got)
	}
}
