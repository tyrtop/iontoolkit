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
	if !ch.Connected || ch.On == 0 {
		t.Errorf("channel = %+v, want it connected with a timestamp", ch)
	}

	if want := el.ports() + 1; len(snap.Interfaces) != want {
		t.Fatalf("got %d interfaces, want %d", len(snap.Interfaces), want)
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
	lastPort := snap.Interfaces[el.ports()-1]
	if lastPort.Status[0].OperationalState != "down" {
		t.Errorf("last port status = %+v, want it down", lastPort.Status[0])
	}

	vlan := snap.Interfaces[len(snap.Interfaces)-1]
	if vlan.Interface.Type != "vlan" || vlan.Interface.UsedFor != "lan" {
		t.Errorf("last interface = %+v, want the vlan", vlan.Interface)
	}
	if vlan.Status[0].Port != nil {
		t.Errorf("vlan port = %+v, want nothing: it is not a physical port", vlan.Status[0].Port)
	}
}

func TestADisconnectedFixtureElementHasEveryChannelDown(t *testing.T) {
	s := load(t, demo)
	for _, el := range s.Tenants[0].Elements {
		if el.Connected {
			continue
		}
		c := fixtureClient(s, s.Tenants[0].TSGID)
		snap := c.ElementSnapshot(t.Context(), el.apiElement())
		if snap.StatusErr != nil {
			t.Fatalf("status of %q: %v", el.ID, snap.StatusErr)
		}
		st := snap.Status
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
	theirs.SiteID = acme.Elements[0].apiElement().SiteID
	snap := c.ElementSnapshot(t.Context(), theirs)
	if snap.StatusErr == nil {
		t.Error("another tenant's element status resolved")
	}
	if snap.IfacesErr == nil {
		t.Fatal("another tenant's interfaces resolved")
	}
	if !strings.Contains(snap.IfacesErr.Error(), "404") {
		t.Errorf("error = %q, want a 404", snap.IfacesErr)
	}
}
