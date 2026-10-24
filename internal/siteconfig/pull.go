package siteconfig

import (
	"fmt"
	"strings"
)

// cloudgenix_config's check_name: no name is "<kind> 1", a repeat "<name> 2"
type nameCheck map[string]int

func (n nameCheck) check(name, kind string) string {
	if name == "" {
		n["\x00"]++
		return fmt.Sprintf("%s %d", kind, n["\x00"])
	}
	if c, dup := n[name]; dup {
		n[name] = c + 1
		return fmt.Sprintf("%s %d", name, c+1)
	}
	n[name] = 1
	return name
}

func byName(records []any, kind string, nameOfRec func(map[string]any) string) (map[string]any, map[string]any) {
	out, ids, seen := map[string]any{}, map[string]any{}, nameCheck{}
	for _, r := range records {
		rec := asMap(r)
		name := str(rec["name"])
		if nameOfRec != nil {
			name = nameOfRec(rec)
		}
		name = seen.check(name, kind)
		rec["name"] = name
		out[name] = rec
		ids[str(rec["id"])] = name
	}
	return out, ids
}

func nameOf(byID map[string]any, id any) any {
	if id == nil {
		return nil
	}
	if n, ok := byID[str(id)]; ok {
		return n
	}
	return id
}

func renameOne(rec map[string]any, key string, byID map[string]any) {
	if v, ok := rec[key]; ok {
		rec[key] = nameOf(byID, v)
	}
}

func renameAll(rec map[string]any, key string, byID map[string]any) {
	if v, ok := rec[key]; ok {
		rec[key] = namesOf(byID, v)
	}
}

func namesOf(byID map[string]any, ids any) any {
	l, ok := ids.([]any)
	if !ok {
		return ids
	}
	out := make([]any, len(l))
	for i, id := range l {
		out[i] = nameOf(byID, id)
	}
	return out
}

// cloudgenix_config nulls a reference to something the controller generated
func nullAll(v any, ids map[string]bool) any {
	switch t := v.(type) {
	case string:
		if ids[t] {
			return nil
		}
	case map[string]any:
		for k, e := range t {
			t[k] = nullAll(e, ids)
		}
	case []any:
		for i, e := range t {
			t[i] = nullAll(e, ids)
		}
	}
	return v
}

type puller struct {
	p    Portal
	say  func(string)
	errs []error
}

func (r *puller) list(path string, v vars) []any {
	body, err := r.p.Get(fill(path, v))
	if err != nil {
		r.errs = append(r.errs, err)
		return []any{}
	}
	if items, ok := asMap(body)["items"].([]any); ok {
		return items
	}
	return []any{}
}

// cloudgenix_config's extract_items: a 404 is no records
func (r *puller) cached(path string, v vars) []any {
	body, err := r.p.Get(fill(path, v))
	if err != nil {
		if !notFound(err) {
			r.errs = append(r.errs, err)
		}
		return []any{}
	}
	if items, ok := asMap(body)["items"].([]any); ok {
		return items
	}
	return []any{}
}

func (r *puller) first(path string, v vars) any {
	if l := r.list(path, v); len(l) > 0 {
		return l[0]
	}
	return nil
}

func Pull(p Portal, site string, say func(string)) (map[string]any, error) {
	r := &puller{p: p, say: say}
	var matches []map[string]any
	for _, s := range r.list(pSites, nil) {
		if rec := asMap(s); str(rec["name"]) == site {
			matches = append(matches, rec)
		}
	}
	if err := r.err(); err != nil {
		return nil, err
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("expected exactly one site called %s, found %d", site, len(matches))
	}
	src := matches[0]
	id := str(src["id"])
	v := vars{"site": id}
	say("reading " + site)

	networks := map[string]string{}
	for _, n := range r.list(pWANNetworks, nil) {
		networks[str(asMap(n)["id"])] = str(asMap(n)["name"])
	}
	m := map[string]any{"source": map[string]any{"id": id, "name": site}, "site": src}
	var wanIDs, lanIDs, clusterIDs map[string]any
	m["waninterfaces"], wanIDs = byName(r.list(pWANInterfaces, v), "Waninterface", func(rec map[string]any) string {
		if name := str(rec["name"]); name != "" {
			return name
		}
		network := networks[str(rec["network_id"])]
		if network == "" {
			network = str(rec["network_id"])
		}
		return "Circuit to " + network
	})
	m["lannetworks"], lanIDs = byName(r.list(pLANNetworks, v), "Laninterface", nil)
	m["spokeclusters"], clusterIDs = byName(r.list(pSpokeClusters, v), "Spokecluster", nil)
	m["wan_ids"], m["lan_ids"], m["cluster_ids"] = wanIDs, lanIDs, clusterIDs
	m["hubclusters"] = r.list(pHubClusters, v)
	m["dhcpservers"] = r.list(pDHCPServers, v)
	m["natlocalprefixes"] = r.list(pNATLocalPrefixes, v)
	zones := r.list(pSiteZones, v)
	for _, z := range zones {
		for _, n := range asList(asMap(z)["networks"]) {
			net := asMap(n)
			switch str(net["network_type"]) {
			case "wan_network":
				renameOne(net, "network_id", wanIDs)
			case "lan_network":
				renameOne(net, "network_id", lanIDs)
			}
		}
	}
	m["sitesecurityzones"] = zones
	extensions := r.list(pSiteExtensions, v)
	for _, x := range extensions {
		if e := asMap(x); wanIDs[str(e["entity_id"])] != nil {
			renameOne(e, "entity_id", wanIDs)
		} else {
			renameOne(e, "entity_id", lanIDs)
		}
	}
	m["site_extensions"] = extensions
	m["ipfixlocalprefixes"] = r.list(pIPFIXPrefixes, v)
	m["multicastsources"] = r.list(pMulticastSources, v)
	deviceid := r.list(pDeviceIDConfigs, v)
	for _, d := range deviceid {
		cfg := asMap(d)
		cfg["snmpdiscoverystartnodes"] = r.list(pSNMPDiscoveryNodes, vars{"site": id, "dev": str(cfg["id"])})
	}
	m["deviceidconfigs"] = deviceid

	var mine []any
	for _, e := range r.list(pElements, nil) {
		if str(asMap(e)["site_id"]) == id {
			mine = append(mine, e)
		}
	}
	byEl, _ := byName(mine, "Element", nil)
	elements := map[string]any{}
	for _, name := range names(byEl) {
		elements[name] = r.element(id, asMap(byEl[name]), wanIDs, lanIDs, clusterIDs)
	}
	m["elements"] = elements
	return m, r.err()
}

func (r *puller) err() error {
	if len(r.errs) > 0 {
		return r.errs[0]
	}
	return nil
}

func (r *puller) element(site string, el, wanIDs, lanIDs, clusterIDs map[string]any) map[string]any {
	name := str(el["name"])
	elID := str(el["id"])
	v := vars{"site": site, "el": elID}
	r.say("reading " + name)
	// pull.py drops hw_id: the machine is found by serial_number
	delete(el, "hw_id")
	out := map[string]any{"record": el}

	modules := r.list(pCellularModules, vars{"el": elID})
	moduleIDs := map[string]any{}
	var sims []any
	for _, mod := range modules {
		rec := asMap(mod)
		moduleIDs[str(rec["id"])] = rec["name"]
		sims = append(sims, r.list(pSIMSecurity, vars{"el": elID, "id": str(rec["id"])})...)
	}
	out["cellular_modules"] = modules
	out["sim_security"] = sims

	all := r.list(pInterfaces, v)
	kinds, counts, ifIDs := map[string]string{}, map[string]int{}, map[string]any{}
	for _, i := range all {
		rec := asMap(i)
		kinds[str(rec["id"])] = str(rec["type"])
		counts[str(rec["name"])]++
		ifIDs[str(rec["id"])] = rec["name"]
	}
	parents, members := map[string]bool{}, map[string]bool{}
	for _, i := range all {
		rec := asMap(i)
		switch str(rec["type"]) {
		case "subinterface", "pppoe", "port":
			if rec["parent"] != nil {
				parents[str(rec["parent"])] = true
			}
		}
		if bp, ok := rec["bypass_pair"].(map[string]any); ok {
			for _, side := range []string{"wan", "lan"} {
				if id := str(bp[side]); bp[side] != nil && kinds[id] == "port" {
					parents[id], members[id] = true, true
				}
			}
		}
	}
	nine := str(el["model_name"]) == "ion 9000"
	ifaces, seen := map[string]any{}, nameCheck{}
	for _, i := range all {
		rec := asMap(i)
		id, t := str(rec["id"]), str(rec["type"])
		if nine && counts[str(rec["name"])] > 1 && t != "bypasspair" {
			continue
		}
		if parents[id] && (members[id] || (t != "virtual_interface" && t != "bypasspair" && t != "port")) {
			continue
		}
		n := str(rec["name"])
		if t := str(rec["type"]); nameable[t] {
			n = seen.check(n, t)
			rec["name"] = n
		}
		ifaces[n] = rec
		ifIDs[str(rec["id"])] = n
	}
	for _, i := range ifaces {
		rec := asMap(i)
		renameOne(rec, "parent", ifIDs)
		if bp, ok := rec["bypass_pair"].(map[string]any); ok {
			renameOne(bp, "wan", ifIDs)
			renameOne(bp, "lan", ifIDs)
		}
		// CGB-8874: WAN interfaces leak onto loopbacks
		if str(rec["type"]) == "loopback" {
			if _, ok := rec["site_wan_interface_ids"]; ok {
				rec["site_wan_interface_ids"] = nil
			}
		} else {
			renameAll(rec, "site_wan_interface_ids", wanIDs)
		}
		if att, ok := rec["attached_lan_networks"].([]any); ok && len(att) > 0 {
			trimmed := make([]any, len(att))
			for k, a := range att {
				trimmed[k] = map[string]any{"lan_network_id": nameOf(lanIDs, asMap(a)["lan_network_id"]), "vlan_id": asMap(a)["vlan_id"]}
			}
			rec["attached_lan_networks"] = trimmed
		}
		if relay, ok := rec["dhcp_relay"].(map[string]any); ok {
			renameOne(relay, "source_interface", ifIDs)
		}
		if bound, ok := rec["bound_interfaces"].([]any); ok {
			for k, b := range bound {
				bound[k] = ifIDs[str(b)]
			}
		}
		if cell, ok := rec["cellular_config"].(map[string]any); ok {
			renameOne(cell, "parent_module_id", moduleIDs)
		}
	}
	out["interfaces"] = ifaces

	if ha, ok := el["spoke_ha_config"].(map[string]any); ok {
		renameOne(ha, "cluster_id", clusterIDs)
		renameOne(ha, "source_interface", ifIDs)
		if track, ok := ha["track"].(map[string]any); ok {
			for _, t := range asList(track["interfaces"]) {
				renameOne(asMap(t), "interface_id", ifIDs)
			}
			for _, t := range asList(track["waninterfaces"]) {
				renameOne(asMap(t), "wan_interface_id", wanIDs)
			}
		}
	}

	routes := r.list(pStaticRoutes, v)
	for _, s := range routes {
		for _, h := range asList(asMap(s)["nexthops"]) {
			renameOne(asMap(h), "nexthop_interface_id", ifIDs)
		}
	}
	static := map[string]any{}
	for _, s := range routes {
		rec := asMap(s)
		rec["name"] = seen.check(str(rec["name"]), "Static Route")
		static[str(rec["name"])] = rec
	}
	out["static"] = static

	if g := r.cached(pBGPConfigs, v); len(g) > 0 {
		out["bgp_global"] = g[0]
	}

	generated := map[string]bool{}
	routing := map[string]any{}
	for _, kind := range []struct{ key, path, label string }{
		{"prefixlists", pPrefixLists, "Prefix List"}, {"aspathaccesslists", pASPathLists, "AS-PATH Access List"},
		{"ipcommunitylists", pCommunityLists, "IP-Community List"}, {"routemaps", pRouteMaps, "Route Map"},
	} {
		var kept []any
		for _, rec := range r.cached(kind.path, v) {
			if asMap(rec)["auto_generated"] == true {
				generated[str(asMap(rec)["id"])] = true
				continue
			}
			kept = append(kept, rec)
		}
		byKind, _ := byName(kept, kind.label, nil)
		var ordered []any
		for _, n := range names(byKind) {
			ordered = append(ordered, byKind[n])
		}
		routing[kind.key] = ordered
	}
	for _, rm := range asList(routing["routemaps"]) {
		nullAll(asMap(rm)["route_map_entries"], generated)
	}
	out["routing"] = routing
	peers, _ := byName(r.cached(pBGPPeers, v), "BGP Peer", nil)
	for _, pr := range peers {
		rec := asMap(pr)
		for _, k := range []string{"route_map_in_id", "route_map_out_id"} {
			if generated[str(rec[k])] {
				rec[k] = nil
			}
		}
	}
	out["peers"] = peers

	zones := r.list(pZones, v)
	for _, z := range zones {
		zone := asMap(z)
		renameAll(zone, "interface_ids", ifIDs)
		renameAll(zone, "waninterface_ids", wanIDs)
		renameAll(zone, "lannetwork_ids", lanIDs)
	}
	out["zones"] = zones

	syslog := r.list(pSyslogServers, v)
	for _, s := range syslog {
		rec := asMap(s)
		renameOne(rec, "source_interface", ifIDs)
		// cloudgenix_config CGCBL-516: a profile's server takes no port of its own
		if rec["syslog_profile_id"] != nil {
			rec["server_port"] = nil
		}
	}
	out["syslog"] = syslog

	if ntp := r.first(pNTP, v); ntp != nil {
		renameAll(asMap(ntp), "source_interface_ids", ifIDs)
		out["ntp"] = ntp
	}
	if probe, err := r.p.Get(fill(pAppProbe, v)); err != nil {
		if !probeAbsent(err) {
			r.errs = append(r.errs, err)
		}
	} else if pm, ok := probe.(map[string]any); ok && pm["id"] != nil {
		renameOne(pm, "source_interface_id", ifIDs)
		out["probe"] = pm
	}
	if got, err := r.p.Get(fill(pAccessConfigs, v)); err != nil {
		r.errs = append(r.errs, err)
	} else if t := one(got); t != nil {
		out["toolkit"] = t
	}
	out["multicast"] = r.list(pMulticastGlobals, v)
	out["multicastrps"] = r.list(pMulticastRPs, v)
	out["snmp_agents"] = r.list(pSNMPAgents, v)
	out["snmp_traps"] = r.list(pSNMPTraps, v)
	out["extensions"] = r.list(pElementExtensions, v)
	dns := r.list(pDNSServices, v)
	for _, d := range dns {
		for _, role := range asList(asMap(d)["dnsservicerole_bindings"]) {
			for _, i := range asList(asMap(role)["interfaces"]) {
				renameOne(asMap(i), "interface_id", ifIDs)
			}
		}
		for _, i := range asList(asMap(d)["domains_to_interfaces"]) {
			renameOne(asMap(i), "interface_id", ifIDs)
		}
	}
	out["dnsservices"] = dns
	radii := r.list(pRadii, v)
	for _, x := range radii {
		renameOne(asMap(x), "source_interface_id", ifIDs)
	}
	out["radii"] = radii
	out["ipfix"] = r.list(pIPFIX, v)
	deviceid := r.list(pElementDeviceID, v)
	for _, x := range deviceid {
		renameOne(asMap(x), "name", ifIDs)
		renameOne(asMap(x), "snmp_discovery_source_interface_id", ifIDs)
	}
	out["deviceidconfigs"] = deviceid
	return out
}

func probeAbsent(err error) bool {
	return strings.Contains(err.Error(), "APPLICATION_PROBE_CONFIG_UNSUPPORTED_SWVERSION") ||
		strings.Contains(err.Error(), "APPLICATION_PROBE_CONFIG_NOT_PRESENT")
}
