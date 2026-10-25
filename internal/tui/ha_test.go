package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
)

func TestHAStatesAreFilledIntoTheList(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	if !(m.runtime().ListWhen()["clustered"] == true) {
		t.Fatal("the HA column is hidden on a list with a cluster in it")
	}
	if got := plain(m.viewPicking()); !strings.Contains(got, "HA") {
		t.Errorf("the list has no ha column:\n%s", got)
	}
	first := set.Tenants[0].Elements[0]
	if got := plain(m.haCell(rowFor(t, m, first.ID))); got != "loading" {
		t.Errorf("cell before the fetch = %q, want it to say it is loading", got)
	}

	w := runtime.HAClusters(elementsOf(m))
	if w.Clusters() != 1 {
		t.Fatalf("%d clusters to read for a pair sharing one, want 1", w.Clusters())
	}

	m.runtime().SweepHA(t.Context(), m.tenant().TSGID, w, 0)
	m = settle(t, m)

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

	if m.runtime().ListWhen()["clustered"] == true {
		t.Error("the HA column is on a list with no clusters in it")
	}
	if w := runtime.HAClusters(elementsOf(m)); w.Clusters() != 0 {
		t.Errorf("%d clusters to read on a list with none", w.Clusters())
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
	got := runtime.HAClusters(els)
	if len(got.Pairs) != 2 {
		t.Errorf("pairs = %v, want one per cluster that can be read one at a time", got.Pairs)
	}
	if len(got.NoSite) != 1 {
		t.Errorf("noSite = %v, want the cluster with no site in it", got.NoSite)
	}
	if len(got.Elements) != 4 {
		t.Errorf("elements = %v, want every clustered element for the aggregate", got.Elements)
	}
}

func rowFor(t *testing.T, m tuiModel, eid string) elementRow {
	t.Helper()
	for _, r := range m.rows() {
		if r.ID == eid {
			return r
		}
	}
	t.Fatalf("no row for %s", eid)
	return elementRow{}
}

func elementsOf(m tuiModel) []api.Element {
	out := make([]api.Element, 0, len(m.rows()))
	for _, r := range m.rows() {
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

	pairs := runtime.HAClusters(elementsOf(m)).Pairs
	if len(pairs) != sites {
		t.Fatalf("%d clusters, want one per site (%d)", len(pairs), sites)
	}

	rt, tsg := m.runtime(), m.tenant().TSGID
	res := rt.FetchHAPairs(t.Context(), tsg, pairs)
	passes := 1
	for next := rt.ApplyHA(res); len(next.Pairs) > 0; next = rt.ApplyHA(res) {
		if passes++; passes > 10 {
			t.Fatal("the sweep did not finish")
		}
		res = rt.FetchHAPairs(t.Context(), tsg, next.Pairs)
	}
	m = settle(t, m)
	if passes < 2 {
		t.Fatalf("%d pass for %d clusters, want the batches to chain", passes, sites)
	}

	for _, r := range m.rows() {
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
	m.runtime().ApplyHA(runtime.HAResult{
		TSG:    m.tenant().TSGID,
		Failed: map[string]string{first.ClusterID(): "403 Forbidden"},
	})
	m = settle(t, m)

	if got := plain(m.haCell(first)); got != "ha?" {
		t.Errorf("cell = %q for a cluster that refused, want it to stop looking like it is loading", got)
	}
	if h := m.runtime().HA(); h.Failed != 1 || h.Err != "403 Forbidden" {
		t.Errorf("HA = %+v, want the count and the reason kept", h)
	}
}

func TestAnAnsweredClusterThatNamesNobodyReadsUnknown(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	m.runtime().ApplyHA(runtime.HAResult{TSG: m.tenant().TSGID, Answered: []string{first.ClusterID()}})
	m = settle(t, m)

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
	}, TSG: m.tenant().TSGID}

	if got := plain(m.haCell(r)); got != "ha?" {
		t.Errorf("cell = %q for an element with no site, want it to admit it cannot be read", got)
	}
}

func TestTheAggregateQueryFillsTheWholeColumnAtOnce(t *testing.T) {
	sites := haBatch + 2
	m, _ := fixtureTUIWith(t, manyClusterFixture(sites))
	m.phase = phasePicking
	m.rebuildRows()

	w := runtime.HAClusters(elementsOf(m))
	m.runtime().SweepHA(t.Context(), m.tenant().TSGID, w, 0)
	if h := m.runtime().HA(); h.Bulk != "used" || h.Done != w.Clusters() {
		t.Fatalf("HA = %+v, want the aggregate to have answered for all %d clusters", h, w.Clusters())
	}
	m = settle(t, m)
	for _, r := range m.rows() {
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

	rt, tsg := m.runtime(), m.tenant().TSGID
	w := runtime.HAClusters(elementsOf(m))
	rt.StartHA(tsg, w)
	next := rt.ApplyHA(runtime.HAResult{TSG: tsg, Bulk: "status 403 Forbidden", Rest: w.Pairs})
	if len(next.Pairs) == 0 {
		t.Fatal("the refusal queued nothing, so the column would never fill")
	}
	if got := m.haPath(); !strings.Contains(got, "one at a time") || !strings.Contains(got, "403") {
		t.Errorf("haPath() = %q, want it to say the query was refused and what took over", got)
	}

	rt.ApplyHA(rt.FetchHAPairs(t.Context(), tsg, next.Pairs))
	m = settle(t, m)
	if got := plain(m.haCell(rowFor(t, m, set.Tenants[0].Elements[0].ID))); got != "active" {
		t.Errorf("cell = %q after the fallback sweep, want the state", got)
	}
}

func TestCachedHAIsShownWithAnAsteriskUntilItIsRefreshed(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := rowFor(t, m, set.Tenants[0].Elements[0].ID)
	m.runtime().SetCachedHA(map[string]string{first.Key(): haActive})

	if got := plain(m.haCell(first)); got != "active*" {
		t.Errorf("cell = %q, want the cached state marked", got)
	}

	m = settle(t, m)
	m.runtime().ApplyHA(runtime.HAResult{
		TSG:      m.tenant().TSGID,
		States:   map[string]string{first.Key(): haStandby},
		Answered: []string{first.ClusterID()},
	})
	m = settle(t, m)
	if got := plain(m.haCell(first)); got != "standby" {
		t.Errorf("cell = %q after a fresh sweep, want it unmarked", got)
	}
}

func TestASweepIsSavedForTheNextLaunch(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	tsg := m.tenant().TSGID
	eid := set.Tenants[0].Elements[0].ID

	m.runtime().SaveHA(tsg, map[string]string{tsg + "/" + eid: haActive})

	c, err := m.store().Cache(tsg)
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

	w := runtime.HAClusters(elementsOf(m))
	if len(w.Elements) < 20 {
		t.Fatalf("%d clustered elements, want the fleet", len(w.Elements))
	}

	ordered := m.runtime().NearestElements(m.tenant().TSGID, w.Elements)
	top := m.rows()[m.matched()[0]].ID
	if ordered[0] != top {
		t.Errorf("first id asked about = %s, want the row under the cursor (%s)", ordered[0], top)
	}

	m.moveCursorTo(len(m.matched()) - 1)
	ordered = m.runtime().NearestElements(m.tenant().TSGID, w.Elements)
	bottom := m.rows()[m.matched()[len(m.matched())-1]].ID
	if ordered[0] != bottom {
		t.Errorf("first id asked about = %s, want the row the cursor moved to (%s)", ordered[0], bottom)
	}

	m.filter.SetValue("store-0007")
	m.applyFilter()
	if len(m.matched()) == 0 || len(m.matched()) == len(m.rows()) {
		t.Fatalf("%d matches, want the query to have narrowed the list", len(m.matched()))
	}
	wanted := map[string]bool{}
	for _, i := range m.matched() {
		wanted[m.rows()[i].ID] = true
	}
	ordered = m.runtime().NearestElements(m.tenant().TSGID, w.Elements)
	for i := range len(wanted) {
		if !wanted[ordered[i]] {
			t.Errorf("id %d asked about (%s) is not one of the searched rows", i, ordered[i])
		}
	}
}

func TestTheNextChunkAsksForTheSearchedRowsFirst(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(20))
	m.phase = phasePicking
	m.width, m.height = 120, 24
	m.rebuildRows()

	rt, tsg := m.runtime(), m.tenant().TSGID
	w := runtime.HAClusters(elementsOf(m))
	rt.ApplyHA(runtime.HAResult{TSG: tsg, Bulk: "used", States: map[string]string{}, RestElements: w.Elements})
	if len(rt.Queued(tsg)) == 0 {
		t.Fatal("the rest of the fleet was not queued")
	}

	m.filter.SetValue("store-0012")
	m.applyFilter()
	wanted := map[string]bool{}
	for _, i := range m.matched() {
		wanted[m.rows()[i].ID] = true
	}
	if len(wanted) == 0 {
		t.Fatal("the query matched nothing")
	}
	next := rt.NearestElements(tsg, rt.Queued(tsg))
	for i := range len(wanted) {
		if !wanted[next[i]] {
			t.Errorf("id %d of the next chunk (%s) is not one of the searched rows", i, next[i])
		}
	}
}

func TestEachChunkIsCachedAsItLands(t *testing.T) {
	m, _ := fixtureTUIWith(t, manyClusterFixture(api.ClusterQueryLimit/2+10))
	rt, tsg := m.runtime(), m.tenant().TSGID

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go rt.SweepHA(ctx, tsg, runtime.HAClusters(elementsOf(m)), time.Hour)

	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _ := m.store().Cache(tsg)
		if c != nil && len(c.HA) > 0 {
			if len(rt.Queued(tsg)) == 0 {
				t.Fatal("the sweep finished in one chunk, so this proves nothing")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the first chunk was not saved while the rest waited")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
