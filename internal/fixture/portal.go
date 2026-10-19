package fixture

import (
	"fmt"
	"strconv"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

// The portal side of a fixture element: back-channel status and interfaces.
// None of it is in the YAML. An element's ports are derived from its model the
// way its ID is derived from its name, so the detail page has something
// shaped like an appliance to draw without every fixture file listing five
// ports.

// ports is how many an element of this model has. Unknown models get the
// 1200's five.
func (e Element) ports() int {
	switch e.Model {
	case "ion 1000", "ion 2000":
		return 4
	case "ion 3200", "ion 3200h":
		return 8
	default:
		return 5
	}
}

// interfaceID keeps a port's ID stable across runs, as element IDs are.
func (e Element) interfaceID(port int) string {
	return syntheticID(fmt.Sprintf("interface/%s/%d", e.ID, port), 19)
}

func (e Element) apiInterfaces() []api.Interface {
	out := make([]api.Interface, 0, e.ports())
	for p := 1; p <= e.ports(); p++ {
		in := api.Interface{
			ID:      e.interfaceID(p),
			Name:    strconv.Itoa(p),
			Type:    "port",
			AdminUp: true,
			MTU:     1500,
			Scope:   "local",
		}
		// Port 1 is the controller port on an ION, 2 the second circuit, 3 the
		// LAN, and the rest unused. The values are the ones a real element
		// answers with: "lan", not "private".
		switch p {
		case 1, 2:
			in.UsedFor = "public"
		case 3:
			in.UsedFor = "lan"
		default:
			in.UsedFor = "none"
		}
		if p <= fixtureCircuits {
			in.SiteWANInterfaceIDs = []string{e.circuitID(p)}
		}
		out = append(out, in)
	}

	// a real element carries its security zone on the vlans, not the ports
	out = append(out, api.Interface{
		ID:      e.vlanInterfaceID(),
		Name:    "vlan1",
		Type:    "vlan",
		UsedFor: "lan",
		AdminUp: true,
		MTU:     1500,
		Scope:   "local",
	})
	return out
}

func (e Element) vlanInterfaceID() string {
	return syntheticID("interface/"+e.ID+"/vlan1", 19)
}

// apiInterfaceStatus mirrors a real one: the status record has an ID of its
// own rather than the interface's, negotiated_mtu is 0 even on a port that is
// up, and extended_state is empty on an ordinary port -- only a service link
// explains itself there.
func (e Element) apiInterfaceStatus(iid string) *api.InterfaceStatus {
	if iid == e.vlanInterfaceID() {
		st := api.InterfaceStatus{
			ID:               syntheticID("interface-status/"+iid, 19),
			ElementID:        e.ID,
			Name:             "vlan1",
			Device:           "vlan1",
			OperationalState: "down",
			// No port block on anything that is not a physical port, which is
			// what a real element answers with.
			VRF:             &api.VRF{ContextID: syntheticID("vrf/"+e.tsg, 19), Name: "Global"},
			LastStateChange: bootedAt.Add(time.Minute).UnixMilli(),
		}
		if e.Connected {
			st.OperationalState = "up"
			st.IPv4Addresses = []string{"10.99.1.1/24"}
		}
		return &st
	}
	for p := 1; p <= e.ports(); p++ {
		if e.interfaceID(p) != iid {
			continue
		}
		st := api.InterfaceStatus{
			ID:               syntheticID("interface-status/"+iid, 19),
			ElementID:        e.ID,
			Name:             strconv.Itoa(p),
			Device:           "eth" + strconv.Itoa(p-1),
			MACAddress:       fakeMAC(iid),
			OperationalState: "down",
			Port:             &api.PortState{},
			VRF:              &api.VRF{ContextID: syntheticID("vrf/"+e.tsg, 19), Name: "Global"},
			LastStateChange:  bootedAt.Add(time.Minute).UnixMilli(),
		}
		// A disconnected element's ports are all down: the portal cannot see
		// past an element that is not talking to it.
		if e.Connected && p <= 3 {
			st.OperationalState = "up"
			st.IPv4Addresses = []string{fmt.Sprintf("10.%d.%d.1/24", p, p*7%256)}
			st.Port = &api.PortState{Connected: true, Speed: 1000, FullDuplex: true}
			st.Routes = []api.Route{{Destination: "0.0.0.0/0", Via: fmt.Sprintf("10.%d.%d.254", p, p*7%256)}}
		}
		return &st
	}
	return nil
}

// Circuits are the site's, two per site, with the first carrying the
// controller session the way a real site's usually does. Field names are the
// portal's own model: name, type, network_id, label_id, link_bw_down/up,
// use_for_controller_connections.
func (e Element) circuitID(n int) string {
	return syntheticID(fmt.Sprintf("waninterface/%s/%s/%d", e.tsg, e.Site, n), 19)
}

const fixtureCircuits = 2

func (e Element) apiCircuits() []map[string]any {
	out := make([]map[string]any, 0, fixtureCircuits)
	for n := 1; n <= fixtureCircuits; n++ {
		out = append(out, map[string]any{
			"id":                             e.circuitID(n),
			"name":                           fmt.Sprintf("ISP %d", n),
			"description":                    nil,
			"type":                           "publicwan",
			"network_id":                     fixtureNetworkID(n),
			"label_id":                       fixtureLabelID(n),
			"link_bw_down":                   100 * n,
			"link_bw_up":                     20 * n,
			"bw_config_mode":                 "manual",
			"lqm_enabled":                    true,
			"cost":                           128,
			"use_for_controller_connections": n == 1,
		})
	}
	return out
}

// apiCircuitStatus is the circuit as the site sees it; apiCircuitState is the
// same circuit as this element sees it, which is the pair that makes a
// one-sided failure visible. Both shapes are unverified against hardware --
// the resource map gave the paths, not the bodies.
func (e Element) apiCircuitStatus(cid string) map[string]any {
	for n := 1; n <= fixtureCircuits; n++ {
		if e.circuitID(n) != cid {
			continue
		}
		state := "down"
		if e.Connected && n == 1 {
			state = "up"
		}
		return map[string]any{"id": cid, "operational_state": state}
	}
	return nil
}

func (e Element) apiCircuitState(cid string) map[string]any {
	for n := 1; n <= fixtureCircuits; n++ {
		if e.circuitID(n) != cid {
			continue
		}
		return map[string]any{
			"id":          cid,
			"operational": e.Connected && n == 1,
			"disabled":    false,
			"inactive":    false,
		}
	}
	return nil
}

func fixtureLabelID(n int) string   { return syntheticID(fmt.Sprintf("label/%d", n), 19) }
func fixtureNetworkID(n int) string { return syntheticID(fmt.Sprintf("wannetwork/%d", n), 19) }

func fixtureLabels() []map[string]any {
	out := make([]map[string]any, 0, fixtureCircuits)
	for n := 1; n <= fixtureCircuits; n++ {
		out = append(out, map[string]any{
			"id":    fixtureLabelID(n),
			"name":  fmt.Sprintf("Circuit to ISP %d", n),
			"label": fmt.Sprintf("public-%d", n),
		})
	}
	return out
}

func fixtureNetworks() []map[string]any {
	out := make([]map[string]any, 0, fixtureCircuits)
	for n := 1; n <= fixtureCircuits; n++ {
		out = append(out, map[string]any{
			"id":   fixtureNetworkID(n),
			"name": fmt.Sprintf("Internet %d", n),
			"type": "publicwan",
		})
	}
	return out
}

// clusterID is the site's HA cluster, shared by every HA element on it, so a
// pair reports the same cluster the way a real one does.
func (e Element) clusterID() string {
	return syntheticID("spokecluster/"+e.tsg+"/"+e.Site, 19)
}

// apiClusterStatus answers the spoke cluster status endpoint, in the shape a
// real cluster answered in: a member is "active" or "unknown" -- there is no
// "standby" -- and a switchover carries the adjustments that moved the
// effective priority off the configured one.
func (s *Set) apiClusterStatus(tsg, cid string) map[string]any {
	t := s.tenant(tsg)
	if t == nil {
		return nil
	}

	members := []map[string]any{}
	events := []map[string]any{}
	for _, el := range t.Elements {
		if !el.HA || el.clusterID() != cid {
			continue
		}
		// A member the controller cannot see reads as "unknown", which is what
		// a dead peer looks like.
		status := "unknown"
		if el.Connected {
			status = "active"
		}
		members = append(members, map[string]any{"element_id": el.ID, "status": status})

		events = append(events, map[string]any{
			"element_id":         el.ID,
			"active":             el.Connected,
			"switch_over_time":   bootedAt.Add(time.Minute).UnixMilli(),
			"effective_priority": 160,
			"peer_connected":     false,
			"cluster_events": []map[string]any{{
				"time":               bootedAt.Add(2 * time.Minute).UnixMilli(),
				"effective_priority": 135,
				"peer_connected":     false,
				"adjustments": []map[string]any{
					{"adjustment": 25, "kind": "process", "name": "radvd"},
				},
			}},
		})
	}
	if len(members) == 0 {
		return nil
	}
	return map[string]any{"cluster_members": members, "switch_over_events": events}
}

// apiStatus is the flat shape the element status endpoint answers with: one
// group of fields per back-channel. A disconnected element has every channel
// down, which is what makes the portal call it disconnected.
func (e Element) apiStatus() map[string]any {
	out := map[string]any{
		"element_id": e.ID,
		"site_id":    e.siteID(),
		"tenant_id":  e.tsg,
	}
	out["device_mode"] = "l3-mode"
	out["last_rebooted_info"] = "Requested by API"
	// Seconds, where the channels below are not: a real response carries all
	// three scales at once, and a fixture that used one would hide it.
	out["last_rebooted_time"] = bootedAt.Unix()
	if e.Connected {
		out["controller_connection_intf"] = e.interfaceID(1)
	}

	on := bootedAt.Add(2 * time.Minute)
	for _, ch := range []string{
		"config_and_events", "analytics_live", "analytics_backlog",
		"flows_live", "flows_backlog", "logs_live", "logs_backlog",
	} {
		out[ch+"_connected"] = e.Connected
		out[ch+"_from"] = ""
		out[ch+"_connected_on_utc"] = 0
		out[ch+"_disconnected_on_utc"] = 0
		// config_and_events counts in 100ns ticks and the rest in
		// milliseconds, which is what a real element status does.
		stamp := on.UnixMilli()
		if ch == "config_and_events" {
			stamp = utcTicks(on)
		}
		if e.Connected {
			out[ch+"_from"] = "10.184.153.41"
			out[ch+"_connected_on_utc"] = stamp
		} else {
			out[ch+"_disconnected_on_utc"] = stamp
			out["last_disconnected_time"] = utcTicks(on)
		}
	}
	return out
}

// bootedAt fixes the fixture's clock so a run is reproducible: the day the
// first ION in the demo file was imagined to have come up.
var bootedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// utcTicks is one of the API's timestamp units, 100ns since the epoch.
func utcTicks(t time.Time) int64 {
	return t.UnixNano() / 100
}

func fakeMAC(seed string) string {
	d := syntheticID("mac/"+seed, 12)
	return fmt.Sprintf("02:%s:%s:%s:%s:%s", d[0:2], d[2:4], d[4:6], d[6:8], d[8:10])
}

// a flat row per element, "active" or "backup", as the capture shows
func (s *Set) apiClusterStatuses(tsg string) []map[string]any {
	ten := s.tenant(tsg)
	if ten == nil {
		return nil
	}
	var out []map[string]any
	for i := range ten.Elements {
		el := &ten.Elements[i]
		if !el.HA {
			continue
		}
		// The first HA element on a site carries it, as the per-cluster
		// fixture decides too.
		status, priority := "backup", 100
		if s.activeMember(tsg, el.clusterID()) == el.ID {
			status, priority = "active", 150
		}
		out = append(out, map[string]any{
			"id":                    syntheticID("clusterstatus/"+el.ID, 17),
			"site_id":               el.siteID(),
			"spoke_cluster_id":      el.clusterID(),
			"element_id":            el.ID,
			"status":                status,
			"priority":              priority,
			"last_status_update_ts": bootedAt.UnixMilli(),
		})
	}
	return out
}

// the per-cluster fixture and the aggregate have to agree on this
func (s *Set) activeMember(tsg, cid string) string {
	ten := s.tenant(tsg)
	if ten == nil {
		return ""
	}
	for i := range ten.Elements {
		el := &ten.Elements[i]
		if el.HA && el.clusterID() == cid && el.Connected {
			return el.ID
		}
	}
	return ""
}

// names like a real tenant's, so the join looks like it would in front of one
func fixtureZones() []map[string]any {
	return []map[string]any{
		{"id": syntheticID("zone/trusted", 18), "name": "Store - LAN",
			"description": "LAN side", "tcp_allow_non_syn": false, "is_l2": false},
		{"id": syntheticID("zone/guest", 18), "name": "Store - Guest",
			"description": "guest wifi", "tcp_allow_non_syn": false, "is_l2": false},
		{"id": syntheticID("zone/internet", 18), "name": "internet",
			"description": "circuits", "tcp_allow_non_syn": false, "is_l2": false},
	}
}

// one record per zone, holding its interface ids; verified in interface.har
func (e Element) apiZoneBindings() []map[string]any {
	return []map[string]any{
		{
			"id":               syntheticID("elzone/lan/"+e.ID, 18),
			"zone_id":          syntheticID("zone/trusted", 18),
			"interface_ids":    []string{e.vlanInterfaceID()},
			"lannetwork_ids":   nil,
			"waninterface_ids": nil,
			"wanoverlay_ids":   nil,
		},
		{
			// A zone with nothing of this element in it, which a tenant always
			// has: the join has to skip it rather than name it.
			"id":               syntheticID("elzone/guest/"+e.ID, 18),
			"zone_id":          syntheticID("zone/guest", 18),
			"interface_ids":    nil,
			"lannetwork_ids":   nil,
			"waninterface_ids": nil,
			"wanoverlay_ids":   nil,
		},
	}
}
