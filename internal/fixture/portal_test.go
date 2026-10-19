package fixture

import (
	"strings"
	"testing"
)

func TestTheRealClientTakesAPortalSnapshotOfAFixtureElement(t *testing.T) {
	s := load(t, demo)
	tenant := s.Tenants[0]
	el := tenant.Elements[0]
	c := fixtureClient(s, tenant.TSGID)

	snap := c.ElementSnapshot(t.Context(), el.apiElement())
	if err := snap.Err(); err != nil {
		t.Fatalf("ElementSnapshot() error = %v", err)
	}

	ch, ok := snap.Status.Channel("config_and_events")
	if !ok {
		t.Fatalf("no config_and_events channel in %+v", snap.Status.Channels)
	}
	// The element is connected in the demo file, so the channel that decides
	// what the portal calls connected has to agree.
	if !ch.Connected || ch.On == 0 {
		t.Errorf("channel = %+v, want it connected with a timestamp", ch)
	}

	if len(snap.Interfaces) != el.ports() {
		t.Fatalf("got %d interfaces, want %d", len(snap.Interfaces), el.ports())
	}
	first := snap.Interfaces[0]
	if first.Interface.Name != "1" || first.Interface.UsedFor != "public" {
		t.Errorf("first interface = %+v", first.Interface)
	}
	if len(first.Status) != 1 {
		t.Fatalf("got %d statuses for a port, want 1", len(first.Status))
	}
	up := first.Status[0]
	if up.OperationalState != "up" || len(up.IPv4Addresses) == 0 {
		t.Errorf("first interface status = %+v", up)
	}
	if up.Port == nil || !up.Port.Connected {
		t.Errorf("port = %+v, want the link up", up.Port)
	}
	// A port nobody wired up is down, which is what makes an interface list
	// worth drawing at all.
	last := snap.Interfaces[len(snap.Interfaces)-1]
	if last.Status[0].OperationalState != "down" {
		t.Errorf("last interface status = %+v, want it down", last.Status[0])
	}
}

func TestADisconnectedFixtureElementHasEveryChannelDown(t *testing.T) {
	s := load(t, demo)
	for _, el := range s.Tenants[0].Elements {
		if el.Connected {
			continue
		}
		c := fixtureClient(s, s.Tenants[0].TSGID)
		st, err := c.ElementStatus(t.Context(), el.ID)
		if err != nil {
			t.Fatalf("ElementStatus(%q) error = %v", el.ID, err)
		}
		if len(st.Channels) == 0 {
			t.Fatal("no channels at all")
		}
		for _, ch := range st.Channels {
			if ch.Connected {
				t.Errorf("channel %s is connected on a disconnected element", ch.Name)
			}
			if ch.Off == 0 {
				t.Errorf("channel %s has no disconnect time", ch.Name)
			}
		}
		return
	}
	t.Skip("no disconnected element in the demo fixture")
}

// A pair, because a cluster of one would not show that both members come back
// from the one endpoint.
const haPair = `
tenants:
  - name: Acme Co
    elements:
      - name: store-0003-rtr-01
        site: Store 0003
        connected: true
        ha: true
      - name: store-0003-rtr-02
        site: Store 0003
        ha: true
`

func TestAClusteredElementReportsItsClusterStatus(t *testing.T) {
	s := load(t, haPair)
	tenant := s.Tenants[0]
	c := fixtureClient(s, tenant.TSGID)

	el := tenant.Elements[0].apiElement()
	// The cluster id lives in spoke_ha_config, not the record's own
	// cluster_id, which a real clustered spoke left null.
	if el.Cluster != "" || el.ClusterID() == "" {
		t.Fatalf("cluster_id = %q, spoke_ha cluster = %q", el.Cluster, el.ClusterID())
	}
	if el.SpokeHA.Track == nil || len(el.SpokeHA.Track.Interfaces) != 1 {
		t.Errorf("track = %+v", el.SpokeHA.Track)
	}

	snap := c.ElementSnapshot(t.Context(), el)
	if err := snap.Err(); err != nil {
		t.Fatalf("ElementSnapshot() error = %v", err)
	}
	if snap.Cluster == nil {
		t.Fatal("no cluster status for a clustered element")
	}
	// Both members of the pair, from the one call, and only the connected one
	// is active.
	if len(snap.Cluster.Members) != 2 {
		t.Fatalf("%d cluster members, want 2: %+v", len(snap.Cluster.Members), snap.Cluster.Members)
	}
	active, ok := snap.Cluster.Active()
	if !ok || active.ElementID != el.ID {
		t.Errorf("Active() = %+v, %v, want %s", active, ok, el.ID)
	}
	if got := snap.Cluster.Members[1].Status; got != "unknown" {
		t.Errorf("the offline peer reads %q, want unknown", got)
	}

	// An element outside a cluster is not asked about at all, so nothing
	// fails and nothing is reported.
	alone := load(t, demo)
	c2 := fixtureClient(alone, alone.Tenants[0].TSGID)
	snap2 := c2.ElementSnapshot(t.Context(), alone.Tenants[0].Elements[0].apiElement())
	if snap2.Cluster != nil || snap2.ClusterErr != nil {
		t.Errorf("cluster = %+v, err = %v, want neither", snap2.Cluster, snap2.ClusterErr)
	}
}

func TestPortalEndpointsAreTenantScopedToo(t *testing.T) {
	s := load(t, demo)
	acme, lab := s.Tenants[0], s.Tenants[1]
	c := fixtureClient(s, acme.TSGID)

	theirs := lab.Elements[0].apiElement()
	if _, err := c.ElementStatus(t.Context(), theirs.ID); err == nil {
		t.Error("another tenant's element status resolved")
	}

	// Through our own site, because the site in the path is beside the point:
	// the element not being this tenant's is what makes it a 404.
	ours := acme.Elements[0].apiElement()
	_, err := c.ListInterfaces(t.Context(), ours.SiteID, theirs.ID)
	if err == nil {
		t.Fatal("another tenant's interfaces resolved")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want a 404", err)
	}
}
