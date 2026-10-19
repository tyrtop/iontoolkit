package tui

import (
	"strings"
	"testing"

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
	// Before the status lands, a clustered element says it is in a cluster
	// and nothing more.
	first := set.Tenants[0].Elements[0]
	if got := plain(m.haCell(rowFor(t, m, first.ID))); got != "ha" {
		t.Errorf("cell before the fetch = %q, want it to admit only membership", got)
	}

	pairs := haClusters(elementsOf(m))
	if len(pairs) != 1 {
		t.Fatalf("%d clusters to read for a pair sharing one, want 1", len(pairs))
	}

	cmd := loadHA(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, pairs)
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
	if got := haClusters(els); len(got) != 2 {
		t.Errorf("haClusters() = %v, want one entry per cluster that can be asked about", got)
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
