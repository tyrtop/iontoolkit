package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/sase/api"
)

// The portal has no "standby": a cluster reads "active" on the member
// carrying traffic and "unknown" on anything the controller cannot see. The
// second node is therefore derived, and only where there is an active member
// to derive it from.
func TestHAStatesAreFilledIntoTheList(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	// Both fixture elements are in the site's cluster, so the column shows.
	if !m.showHAColumn() {
		t.Fatal("the HA column is hidden on a list with a cluster in it")
	}
	if got := plain(m.viewPicking()); !strings.Contains(got, "ha") {
		t.Errorf("the list has no ha column:\n%s", got)
	}
	// Before the status lands, a clustered element says the state is coming
	// rather than naming one.
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

	// The fixture's connected element is the active one; its peer is not
	// connected, so the cluster reports it as unknown -- and with an active
	// member in the cluster that is the standby.
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

// A list with no clusters pays nothing for the column.
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

// One request per cluster, not per element: a pair shares a cluster.
func TestHAClustersAreCountedOncePerCluster(t *testing.T) {
	els := []api.Element{
		{ID: "1", SiteID: "s1", SpokeHA: &api.SpokeHA{ClusterID: "c1"}},
		{ID: "2", SiteID: "s1", SpokeHA: &api.SpokeHA{ClusterID: "c1"}},
		{ID: "3", SiteID: "s2", SpokeHA: &api.SpokeHA{ClusterID: "c2"}},
		{ID: "4", SiteID: "s3"},
		// In a cluster but with no site, so there is no path to ask about.
		{ID: "5", SpokeHA: &api.SpokeHA{ClusterID: "c3"}},
	}
	got := haClusters(els)
	if len(got.pairs) != 2 {
		t.Errorf("pairs = %v, want one per cluster that can be read one at a time", got.pairs)
	}
	// The one with no site can only ever be read by the aggregate, which asks
	// by element, so it is kept apart rather than dropped.
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

// manyClusterFixture is a fleet of HA pairs, one cluster per site, with more
// clusters than a single pass reads.
func manyClusterFixture(sites int) string {
	var b strings.Builder
	b.WriteString("tenants:\n  - name: Acme Co\n    tsg_id: \"000000000\"\n    users:\n      - name: admin\n        password: ion\n    elements:\n")
	for i := 1; i <= sites; i++ {
		fmt.Fprintf(&b, "      - name: store-%04d-rtr-01\n        model: ion 1200\n        site: Store %04d\n        connected: true\n        ha: true\n", i, i)
		fmt.Fprintf(&b, "      - name: store-%04d-rtr-02\n        model: ion 1200\n        site: Store %04d\n        connected: true\n        ha: true\n", i, i)
	}
	return b.String()
}

// Every cluster resolves, however many there are. This is the bug that put
// "ha" on most of a fleet: the sweep read the first 64 clusters and dropped
// the rest, and the status line's "alt+r for another pass" rebuilt the same
// list and read the same 64 again.
func TestEveryClusterResolvesNotJustTheFirstBatch(t *testing.T) {
	sites := haBatch + 2
	m, _ := fixtureTUIWith(t, manyClusterFixture(sites))
	m.phase = phasePicking
	m.rebuildRows()

	pairs := haClusters(elementsOf(m)).pairs
	if len(pairs) != sites {
		t.Fatalf("%d clusters, want one per site (%d)", len(pairs), sites)
	}

	// Follow the passes the way the program does: each message carries what
	// it did not reach, and the next command takes it.
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
			// The last pass hands back the cache write, not another read.
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

// A cluster that refuses is why a row says nothing, so the row says that it
// could not be told rather than that it is still loading, and the line says
// how many and why.
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

// A cluster that answered without naming a member means the controller cannot
// see it, which is the API's own word rather than something still in flight.
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

// An element whose record carries no site id can never be asked about: the
// status path is per site. It used to show "ha" for good.
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

// One POST for the whole tenant instead of one GET per cluster. This is what
// the portal's own resource map calls query_status_spokeclusters, and on a
// fleet of pairs it is the difference between a column that is there with the
// list and one that fills at five requests a second.
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

// A tenant whose aggregate is refused has to fall back, not come up empty:
// the per-cluster sweep is the only path proved against hardware.
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

	// And the sweep it queued does fill the column.
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

// Without an aggregate the sweep is slow, so the last one is kept and shown
// with an asterisk rather than leaving the column empty on every launch.
func TestCachedHAIsShownWithAnAsteriskUntilItIsRefreshed(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	m.haCached = map[string]string{first.key(): haActive}

	if got := plain(m.haCell(first)); got != "active*" {
		t.Errorf("cell = %q, want the cached state marked", got)
	}

	// This run's own answer replaces it, asterisk and all.
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

// The sweep is written to the element cache, which is what the next launch
// reads: a value saved per element id, keyed off the tenant's file.
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
	// The elements it was attached to are still there.
	if len(c.Elements) == 0 {
		t.Error("saving the sweep dropped the cached elements")
	}
}

// The portal's trick, which is the whole reason its page is quick: ask about
// the rows you are showing, not the fleet you have. The first request covers
// what is on screen, and a search reprioritises what is asked next -- the
// matched rows are the only ones ranked at all.
func TestTheAggregateAsksAboutTheVisibleRowsFirst(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(20))
	m.phase = phasePicking
	m.width, m.height = 120, 24
	m.rebuildRows()

	w := haClusters(elementsOf(m))
	if len(w.elements) < 20 {
		t.Fatalf("%d clustered elements, want the fleet", len(w.elements))
	}

	// With the cursor at the top, the first ids asked about are the top rows.
	ordered := m.visibleFirst(m.tenant.TSGID, w.elements)
	top := m.all[m.matches[0]].ID
	if ordered[0] != top {
		t.Errorf("first id asked about = %s, want the row under the cursor (%s)", ordered[0], top)
	}

	// Move down the list and the order follows the cursor.
	m.moveCursorTo(len(m.matches) - 1)
	ordered = m.visibleFirst(m.tenant.TSGID, w.elements)
	bottom := m.all[m.matches[len(m.matches)-1]].ID
	if ordered[0] != bottom {
		t.Errorf("first id asked about = %s, want the row the cursor moved to (%s)", ordered[0], bottom)
	}

	// A search narrows it: the matched rows are asked about first, and
	// everything else goes behind them.
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

// The background fill is paced on purpose, and a search does not wait for it:
// the rows a query brings on screen are asked about immediately, which is only
// possible because the fill leaves the rate budget alone between chunks.
func TestASearchAsksForItsRowsWithoutWaitingForTheFill(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(20))
	m.phase = phasePicking
	m.width, m.height = 120, 24
	m.rebuildRows()

	// A chunk lands with the fleet still queued, which is the paced state.
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

	// Searching asks for what it put on screen, straight away.
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
	// One request per tenant at a time: a second ask while that one is out
	// must not duplicate it.
	if again := m.askVisibleHA(); again != nil {
		t.Error("a second ask went out while the first was still in flight")
	}

	// And the answer fills exactly those rows.
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

// Each chunk is written to the cache as it lands, not only at the end: a fill
// that is paced across a fleet would otherwise save nothing until it finished.
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
	// The batch carries the next tick and the cache write; run them.
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
