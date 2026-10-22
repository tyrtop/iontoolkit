package fixture

import (
	"fmt"
	"strconv"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

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
		switch p {
		case 1, 2:
			in.UsedFor = "public"
			in.IPv4Config = &api.IPv4Config{Type: "dhcp"}
		case 3:
			in.UsedFor = "lan"
			in.IPv4Config = &api.IPv4Config{
				Type:         "static",
				StaticConfig: &api.IPv4StaticConfig{Address: portAddress(p)},
			}
		default:
			in.UsedFor = "none"
		}
		if p <= fixtureCircuits {
			in.SiteWANInterfaceIDs = []string{e.circuitID(p)}
		}
		out = append(out, in)
	}

	out = append(out, api.Interface{
		ID:      e.vlanInterfaceID(),
		Name:    "vlan1",
		Type:    "vlan",
		UsedFor: "lan",
		AdminUp: true,
		MTU:     1500,
		Scope:   "local",
		IPv4Config: &api.IPv4Config{
			Type:         "static",
			StaticConfig: &api.IPv4StaticConfig{Address: vlanAddress},
		},
	})
	return out
}

func (e Element) vlanInterfaceID() string {
	return syntheticID("interface/"+e.ID+"/vlan1", 19)
}

func (e Element) apiInterfaceStatus(iid string) *api.InterfaceStatus {
	if iid == e.vlanInterfaceID() {
		st := api.InterfaceStatus{
			ID:               syntheticID("interface-status/"+iid, 19),
			ElementID:        e.ID,
			Name:             "vlan1",
			Device:           "vlan1",
			OperationalState: "down",
			VRF:              &api.VRF{ContextID: syntheticID("vrf/"+e.tsg, 19), Name: "Global"},
			LastStateChange:  bootedAt.Add(time.Minute).UnixMilli(),
		}
		if e.Connected {
			st.OperationalState = "up"
			st.IPv4Addresses = []string{vlanAddress}
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
		if e.Connected && p <= 3 {
			st.OperationalState = "up"
			st.IPv4Addresses = []string{portAddress(p)}
			st.Port = &api.PortState{Connected: true, Speed: 1000, FullDuplex: true}
			st.Routes = []api.Route{{Destination: "0.0.0.0/0", Via: fmt.Sprintf("10.%d.%d.254", p, p*7%256)}}
		}
		return &st
	}
	return nil
}

const vlanAddress = "10.99.1.1/24"

func portAddress(p int) string {
	return fmt.Sprintf("10.%d.%d.1/24", p, p*7%256)
}

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

func (e Element) clusterID() string {
	return syntheticID("spokecluster/"+e.tsg+"/"+e.Site, 19)
}

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

func (e Element) apiStatus() map[string]any {
	out := map[string]any{
		"element_id": e.ID,
		"site_id":    e.siteID(),
		"tenant_id":  e.tsg,
	}
	out["device_mode"] = "l3-mode"
	out["last_rebooted_info"] = "Requested by API"
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

var bootedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func utcTicks(t time.Time) int64 {
	return t.UnixNano() / 100
}

func fakeMAC(seed string) string {
	d := syntheticID("mac/"+seed, 12)
	return fmt.Sprintf("02:%s:%s:%s:%s:%s", d[0:2], d[2:4], d[4:6], d[6:8], d[8:10])
}

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
			"id":               syntheticID("elzone/guest/"+e.ID, 18),
			"zone_id":          syntheticID("zone/guest", 18),
			"interface_ids":    nil,
			"lannetwork_ids":   nil,
			"waninterface_ids": nil,
			"wanoverlay_ids":   nil,
		},
	}
}
