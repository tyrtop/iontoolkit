package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/workflow"
)

type m = map[string]any

func ls(v ...any) []any { return v }

type sitePortal struct {
	answers map[string]any
	asked   []string
	sent    []string
}

func (p *sitePortal) GetJSON(_ context.Context, path string) ([]byte, error) {
	p.asked = append(p.asked, path)
	v, ok := p.answers[path]
	if !ok {
		return nil, errors.New(path + ": status 404 Not Found")
	}
	return json.Marshal(v)
}

func (p *sitePortal) Send(_ context.Context, method, path string, _ []byte) ([]byte, error) {
	p.sent = append(p.sent, method+" "+path)
	return nil, errors.New("the portal said no")
}

func items(v ...any) m { return m{"items": v} }

func storeSite() *sitePortal {
	const (
		v49  = "/sdwan/v4.9/api"
		site = "/sites/s1"
	)
	ans := map[string]any{
		"/sdwan/v2.1/api/wannetworks": items(m{"id": "n-inet", "name": "Internet", "type": "publicwan"},
			m{"id": "n-mpls", "name": "MPLS", "type": "privatewan"}),
		"/sdwan/v4.0/api" + site + "/hubclusters":     items(),
		"/sdwan/v2.1/api" + site + "/deviceidconfigs": items(),
		v49 + "/sites": items(
			m{"id": "s1", "name": "S5037", "admin_state": "active", "element_cluster_role": "SPOKE",
				"location": m{"latitude": 33.75, "longitude": -84.39, "description": "Store 5037"},
				"address":  m{"city": "Atlanta", "country": "United States"}, "policy_set_id": nil,
				"_etag": 7, "_debug": nil},
			m{"id": "s9", "name": "S9999"},
		),
		"/sdwan/v2.7/api" + site + "/waninterfaces": items(
			m{"id": "w1", "name": "ISP 1", "type": "publicwan", "network_id": "n-inet", "label_id": "l1", "link_bw_down": 100},
			m{"id": "w2", "name": "ISP 2", "type": "publicwan", "network_id": "n-inet", "label_id": "l2"},
			m{"id": "w3", "name": "LTE", "type": "publicwan", "network_id": "n-inet", "label_id": "l3"},
			m{"id": "w4", "name": "MPLS", "type": "privatewan", "network_id": "n-mpls", "label_id": "l4"},
		),
		"/sdwan/v3.3/api" + site + "/lannetworks":                items(),
		"/sdwan/v2.0/api" + site + "/spokeclusters":              items(m{"id": "c1", "name": "S5037 cluster", "preempt": true}),
		"/sdwan/v2.3/api" + site + "/dhcpservers":                items(m{"id": "d1", "subnet": "10.1.4.0/24", "gateway": "10.1.4.1", "disabled": false}),
		"/sdwan/v2.0/api" + site + "/natlocalprefixes":           items(m{"id": "np1", "prefix_id": "gp1", "ipv4_prefixes": ls("10.1.0.0/16")}),
		"/sdwan/v2.0/api" + site + "/sitesecurityzones":          items(),
		"/sdwan/v2.0/api" + site + "/extensions":                 items(),
		"/sdwan/v2.0/api" + site + "/ipfixlocalprefixes":         items(),
		"/sdwan/v2.0/api" + site + "/multicastsourcesiteconfigs": items(),
		"/sdwan/v3.1/api/elements": items(
			router("e1", "S5037-RTR-01", "Router 1", 200),
			router("e2", "S5037-RTR-02", "Router 2", 100),
			m{"id": "e9", "name": "S9999-RTR-01", "site_id": "s9"},
		),
		"/sdwan/v2.5/api/machines": items(
			m{"id": "m1", "sl_no": "NEW0001", "model_name": "ion 1200-s", "machine_state": "manufactured", "connected": true},
			m{"id": "m2", "sl_no": "NEW0002", "model_name": "ion 1200-s", "machine_state": "claimed", "connected": true},
		),
		"/sdwan/v2.2/api/element_images": items(m{"id": "img636", "version": "6.3.6-b9"}, m{"id": "img640", "version": "6.4.0-b3"}),
	}
	for _, e := range []struct{ id, wan1, bypass string }{{"e1", "w1", "w2"}, {"e2", "w1", "w2"}} {
		elementAnswers(ans, e.id, e.wan1, e.bypass)
	}
	return &sitePortal{answers: ans}
}

func router(id, name, desc string, priority int) m {
	return m{"id": id, "name": name, "site_id": "s1", "serial_number": "OLD-" + id, "model_name": "ion 2000",
		"software_version": "6.1.5-b2", "description": "S5037 " + desc, "connected": true, "state": "bound",
		"l3_lan_forwarding": true, "_etag": 3,
		"spoke_ha_config": m{"cluster_id": "c1", "enable": true, "priority": priority, "source_interface": id + "-ctl",
			"track": m{"interfaces": ls(m{"interface_id": id + "-3", "reduce_priority": 50}), "waninterfaces": nil}}}
}

func elementAnswers(ans map[string]any, el, wan1, bypassWAN string) {
	base := "/sites/s1/elements/" + el
	for _, path := range []string{"/sdwan/v2.0/api/elements/" + el + "/cellular_modules", "/sdwan/v2.0/api" + base + "/multicastrps",
		"/sdwan/v2.0/api" + base + "/ipfix", "/sdwan/v2.0/api" + base + "/deviceidconfigs"} {
		ans[path] = items()
	}
	dhcp := m{"type": "dhcp", "dhcp_config": nil}
	static := func(addr string) m {
		return m{"type": "static", "static_config": m{"address": addr}, "dns_v4_config": nil}
	}
	ans["/sdwan/v4.17/api"+base+"/interfaces"] = items(
		m{"id": el + "-1", "name": "1", "type": "port", "admin_up": true, "site_wan_interface_ids": ls(wan1), "ipv4_config": dhcp},
		m{"id": el + "-2", "name": "2", "type": "port", "description": "LTE uplink", "site_wan_interface_ids": ls("w3"), "ipv4_config": dhcp},
		m{"id": el + "-3", "name": "3", "type": "port", "admin_up": true, "ipv4_config": nil, "multicast_config": nil},
		m{"id": el + "-4", "name": "4", "type": "port"},
		m{"id": el + "-5", "name": "5", "type": "port", "description": "to peer Port 5"},
		m{"id": el + "-45", "name": "45", "type": "bypasspair", "bypass_pair": m{"wan": el + "-4", "lan": el + "-5", "use_relay": true},
			"site_wan_interface_ids": ls(bypassWAN, "w4"), "ipv4_config": dhcp, "mtu": 1500},
		m{"id": el + "-ctl", "name": "controller", "type": "port",
			"ipv4_config": m{"type": "dhcp", "dns_v4_config": m{"name_servers": ls("8.8.8.8")}}},
		m{"id": el + "-31", "name": "3.1", "type": "subinterface", "parent": el + "-3", "sub_interface": m{"vlan_id": 1},
			"ipv4_config": static("10.1.1.1/24"), "nat_zone_id": "nz-lan", "nat_pools": nil},
		m{"id": el + "-34", "name": "3.4", "type": "subinterface", "parent": el + "-3", "sub_interface": m{"vlan_id": 4},
			"ipv4_config": static("10.1.4.1/24")},
		m{"id": el + "-35", "name": "3.5", "type": "subinterface", "parent": el + "-3", "sub_interface": m{"vlan_id": 5},
			"ipv4_config": static("10.1.5.1/24")},
		m{"id": el + "-ap", "name": "AUTO-PRISMA_IPSEC-Tunnel_1", "type": "service_link", "parent": el + "-1"},
	)
	ans["/sdwan/v2.3/api"+base+"/staticroutes"] = items(
		m{"id": el + "-sr3", "name": "Static Route 3", "destination_prefix": "10.193.115.0/23", "scope": "global",
			"nexthops": ls(m{"nexthop_ip": "10.1.4.254", "nexthop_interface_id": el + "-34", "admin_distance": 1})},
		m{"id": el + "-sr1", "name": "LAN summary", "destination_prefix": "10.0.0.0/8", "scope": "global",
			"nexthops": ls(m{"nexthop_ip": "10.1.1.254", "nexthop_interface_id": el + "-31"})},
	)
	ans["/sdwan/v2.3/api"+base+"/bgpconfigs"] = items(m{"id": el + "-bgp", "local_as_num": "65001", "prefix_adv_type": "aggregate"})
	ans["/sdwan/v2.4/api"+base+"/bgppeers"] = items(
		m{"id": el + "-p1", "name": "AUTO-PRISMA_IPSEC-Tunnel_1", "peer_ip": "169.254.0.1"},
		m{"id": el + "-p2", "name": "core", "peer_ip": "10.1.4.2", "route_map_in_id": el + "-rm"},
	)
	ans["/sdwan/v2.1/api"+base+"/routing_prefixlists"] = items(m{"id": el + "-pl", "name": "lan", "prefix_filter_list": ls(m{"prefix": "10.0.0.0/8"})})
	ans["/sdwan/v2.1/api"+base+"/routing_aspathaccesslists"] = items()
	ans["/sdwan/v2.0/api"+base+"/routing_ipcommunitylists"] = items()
	ans["/sdwan/v2.2/api"+base+"/routing_routemaps"] = items(m{"id": el + "-rm", "name": "in",
		"route_map_entries": ls(m{"match": m{"ip_prefix_list_id": el + "-pl"}, "permit": true})})
	ans["/sdwan/v2.0/api"+base+"/securityzones"] = items(
		m{"id": el + "-z1", "zone_id": "gz-lan", "interface_ids": ls(el+"-31", el+"-34", el+"-35")},
		m{"id": el + "-z2", "zone_id": "gz-wan", "interface_ids": ls(el + "-ap"), "waninterface_ids": ls("w1", "w2", "w3", "555")},
	)
	ans["/sdwan/v2.2/api"+base+"/syslogservers"] = items(m{"id": el + "-sl", "name": "siem", "server_ip": "10.0.0.5", "source_interface": el + "-ctl"})
	ans["/sdwan/v2.0/api/elements/"+el+"/ntp"] = items(m{"id": el + "-ntp", "name": "default", "source_interface_ids": nil})
	ans["/sdwan/v2.0/api"+base+"/application_probe"] = m{"id": el + "-probe", "enable_probe": true, "source_interface_id": el + "-31"}
	ans["/sdwan/v2.2/api/elements/"+el+"/elementaccessconfigs"] = m{"id": el + "-ac", "ssh_outbound_enabled": false}
	ans["/sdwan/v2.1/api"+base+"/multicastglobalconfigs"] = items(m{"id": el + "-mg"})
	ans["/sdwan/v2.1/api"+base+"/snmpagents"] = items(m{"id": el + "-sa", "v2_config": m{"community": "public"}})
	ans["/sdwan/v2.0/api"+base+"/snmptraps"] = items()
	ans["/sdwan/v2.0/api"+base+"/extensions"] = items(m{"id": el + "-x", "name": "old extension"})
	ans["/sdwan/v2.0/api"+base+"/dnsservices"] = items()
	ans["/sdwan/v2.0/api/elements/"+el+"/radii"] = items()
}

func ion1200Dir() string {
	if dir := os.Getenv("ION1200_WORKFLOW"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "iontk", "workflows", "ion1200-migrate")
}

type migration struct {
	res   workflow.Result
	plan  m
	lines []string
	err   error
}

func migrate(t *testing.T, p *sitePortal, in map[string]string, doit bool) migration {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	WorkflowsFolder, WorkflowPaths = filepath.Join(root, "none"), []string{ion1200Dir()}
	t.Cleanup(func() { WorkflowsFolder, WorkflowPaths = "", nil })
	rt := NewRuntime(st, cfg)
	if w := rt.Warnings(); len(w) > 0 {
		t.Fatalf("the workflow did not load: %v", w)
	}
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	if in == nil {
		in = map[string]string{"site": "S5037", "rtr01_serial": "NEW0001", "rtr02_serial": "NEW0002"}
	}
	var out migration
	c := &runtime.Call{Ctx: context.Background(), Args: in, DoIt: doit, Out: func(s string) { out.lines = append(out.lines, s) }}
	out.err = rt.Exec(c, WorkflowPrefix+"ion1200-migrate")
	out.res, _ = c.Result.(workflow.Result)
	for _, l := range out.lines {
		if where, ok := strings.CutPrefix(l, "plan written to "); ok {
			b, err := os.ReadFile(where)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &out.plan); err != nil {
				t.Fatalf("the plan is not JSON: %v\n%s", err, b)
			}
		}
	}
	return out
}

func dig(v any, path ...string) any {
	for _, k := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestTheMigrationConvertsAStoreSiteAsTheOriginalDid(t *testing.T) {
	run := migrate(t, storeSite(), nil, false)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
	}
	p := run.plan
	r1 := dig(p, "elements", "S5037-RTR-01")
	r2 := dig(p, "elements", "S5037-RTR-02")

	check := func(what string, got any, want string) {
		t.Helper()
		if g := asJSON(got); g != want {
			t.Errorf("%s = %s, want %s", what, g, want)
		}
	}
	check("site name", dig(p, "site", "name"), `"S5037-REPL"`)
	check("site state", dig(p, "site", "admin_state"), `"disabled"`)
	check("site location", dig(p, "site", "location", "description"), `"Store 5037"`)
	check("site circuits", keysOf(dig(p, "waninterfaces")), `["ISP 1","ISP 2","MPLS"]`)

	ifs := keysOf(dig(r1, "interfaces"))
	check("RTR-01 interfaces", ifs, `["1","10","2","34","5","6","7","8","9","vlan1","vlan4","vlan5","vlan99"]`)
	check("RTR-02 interfaces", keysOf(dig(r2, "interfaces")), asJSON(ifs))

	check("RTR-01 vlan99", dig(r1, "interfaces", "vlan99", "ipv4_config", "static_config", "address"), `"10.193.114.253/30"`)
	check("RTR-01 vlan99 gateway", dig(r1, "interfaces", "vlan99", "ipv4_config", "routes"), `[{"destination":"0.0.0.0/0","via":"10.193.114.254"}]`)
	check("RTR-02 vlan99", dig(r2, "interfaces", "vlan99", "ipv4_config", "static_config", "address"), `"10.193.114.254/30"`)
	check("vlan99 DNS from the controller port", dig(r1, "interfaces", "vlan99", "ipv4_config", "dns_v4_config"), `{"name_servers":["8.8.8.8"]}`)
	check("vlan1 from 3.1", dig(r1, "interfaces", "vlan1", "vlan_config"), `{"mstp_instance":0,"vlan_id":1,"voice_enabled":false}`)
	check("vlan1 NAT zone kept", dig(r1, "interfaces", "vlan1", "nat_zone_id"), `"nz-lan"`)
	check("static DNS is an empty object", dig(r1, "interfaces", "vlan4", "ipv4_config", "dns_v4_config"), `{}`)
	check("port 5 trunk", dig(r1, "interfaces", "5", "switch_port_config", "vlan_mode"), `"trunk"`)
	check("port 7 PoE", dig(r1, "interfaces", "7", "poe_enabled"), `true`)
	check("port 6 down", dig(r1, "interfaces", "6", "admin_up"), `false`)
	check("bypass pair 34", dig(r1, "interfaces", "34", "bypass_pair"), `{"lan":"4","use_relay":true,"wan":"3"}`)
	check("port 2 description", dig(r1, "interfaces", "2", "description"), `""`)
	check("port 2 circuits", dig(r1, "interfaces", "2", "site_wan_interface_ids"), `null`)
	check("DHCP shape", dig(r1, "interfaces", "1", "ipv4_config", "dhcp_config"), `{"client_id":null,"hostname":null}`)

	check("RTR-02 port 1 circuits", dig(r2, "interfaces", "1", "site_wan_interface_ids"), `["ISP 2"]`)
	check("RTR-02 34 circuits", dig(r2, "interfaces", "34", "site_wan_interface_ids"), `["MPLS","ISP 1"]`)

	check("routes from RTR-01, local", dig(r2, "static", "LAN summary", "scope"), `"local"`)
	check("route next hop", dig(r2, "static", "Static Route 3", "nexthops"), `[{"admin_distance":1,"nexthop_interface_id":"vlan4","nexthop_ip":"10.1.4.254"}]`)
	check("syslog from controller", dig(r1, "syslog"), `[{"id":"e1-sl","name":"siem","server_ip":"10.0.0.5","source_interface":"vlan99"}]`)
	check("probe mirrored", dig(r2, "probe", "source_interface_id"), `"vlan1"`)
	check("BGP peers", keysOf(dig(r1, "peers")), `["core"]`)
	check("toolkit", dig(r1, "toolkit", "ssh_outbound_enabled"), `true`)
	check("software", dig(r1, "record", "software_version"), `"6.3.6-b9"`)
	check("serial", dig(r2, "record", "serial_number"), `"NEW0002"`)
	check("HA RTR-01", dig(r1, "record", "spoke_ha_config", "priority"), `150`)
	check("HA RTR-02 track", dig(r2, "record", "spoke_ha_config", "track"), `{"interfaces":[{"interface_id":"5","reduce_priority":100}],"waninterfaces":null}`)
	check("HA source", dig(r2, "record", "spoke_ha_config", "source_interface"), `"vlan99"`)
	check("SNMP", dig(r1, "snmp_agents"), `[{"description":null,"system_contact":null,"system_location":null,"tags":null,"v2_config":null,"v3_config":{"enabled":true,"users_access":[{"auth_phrase":"Placeholder1!","auth_type":"sha","enc_phrase":"Placeholder1!","enc_type":"aes","engine_id":null,"security_level":"private","user_name":"tscnms"}]}}]`)

	zones := asJSON(dig(r2, "zones"))
	for _, want := range []string{`"interface_ids":["vlan1","vlan4","vlan5"]`, `"waninterface_ids":["ISP 1","ISP 2"]`} {
		if !strings.Contains(zones, want) {
			t.Errorf("RTR-02 zones lack %s: %s", want, zones)
		}
	}
	if strings.Contains(zones, "AUTO-PRISMA") || strings.Contains(zones, "555") {
		t.Errorf("RTR-02 zones kept a tunnel or a stale circuit: %s", zones)
	}

	text := strings.Join(run.lines, "\n")
	for _, want := range []string{"checks: pass", "removed stale WAN interface 555", "removed Port 2 WAN interface LTE",
		"S5037-RTR-02 interface 1: Internet circuits made S5037-RTR-01's interface 34's (ISP 2, was ISP 1)",
		"plan written to "} {
		if !strings.Contains(text, want) {
			t.Errorf("the run never said %q:\n%s", want, text)
		}
	}
}

func keysOf(v any) []string {
	mm, _ := v.(map[string]any)
	var out []string
	for k := range mm {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func TestTheMigrationPushListsEveryWriteInOrder(t *testing.T) {
	run := migrate(t, storeSite(), nil, false)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
	}
	var seq []string
	for _, w := range run.res.Writes {
		seq = append(seq, w.Method+" "+w.Path)
	}
	all := strings.Join(seq, "\n")

	at := -1
	for i, w := range run.res.Writes {
		if w.Method == "POST" && w.Path == "/sdwan/v4.9/api/sites" {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("no site was made:\n%s", all)
	}
	site := run.res.Writes[at]
	if dig(site.Body, "admin_state") != "disabled" || dig(site.Body, "_etag") != nil || dig(site.Body, "id") != nil {
		t.Errorf("the new site = %v", site.Body)
	}
	siteID := fmt.Sprintf("dry-run-%d", at+1)

	order := []string{
		"POST /sdwan/v2.5/api/machines/m1/operations",
		"PUT /sdwan/v2.0/api/elements/dry-run-element-NEW0001/software/state",
		"PUT /sdwan/v2.0/api/elements/dry-run-element-NEW0002/software/state",
		"POST /sdwan/v4.9/api/sites",
		"POST /sdwan/v2.7/api/sites/dry-run-1/waninterfaces",
		"POST /sdwan/v2.0/api/sites/dry-run-1/spokeclusters",
		"PUT /sdwan/v3.1/api/elements/dry-run-element-NEW0001",
		"POST /sdwan/v4.17/api/sites/dry-run-1/elements/dry-run-element-NEW0001/interfaces",
		"PUT /sdwan/v4.17/api/sites/dry-run-1/elements/dry-run-element-NEW0001/interfaces/dry-run-if-NEW0001-5",
		"PUT /sdwan/v3.1/api/elements/dry-run-element-NEW0001",
		"POST /sdwan/v2.1/api/sites/dry-run-1/elements/dry-run-element-NEW0001/routing_prefixlists",
		"POST /sdwan/v2.2/api/sites/dry-run-1/elements/dry-run-element-NEW0001/routing_routemaps",
		"POST /sdwan/v2.4/api/sites/dry-run-1/elements/dry-run-element-NEW0001/bgppeers",
		"POST /sdwan/v2.3/api/sites/dry-run-1/elements/dry-run-element-NEW0001/staticroutes",
		"POST /sdwan/v2.1/api/sites/dry-run-1/elements/dry-run-element-NEW0001/snmpagents",
		"POST /sdwan/v2.0/api/sites/dry-run-1/elements/dry-run-element-NEW0001/securityzones",
		"PUT /sdwan/v3.1/api/elements/dry-run-element-NEW0002",
	}
	at = 0
	for _, want := range order {
		want = strings.ReplaceAll(want, "dry-run-1/", siteID+"/")
		i := strings.Index(all[at:], want)
		if i < 0 {
			t.Fatalf("no %q after write %d:\n%s", want, at, all)
		}
		at += i + len(want)
	}
	if strings.Count(all, "/machines/m2/operations") != 0 {
		t.Error("a machine already claimed was claimed again")
	}

	ifs := "/sdwan/v4.17/api/sites/" + siteID + "/elements/dry-run-element-NEW0001/interfaces"
	var steps []string
	for _, w := range run.res.Writes {
		switch {
		case w.Path == ifs+"/dry-run-if-NEW0001-3" || w.Path == ifs+"/dry-run-if-NEW0001-4":
			steps = append(steps, w.Method+" "+w.Path[len(ifs)+1:]+" "+asJSON(dig(w.Body, "used_for"))+" "+asJSON(dig(w.Body, "site_wan_interface_ids")))
		case w.Path == ifs && dig(w.Body, "type") == "bypasspair":
			steps = append(steps, "POST "+asJSON(dig(w.Body, "name")))
		}
	}
	if got := strings.Join(steps, "\n"); got != `PUT dry-run-if-NEW0001-3 "none" null
PUT dry-run-if-NEW0001-4 "none" null
POST null` {
		t.Errorf("bypass pair 34 went:\n%s", got)
	}

	var claim, assign, peer, ha m
	for _, w := range run.res.Writes {
		switch {
		case strings.HasSuffix(w.Path, "/machines/m1/operations"):
			claim, _ = w.Body.(map[string]any)
		case w.Path == "/sdwan/v3.1/api/elements/dry-run-element-NEW0001" && dig(w.Body, "site_id") == siteID && dig(w.Body, "spoke_ha_config") == nil:
			if assign == nil {
				assign, _ = w.Body.(map[string]any)
			}
		case strings.HasSuffix(w.Path, "dry-run-element-NEW0001/bgppeers"):
			peer, _ = w.Body.(map[string]any)
		case w.Path == "/sdwan/v3.1/api/elements/dry-run-element-NEW0002" && dig(w.Body, "spoke_ha_config") != nil:
			ha, _ = w.Body.(map[string]any)
		}
	}
	if asJSON(claim) != `{"inventory_op":"claim"}` {
		t.Errorf("claim = %s", asJSON(claim))
	}
	if dig(assign, "name") != "S5037-RTR-01" || dig(assign, "description") != "S5037 Router 1" {
		t.Errorf("assign = %s", asJSON(assign))
	}
	for _, w := range run.res.Writes {
		if strings.HasSuffix(w.Path, "dry-run-element-NEW0001/routing_routemaps") &&
			!strings.Contains(asJSON(w.Body), `"ip_prefix_list_id":"dry-run-`) {
			t.Errorf("the route map does not name the new prefix list: %s", asJSON(w.Body))
		}
	}
	if !strings.HasPrefix(asJSON(dig(peer, "route_map_in_id")), `"dry-run-`) {
		t.Errorf("the peer's route map was not the new element's: %s", asJSON(peer))
	}
	if dig(ha, "spoke_ha_config", "priority") != 100.0 ||
		!strings.HasPrefix(asJSON(dig(ha, "spoke_ha_config", "cluster_id")), `"dry-run-`) {
		t.Errorf("RTR-02 spoke HA = %s", asJSON(ha))
	}
	if dig(ha, "site_id") != siteID || dig(ha, "name") != "S5037-RTR-02" {
		t.Errorf("RTR-02 spoke HA is not on the element assign made: %s", asJSON(ha))
	}
	if !strings.Contains(strings.Join(run.lines, "\n"), "dry run: nothing sent") {
		t.Error("the result does not say it was a dry run")
	}
}

func TestTheMigrationCarriesWhatDoSiteCarries(t *testing.T) {
	p := storeSite()
	p.answers["/sdwan/v2.1/api/sites/s1/deviceidconfigs"] = items(m{"id": "dev1", "config_enabled": true})
	p.answers["/sdwan/v2.0/api/sites/s1/deviceidconfigs/dev1/snmpdiscoverystartnodes"] = items(m{"id": "n1", "name": "core", "ipv4_address": "10.1.4.1"})
	p.answers["/sdwan/v2.0/api/sites/s1/elements/e2/dnsservices"] = items(m{"id": "dns1", "name": "dns", "element_id": "e2",
		"domains_to_interfaces": ls(m{"interface_id": "e2-1", "domain_names": ls("store.local")})})
	run := migrate(t, p, nil, false)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
	}
	var seq []string
	for _, w := range run.res.Writes {
		seq = append(seq, w.Method+" "+w.Path+" "+asJSON(w.Body))
	}
	all := strings.Join(seq, "\n")
	for _, want := range []string{"/deviceidconfigs/dry-run-deviceid-", `"config_enabled":true`, `"deviceid_profile_id":null`,
		"/snmpdiscoverystartnodes", `"ipv4_address":"10.1.4.1"`, "dry-run-element-NEW0002/dnsservices", `"interface_id":"dry-run-`} {
		if !strings.Contains(all, want) {
			t.Errorf("the push has no %s:\n%s", want, all)
		}
	}
}

func TestTheMigrationRefusesWhatItCannotWorkOut(t *testing.T) {
	p := storeSite()
	p.answers["/sdwan/v2.3/api/sites/s1/elements/e1/staticroutes"] = items()
	run := migrate(t, p, nil, false)
	if run.err == nil || !strings.Contains(run.err.Error(), "static route 3") || len(run.res.Writes) != 0 {
		t.Errorf("err = %v, %d writes", run.err, len(run.res.Writes))
	}

	p = storeSite()
	p.answers["/sdwan/v4.0/api/sites/s1/hubclusters"] = items(m{"id": "hc1", "name": "dc"})
	if run := migrate(t, p, nil, false); run.err == nil || !strings.Contains(run.err.Error(), "hubclusters") || len(run.res.Writes) != 0 {
		t.Errorf("a site with hub clusters was taken: %v", run.err)
	}

	run = migrate(t, storeSite(), map[string]string{"site": "S5037", "rtr01_serial": "NEW0001", "rtr02_serial": "NEW0001"}, false)
	if run.err == nil || !strings.Contains(run.err.Error(), "different serials") {
		t.Errorf("the same serial twice was taken: %v", run.err)
	}

	run = migrate(t, storeSite(), map[string]string{"site": "S5037", "rtr01_serial": "NOPE", "rtr02_serial": "NEW0002"}, false)
	if run.err == nil || !strings.Contains(run.err.Error(), "no ION with serial NOPE") {
		t.Errorf("a serial not in inventory was taken: %v", run.err)
	}

	p = storeSite()
	els := p.answers["/sdwan/v3.1/api/elements"].(m)
	els["items"] = append(els["items"].([]any), m{"id": "elive", "serial_number": "NEW0002", "site_id": "s9"})
	if run := migrate(t, p, nil, false); run.err == nil || !strings.Contains(run.err.Error(), "NEW0002 is assigned to site s9") {
		t.Errorf("a router on another site was taken: %v", run.err)
	}

	sp := storeSite()
	sp.answers["/sdwan/v2.5/api/machines/m1"] = m{"id": "m1", "machine_state": "manufactured", "connected": true}
	run = migrate(t, sp, nil, true)
	if run.err == nil || !strings.Contains(run.err.Error(), "the portal said no") ||
		len(sp.sent) != 1 || sp.sent[0] != "POST /sdwan/v2.5/api/machines/m1/operations" {
		t.Errorf("--doit: %v, sent %v", run.err, sp.sent)
	}
}

func TestTheMigrationRefusesASiteAlreadyBuilt(t *testing.T) {
	p := storeSite()
	sites := p.answers["/sdwan/v4.9/api/sites"].(m)
	sites["items"] = append(sites["items"].([]any), m{"id": "r1", "name": "S5037-REPL"})
	run := migrate(t, p, nil, false)
	if run.err == nil || !strings.Contains(run.err.Error(), "a site is already called S5037-REPL") || len(run.res.Writes) != 0 {
		t.Errorf("err = %v, %d writes", run.err, len(run.res.Writes))
	}
}

func router1200(id, name, desc string, priority int) m {
	r := router(id, name, desc, priority)
	r["model_name"], r["software_version"] = "ion 1200-s", "6.3.4-b2"
	r["spoke_ha_config"] = m{"cluster_id": "c1", "enable": true, "priority": priority, "source_interface": id + "-v99",
		"track": m{"interfaces": ls(m{"interface_id": id + "-5", "reduce_priority": priority}), "waninterfaces": nil}}
	return r
}

func interfaces1200(ans map[string]any, el, wan1, bypassWAN string, port2WAN any, vlan99 string) {
	base := "/sites/s1/elements/" + el
	dhcp := m{"type": "dhcp", "dhcp_config": nil}
	static := func(addr string) m {
		return m{"type": "static", "static_config": m{"address": addr}, "dns_v4_config": m{}}
	}
	sw := func(n string, mode string) m {
		return m{"id": el + "-" + n, "name": n, "type": "switch_port", "used_for": "switch-access",
			"switch_port_config": m{"vlan_mode": mode, "native_vlan_id": 1, "trunk_vlans": ls("all")}}
	}
	vlan := func(id int, addr string) m {
		return m{"id": fmt.Sprintf("%s-v%d", el, id), "name": fmt.Sprintf("vlan%d", id), "type": "vlan", "used_for": "lan",
			"vlan_config": m{"vlan_id": id, "mstp_instance": 0, "voice_enabled": id == 6}, "ipv4_config": static(addr)}
	}
	v1 := vlan(1, "10.1.1.1/24")
	v1["nat_zone_id"] = "nz-lan"
	gw := strings.TrimSuffix(vlan99, ".253/30") + ".254"
	if strings.HasSuffix(vlan99, ".254/30") {
		gw = strings.TrimSuffix(vlan99, ".254/30") + ".253"
	}
	ans["/sdwan/v4.17/api"+base+"/interfaces"] = items(
		m{"id": el + "-1", "name": "1", "type": "port", "admin_up": true, "used_for": "public", "site_wan_interface_ids": ls(wan1), "ipv4_config": dhcp},
		m{"id": el + "-2", "name": "2", "type": "port", "used_for": "public", "site_wan_interface_ids": port2WAN, "ipv4_config": dhcp},
		m{"id": el + "-3", "name": "3", "type": "port"},
		m{"id": el + "-4", "name": "4", "type": "port"},
		m{"id": el + "-34", "name": "34", "type": "bypasspair", "used_for": "public", "bypass_pair": m{"wan": el + "-3", "lan": el + "-4", "use_relay": true},
			"site_wan_interface_ids": ls(bypassWAN, "w4"), "ipv4_config": dhcp},
		sw("5", "trunk"), sw("6", "access"), sw("7", "access"), sw("8", "access"), sw("9", "access"), sw("10", "access"),
		v1, vlan(4, "10.1.4.1/24"), vlan(5, "10.1.5.1/24"),
		m{"id": el + "-v99", "name": "vlan99", "type": "vlan", "used_for": "ha", "scope": "global",
			"vlan_config": m{"vlan_id": 99, "mstp_instance": 0, "voice_enabled": false},
			"ipv4_config": m{"type": "static", "static_config": m{"address": vlan99}, "dns_v4_config": m{},
				"routes": ls(m{"destination": "0.0.0.0/0", "via": gw})}},
		m{"id": el + "-ap", "name": "AUTO-PRISMA_IPSEC-Tunnel_1", "type": "service_link", "parent": el + "-1"},
	)
	ans["/sdwan/v2.3/api"+base+"/staticroutes"] = items(
		m{"id": el + "-sr3", "name": "Static Route 3", "destination_prefix": "10.193.114.252/30", "scope": "global",
			"nexthops": ls(m{"nexthop_ip": "10.1.4.254", "nexthop_interface_id": el + "-v4", "admin_distance": 1})},
		m{"id": el + "-sr1", "name": "LAN summary", "destination_prefix": "10.0.0.0/8", "scope": "global",
			"nexthops": ls(m{"nexthop_ip": "10.1.1.254", "nexthop_interface_id": el + "-v1"})},
	)
	ans["/sdwan/v2.0/api"+base+"/securityzones"] = items(
		m{"id": el + "-z1", "zone_id": "gz-lan", "interface_ids": ls(el+"-v1", el+"-v4", el+"-v5")},
		m{"id": el + "-z2", "zone_id": "gz-wan", "interface_ids": ls(el + "-ap"), "waninterface_ids": ls("w1", "w3", "555")},
	)
	ans["/sdwan/v2.2/api"+base+"/syslogservers"] = items(m{"id": el + "-sl", "name": "siem", "server_ip": "10.0.0.5", "source_interface": el + "-v5"})
	ans["/sdwan/v2.0/api"+base+"/application_probe"] = m{"id": el + "-probe", "enable_probe": true, "source_interface_id": el + "-v1"}
}

func ion1200Store(pair bool) *sitePortal {
	p := storeSite()
	els := []any{router1200("e1", "S5037-RTR-01", "Router 1", 150)}
	if pair {
		els = append(els, router1200("e2", "S5037-RTR-02", "Router 2", 100))
	}
	p.answers["/sdwan/v3.1/api/elements"] = items(append(els, m{"id": "e9", "name": "S9999-RTR-01", "site_id": "s9"})...)
	interfaces1200(p.answers, "e1", "w3", "w1", ls("w2"), "10.193.114.253/30")
	interfaces1200(p.answers, "e2", "w1", "w3", nil, "10.193.114.254/30")
	return p
}

func TestAnION1200SStoreIsCopiedOntoTheNewRouters(t *testing.T) {
	tests := []struct {
		name string
		pair bool
	}{
		{"a pair", true},
		{"one router, RTR-02 made from it", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := migrate(t, ion1200Store(tt.pair), nil, false)
			if run.err != nil {
				t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
			}
			p := run.plan
			r1 := dig(p, "elements", "S5037-RTR-01")
			r2 := dig(p, "elements", "S5037-RTR-02")
			check := func(what string, got any, want string) {
				t.Helper()
				if g := asJSON(got); g != want {
					t.Errorf("%s = %s, want %s", what, g, want)
				}
			}
			check("site circuits", keysOf(dig(p, "waninterfaces")), `["ISP 1","ISP 2","LTE","MPLS"]`)
			ifs := `["1","10","2","34","5","6","7","8","9","vlan1","vlan4","vlan5","vlan99"]`
			check("RTR-01 interfaces", keysOf(dig(r1, "interfaces")), ifs)
			check("RTR-02 interfaces", keysOf(dig(r2, "interfaces")), ifs)
			check("RTR-01 vlan99", dig(r1, "interfaces", "vlan99", "ipv4_config", "static_config", "address"), `"10.193.114.253/30"`)
			check("RTR-02 vlan99", dig(r2, "interfaces", "vlan99", "ipv4_config", "static_config", "address"), `"10.193.114.254/30"`)
			check("RTR-02 vlan99 gateway", dig(r2, "interfaces", "vlan99", "ipv4_config", "routes"), `[{"destination":"0.0.0.0/0","via":"10.193.114.253"}]`)
			check("port 2 circuit kept", dig(r1, "interfaces", "2", "site_wan_interface_ids"), `["ISP 2"]`)
			check("bypass pair 34", dig(r1, "interfaces", "34", "bypass_pair"), `{"lan":"4","use_relay":true,"wan":"3"}`)
			check("vlan1 NAT zone kept", dig(r1, "interfaces", "vlan1", "nat_zone_id"), `"nz-lan"`)
			check("RTR-02 port 1 circuits", dig(r2, "interfaces", "1", "site_wan_interface_ids"), `["ISP 1"]`)
			check("RTR-02 34 circuits", dig(r2, "interfaces", "34", "site_wan_interface_ids"), `["MPLS","LTE"]`)
			check("routes local", dig(r2, "static", "LAN summary", "scope"), `"local"`)
			check("route next hop", dig(r1, "static", "Static Route 3", "nexthops"), `[{"admin_distance":1,"nexthop_interface_id":"vlan4","nexthop_ip":"10.1.4.254"}]`)
			check("syslog source", dig(r2, "syslog"), `[{"id":"e1-sl","name":"siem","server_ip":"10.0.0.5","source_interface":"vlan5"}]`)
			check("probe source", dig(r2, "probe", "source_interface_id"), `"vlan1"`)
			check("BGP peers", keysOf(dig(r1, "peers")), `["core"]`)
			check("toolkit", dig(r1, "toolkit", "ssh_outbound_enabled"), `true`)
			check("software", dig(r1, "record", "software_version"), `"6.3.6-b9"`)
			check("serial", dig(r2, "record", "serial_number"), `"NEW0002"`)
			check("RTR-02 name", dig(r2, "record", "name"), `"S5037-RTR-02"`)
			check("RTR-02 description", dig(r2, "record", "description"), `"S5037 Router 2"`)
			check("HA RTR-01", dig(r1, "record", "spoke_ha_config", "priority"), `150`)
			check("HA RTR-02 track", dig(r2, "record", "spoke_ha_config", "track"), `{"interfaces":[{"interface_id":"5","reduce_priority":100}],"waninterfaces":null}`)
			check("HA source", dig(r2, "record", "spoke_ha_config", "source_interface"), `"vlan99"`)

			zones := asJSON(dig(r2, "zones"))
			if !strings.Contains(zones, `"interface_ids":["vlan1","vlan4","vlan5"]`) || strings.Contains(zones, "AUTO-PRISMA") || strings.Contains(zones, "555") {
				t.Errorf("RTR-02 zones = %s, want RTR-01's LAN zone and no tunnel or stale circuit", zones)
			}
			text := strings.Join(run.lines, "\n")
			for _, want := range []string{"converted S5037 (ion 1200-s) for two ION 1200-S routers", "checks: pass", "removed stale WAN interface 555"} {
				if !strings.Contains(text, want) {
					t.Errorf("the run never said %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "Port 2 WAN interface") {
				t.Errorf("a 1200-S store lost its port 2 circuit, which only an ION 2000's goes:\n%s", text)
			}
		})
	}
}

func TestOnlyION2000AndION1200SStoresAreConverted(t *testing.T) {
	model := func(p *sitePortal, id, model string) {
		for _, e := range p.answers["/sdwan/v3.1/api/elements"].(m)["items"].([]any) {
			if e.(m)["id"] == id {
				e.(m)["model_name"] = model
			}
		}
	}
	tests := []struct {
		name string
		set  func(*sitePortal)
		want string
	}{
		{"another model", func(p *sitePortal) { model(p, "e1", "ion 3000"); model(p, "e2", "ion 3000") },
			"S5037-RTR-01 is an ion 3000; only ion 2000 and ion 1200-s routers are converted"},
		{"one of each", func(p *sitePortal) { model(p, "e2", "ion 1200-s") }, "a site is converted from one model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := storeSite()
			tt.set(p)
			run := migrate(t, p, nil, false)
			if run.err == nil || !strings.Contains(run.err.Error(), tt.want) || len(run.res.Writes) != 0 {
				t.Errorf("err = %v, %d writes, want %q and nothing planned", run.err, len(run.res.Writes), tt.want)
			}
		})
	}
}

func TestDumpTheMigrationForComparison(t *testing.T) {
	dir := os.Getenv("ION1200_DUMP")
	if dir == "" {
		t.Skip("ION1200_DUMP is not set")
	}
	p := storeSite()
	switch os.Getenv("ION1200_VARIANT") {
	case "rtr01-only", "rtr02-only":
		drop := map[string]string{"rtr01-only": "e2", "rtr02-only": "e1"}[os.Getenv("ION1200_VARIANT")]
		var kept []any
		for _, e := range p.answers["/sdwan/v3.1/api/elements"].(m)["items"].([]any) {
			if e.(m)["id"] != drop {
				kept = append(kept, e)
			}
		}
		p.answers["/sdwan/v3.1/api/elements"] = m{"items": kept}
	}
	run := migrate(t, p, nil, false)
	if run.err != nil {
		t.Fatal(run.err)
	}
	a, _ := json.MarshalIndent(p.answers, "", "  ")
	b, _ := json.MarshalIndent(run.plan, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "answers.json"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestADryRunAfterAFailedClaim(t *testing.T) {
	p := storeSite()
	p.answers["/sdwan/v2.5/api/machines"].(m)["items"].([]any)[0].(m)["machine_state"] = "claim_failed"
	els := p.answers["/sdwan/v3.1/api/elements"].(m)
	els["items"] = append(els["items"].([]any), m{"id": "enew1", "serial_number": "NEW0001", "site_id": "1",
		"name": nil, "model_name": "ion 1200-s", "software_version": "0.2.AS5.157", "state": "ready",
		"nat_policysetstack_id": nil, "device_profile_id": nil})
	p.answers["/sdwan/v2.0/api/elements/enew1/software/state"] = m{"id": "ss1", "image_id": "old"}
	p.answers["/sdwan/v2.1/api/elements/enew1/software/status"] = items()
	p.answers["/sdwan/v3.1/api/elements/enew1"] = m{"id": "enew1", "serial_number": "NEW0001", "site_id": "1",
		"_etag": 7, "_schema": 3, "nat_policysetstack_id": nil, "device_profile_id": nil}

	run := migrate(t, p, nil, false)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
	}
	var claimed, upgraded bool
	var assign string
	for _, w := range run.res.Writes {
		claimed = claimed || w.Path == "/sdwan/v2.5/api/machines/m1/operations"
		upgraded = upgraded || w.Path == "/sdwan/v2.0/api/elements/enew1/software/state"
		if w.Path == "/sdwan/v3.1/api/elements/enew1" && assign == "" {
			assign = asJSON(w.Body)
		}
	}
	if !claimed || !upgraded {
		t.Errorf("claim again %v, upgrade the element there is %v", claimed, upgraded)
	}
	for _, want := range []string{`"nat_policysetstack_id":null`, `"device_profile_id":null`, `"_etag":7`, `"_schema":3`} {
		if !strings.Contains(assign, want) {
			t.Errorf("assign left out %s, which the controller wants present: %s", want, assign)
		}
	}
	for _, asked := range p.asked {
		if strings.Contains(asked, "enew1/") && !strings.HasSuffix(asked, "/software/state") && !strings.HasSuffix(asked, "/software/status") {
			t.Errorf("read %s off an element the dry run only planned to assign", asked)
		}
	}
}

func runningSite(version string) *sitePortal {
	p := storeSite()
	p.answers["/sdwan/v2.2/api/element_images"] = items(
		m{"id": "img561", "version": "5.6.1-b3"}, m{"id": "img615", "version": "6.1.5-b2"},
		m{"id": "img621", "version": "6.2.1-b4"}, m{"id": "img625", "version": "6.2.5-b1"},
		m{"id": "img629", "version": "6.2.9-b1", "state": "no-support"}, m{"id": "img634", "version": "6.3.4-b2"},
		m{"id": "img627", "version": "6.2.7-b1", "model_software_map": ls(m{"model": "ion 1000"})},
		m{"id": "img636", "version": "6.3.6-b9"}, m{"id": "img640", "version": "6.4.0-b3"},
	)
	els := p.answers["/sdwan/v3.1/api/elements"].(m)
	for i, serial := range []string{"NEW0001", "NEW0002"} {
		id := fmt.Sprintf("enew%d", i+1)
		el := m{"id": id, "serial_number": serial, "site_id": "1", "name": nil, "model_name": "ion 1200-s",
			"software_version": version, "state": "ready", "connected": true}
		els["items"] = append(els["items"].([]any), el)
		p.answers["/sdwan/v3.1/api/elements/"+id] = el
		p.answers["/sdwan/v2.0/api/elements/"+id+"/software/state"] = m{"id": "ss-" + id, "image_id": "old"}
		p.answers["/sdwan/v2.1/api/elements/"+id+"/software/status"] = items()
	}
	return p
}

func upgradesOf(run migration, el string) string {
	got := []string{}
	for _, w := range run.res.Writes {
		if w.Path == "/sdwan/v2.0/api/elements/"+el+"/software/state" {
			got = append(got, fmt.Sprint(dig(w.Body, "image_id")))
		}
	}
	return asJSON(got)
}

func TestTheUpgradeStepsThroughWhatDoSiteStepsThrough(t *testing.T) {
	tests := []struct {
		name, running, want, said string
	}{
		{"6.1 by way of the newest 6.2 that runs on a 1200-S", "6.1.5-b2", `["img625","img636"]`,
			"NEW0001 runs 6.1.5-b2, so it goes to 6.2.5-b1 and then 6.3.6-b9"},
		{"5.6 by way of 6.1 and 6.2", "5.6.1-b3", `["img615","img625","img636"]`,
			"NEW0001 runs 5.6.1-b3, so it goes to 6.1.5-b2 and then 6.2.5-b1 and then 6.3.6-b9"},
		{"6.2 straight on", "6.2.1-b4", `["img636"]`, "upgrade NEW0001 to 6.3.6-b9"},
		{"a micro step straight on", "6.3.4-b2", `["img636"]`, "upgrade NEW0001 to 6.3.6-b9"},
		{"already there", "6.3.6-b9", `[]`, "NEW0001 already runs 6.3.6-b9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := migrate(t, runningSite(tt.running), nil, false)
			if run.err != nil {
				t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
			}
			if got := upgradesOf(run, "enew1"); got != tt.want {
				t.Errorf("a box on %s is upgraded through %s, want %s: do_site steps through the releases between", tt.running, got, tt.want)
			}
			if text := strings.Join(run.lines, "\n"); !strings.Contains(text, tt.said) {
				t.Errorf("the run never said %q:\n%s", tt.said, text)
			}
		})
	}
}

func TestTheUpgradeRefusesWhatItCannotStepThrough(t *testing.T) {
	tests := []struct {
		name, running, want string
	}{
		{"a newer release", "6.4.0-b3", "NEW0001 runs 6.4.0-b3, newer than 6.3.6-b9; it is not downgraded"},
		{"no image for any step", "5.2.7-b1", "the portal has no image to step NEW0001 up from 5.2.7-b1 towards 6.3.6-b9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := migrate(t, runningSite(tt.running), nil, false)
			if run.err == nil || !strings.Contains(run.err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", run.err, tt.want)
			}
			if got := upgradesOf(run, "enew1"); got != `[]` {
				t.Errorf("planned upgrades %s for a box that cannot be stepped", got)
			}
		})
	}
}

type doitPortal struct {
	*sitePortal
	next    int
	claimed map[string]bool
	bound   map[string]bool
	sent    []string
	bodies  map[string]any
}

func (p *doitPortal) GetJSON(ctx context.Context, path string) ([]byte, error) {
	const v31 = "/sdwan/v3.1/api/elements"
	switch {
	case strings.HasPrefix(path, "/sdwan/v2.5/api/machines/"):
		id := strings.TrimPrefix(path, "/sdwan/v2.5/api/machines/")
		state := "manufactured"
		if p.claimed[id] || id == "m2" {
			state = "claimed"
		}
		return json.Marshal(m{"id": id, "machine_state": state, "connected": true})
	case path == v31:
		var els []any
		for _, mid := range []string{"m1", "m2"} {
			if p.claimed[mid] || mid == "m2" {
				serial := map[string]string{"m1": "NEW0001", "m2": "NEW0002"}[mid]
				els = append(els, m{"id": "el-" + serial, "serial_number": serial, "site_id": "1"})
			}
		}
		base, _ := p.sitePortal.answers[v31].(m)
		els = append(els, base["items"].([]any)...)
		return json.Marshal(items(els...))
	case strings.HasPrefix(path, v31+"/el-"):
		id := strings.TrimPrefix(path, v31+"/")
		state := "ready"
		if p.bound[id] {
			state = "bound"
		}
		return json.Marshal(m{"id": id, "serial_number": strings.TrimPrefix(id, "el-"), "connected": true,
			"state": state, "software_version": "6.2.1-b4", "_etag": 1})
	case strings.HasSuffix(path, "/software/state"):
		return json.Marshal(m{"id": "sw", "image_id": "old", "_etag": 1})
	case strings.HasSuffix(path, "/software/status"):
		return json.Marshal(items(m{"active_image_id": "img636", "_updated_on_utc": 2}))
	case strings.Contains(path, "/sites/new-"):
		switch {
		case strings.HasSuffix(path, "/interfaces"):
			var ifs []any
			for n := 1; n <= 10; n++ {
				ifs = append(ifs, m{"id": fmt.Sprintf("if-%d", n), "name": fmt.Sprint(n), "type": "port", "_etag": 1})
			}
			// a factory ION 1200-S: port 4 on DHCP, vlan 1 already there
			ifs[3].(m)["ipv4_config"] = m{"type": "dhcp"}
			ifs = append(ifs, m{"id": "if-vlan1", "name": "vlan1", "type": "vlan", "_etag": 1})
			return json.Marshal(items(ifs...))
		case strings.HasSuffix(path, "/application_probe"):
			return json.Marshal(m{"id": "probe", "_etag": 1})
		default:
			return json.Marshal(items(m{"id": "singleton", "_etag": 1}))
		}
	case strings.HasSuffix(path, "/ntp") && strings.Contains(path, "/el-"):
		return json.Marshal(items(m{"id": "singleton", "_etag": 1}))
	case strings.HasSuffix(path, "/elementaccessconfigs") && strings.Contains(path, "/el-"):
		return json.Marshal(m{"id": "singleton", "_etag": 1})
	}
	return p.sitePortal.GetJSON(ctx, path)
}

func (p *doitPortal) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	p.sent = append(p.sent, method+" "+path)
	var b any
	_ = json.Unmarshal(body, &b)
	p.bodies[method+" "+path] = b
	switch {
	case strings.HasSuffix(path, "/operations"):
		p.claimed[strings.Split(path, "/")[5]] = true
	case method == "PUT" && strings.HasPrefix(path, "/sdwan/v3.1/api/elements/"):
		if dig(b, "site_id") != nil && dig(b, "site_id") != "1" {
			p.bound[strings.TrimPrefix(path, "/sdwan/v3.1/api/elements/")] = true
		}
	}
	if method == "POST" {
		p.next++
		return json.Marshal(m{"id": fmt.Sprintf("new-%d", p.next)})
	}
	return []byte(`{}`), nil
}

func TestTheMigrationDoesItAgainstAFakeController(t *testing.T) {
	p := &doitPortal{sitePortal: storeSite(), claimed: map[string]bool{}, bound: map[string]bool{}, bodies: map[string]any{}}
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	WorkflowsFolder, WorkflowPaths = filepath.Join(root, "none"), []string{ion1200Dir()}
	t.Cleanup(func() { WorkflowsFolder, WorkflowPaths = "", nil })
	rt := NewRuntime(st, cfg)
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	var lines []string
	c := &runtime.Call{Ctx: context.Background(), DoIt: true, Out: func(s string) { lines = append(lines, s) },
		Args: map[string]string{"site": "S5037", "rtr01_serial": "NEW0001", "rtr02_serial": "NEW0002"}}
	if err := rt.Exec(c, WorkflowPrefix+"ion1200-migrate"); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(lines, "\n"))
	}
	all := strings.Join(p.sent, "\n")
	for _, want := range []string{
		"POST /sdwan/v2.5/api/machines/m1/operations",
		"PUT /sdwan/v2.0/api/elements/el-NEW0001/software/state",
		"POST /sdwan/v4.9/api/sites",
		"PUT /sdwan/v3.1/api/elements/el-NEW0001",
		"PUT /sdwan/v4.17/api/sites/new-2/elements/el-NEW0001/interfaces/if-5",
		"PUT /sdwan/v2.3/api/sites/new-2/elements/el-NEW0001/bgpconfigs/singleton",
		"PUT /sdwan/v2.2/api/elements/el-NEW0002/elementaccessconfigs/singleton",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %s in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "dry-run") {
		t.Errorf("a placeholder reached the portal:\n%s", all)
	}
	ifs := "/sdwan/v4.17/api/sites/new-2/elements/el-NEW0001/interfaces"
	if !strings.Contains(all, "PUT "+ifs+"/if-vlan1") || p.bodies["PUT "+ifs+"/if-vlan1"] == nil {
		t.Errorf("vlan1, which the router has, was not changed in place:\n%s", all)
	}
	for _, w := range p.sent {
		if w == "POST "+ifs && dig(p.bodies[w], "name") == "vlan1" {
			t.Error("vlan1 was posted though the router has it")
		}
	}
	if b, _ := p.bodies["PUT "+ifs+"/if-4"].(map[string]any); b == nil || b["ipv4_config"] != nil || b["used_for"] != "none" {
		t.Errorf("bypass member 4 was not reset to member_port: %s", asJSON(b))
	} else if _, ok := b["ipv4_config"]; !ok {
		t.Errorf("bypass member 4 kept no ipv4_config key: %s", asJSON(b))
	}
	if got := dig(p.bodies["PUT /sdwan/v2.0/api/elements/el-NEW0001/software/state"], "image_id"); got != "img636" {
		t.Errorf("upgrade image = %v", got)
	}
	if got := dig(p.bodies["PUT /sdwan/v4.17/api/sites/new-2/elements/el-NEW0001/interfaces/if-5"], "_etag"); got != 1.0 {
		t.Errorf("port 5's PUT does not carry the portal's _etag: %v", got)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "is built, and disabled") {
		t.Errorf("the run did not finish:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTheMigrationCarriesARelayAndDropsCircuitsTheNewSiteLoses(t *testing.T) {
	p := &doitPortal{sitePortal: storeSite(), claimed: map[string]bool{}, bound: map[string]bool{}, bodies: map[string]any{}}
	p.answers["/sdwan/v2.0/api/sites/s1/sitesecurityzones"] = items(m{"id": "sz1", "zone_id": "gz-wan",
		"networks": ls(m{"network_id": "w3", "network_type": "wan_network"}, m{"network_id": "w1", "network_type": "wan_network"})})
	p.answers["/sdwan/v2.0/api/sites/s1/extensions"] = items(m{"id": "x1", "name": "lte ext", "entity_id": "w3"})
	for _, el := range []string{"e1", "e2"} {
		for _, i := range p.answers["/sdwan/v4.17/api/sites/s1/elements/"+el+"/interfaces"].(m)["items"].([]any) {
			if i.(m)["name"] == "3.4" {
				i.(m)["dhcp_relay"] = m{"source_interface": el + "-34", "server_ips": ls("10.0.0.9")}
			}
		}
	}
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	WorkflowsFolder, WorkflowPaths = filepath.Join(root, "none"), []string{ion1200Dir()}
	t.Cleanup(func() { WorkflowsFolder, WorkflowPaths = "", nil })
	rt := NewRuntime(st, cfg)
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	var lines []string
	c := &runtime.Call{Ctx: context.Background(), DoIt: true, Out: func(s string) { lines = append(lines, s) },
		Args: map[string]string{"site": "S5037", "rtr01_serial": "NEW0001", "rtr02_serial": "NEW0002"}}
	if err := rt.Exec(c, WorkflowPrefix+"ion1200-migrate"); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(lines, "\n"))
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"removed WAN interface LTE from site zone gz-wan", "removed site extension lte ext"} {
		if !strings.Contains(text, want) {
			t.Errorf("no note %q:\n%s", want, text)
		}
	}
	all := strings.Join(p.sent, "\n")
	if _, sent := p.bodies["POST /sdwan/v2.0/api/sites/new-2/extensions"]; sent {
		t.Error("the extension on the dropped circuit was posted")
	}
	zone := asJSON(p.bodies["POST /sdwan/v2.0/api/sites/new-2/sitesecurityzones"])
	if strings.Contains(zone, "LTE") || !strings.Contains(zone, `"network_id":"new-`) {
		t.Errorf("site zone = %s, want only the kept circuit, by its new id", zone)
	}
	ifs := "/sdwan/v4.17/api/sites/new-2/elements/el-NEW0001/interfaces"
	var vlan4 string
	for path, b := range p.bodies {
		if strings.HasPrefix(path, "PUT "+ifs+"/new-") && dig(b, "dhcp_relay", "source_interface") != nil {
			vlan4 = path
			if want := strings.TrimPrefix(path, "PUT "+ifs+"/"); dig(b, "dhcp_relay", "source_interface") != want {
				t.Errorf("vlan4's relay = %v, want its own new id %s", dig(b, "dhcp_relay", "source_interface"), want)
			}
		}
	}
	if vlan4 == "" {
		t.Errorf("no PUT set vlan4's relay to itself after the POST:\n%s", all)
	}
}

type waitingPortal struct {
	*doitPortal
	claiming, upgrading bool
	asked               any
	gets                []string
	created             map[string][]any
	assigned            map[string]m
	writes              []string
	put                 map[string]m
	noSite              bool
}

func (p *waitingPortal) GetJSON(ctx context.Context, path string) ([]byte, error) {
	p.gets = append(p.gets, path)
	b, err := p.read(ctx, path)
	var one m
	if err == nil && json.Unmarshal(b, &one) == nil && one["items"] == nil {
		return p.over(one, path)
	}
	at := strings.LastIndex(path, "/")
	if lb, lerr := p.read(ctx, path[:at]); lerr == nil {
		var list m
		_ = json.Unmarshal(lb, &list)
		items, _ := list["items"].([]any)
		if list["items"] == nil {
			items = ls(map[string]any(list))
		}
		for _, it := range items {
			if rec, ok := it.(map[string]any); ok && fmt.Sprint(rec["id"]) == path[at+1:] {
				return p.over(rec, path)
			}
		}
	}
	return b, err
}

func (p *waitingPortal) over(rec m, path string) ([]byte, error) {
	for k, v := range p.put[path] {
		rec[k] = v
	}
	return json.Marshal(rec)
}

func (p *waitingPortal) read(ctx context.Context, path string) ([]byte, error) {
	b, err := p.answer(ctx, path)
	const elements = "/sdwan/v3.1/api/elements"
	if err != nil || (len(p.created[path]) == 0 && (path != elements || len(p.assigned) == 0)) {
		return b, err
	}
	var v m
	if json.Unmarshal(b, &v) != nil {
		return b, nil
	}
	list, _ := v["items"].([]any)
	if path == elements {
		for _, it := range list {
			if e, ok := it.(map[string]any); ok && p.assigned[fmt.Sprint(e["id"])] != nil {
				b := p.assigned[fmt.Sprint(e["id"])]
				e["site_id"], e["name"] = b["site_id"], b["name"]
			}
		}
	}
	v["items"] = append(list, p.created[path]...)
	return json.Marshal(v)
}

func (p *waitingPortal) answer(ctx context.Context, path string) ([]byte, error) {
	switch {
	case path == "/sdwan/v4.9/api/sites" && !p.noSite:
		b, err := p.doitPortal.GetJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		var v m
		_ = json.Unmarshal(b, &v)
		v["items"] = append(v["items"].([]any), m{"id": "new-1", "name": "S5037-REPL", "admin_state": "disabled"})
		return json.Marshal(v)
	case !p.noSite && strings.HasPrefix(path, "/sdwan/") && strings.Contains(path, "/sites/new-1/") &&
		(strings.HasSuffix(path, "/waninterfaces") || strings.HasSuffix(path, "/lannetworks") || strings.HasSuffix(path, "/spokeclusters")):
		return p.doitPortal.GetJSON(ctx, strings.Replace(path, "/sites/new-1/", "/sites/s1/", 1))
	case path == "/sdwan/v2.5/api/machines" && p.claimed["m1"]:
		state := "claimed"
		if p.claiming {
			state = "claim_pending"
		}
		return json.Marshal(items(
			m{"id": "m1", "sl_no": "NEW0001", "model_name": "ion 1200-s", "machine_state": state, "connected": true},
			m{"id": "m2", "sl_no": "NEW0002", "model_name": "ion 1200-s", "machine_state": "claimed", "connected": true},
		))
	case path == "/sdwan/v2.5/api/machines/m1" && p.claiming && p.claimed["m1"]:
		return json.Marshal(m{"id": "m1", "machine_state": "claim_pending", "connected": true})
	case strings.HasSuffix(path, "/el-NEW0001/software/state") && p.asked != nil:
		return json.Marshal(m{"id": "sw", "image_id": p.asked, "_etag": 1})
	case strings.HasSuffix(path, "/el-NEW0001/software/status") && p.upgrading:
		return json.Marshal(items(m{"active_image_id": "old", "_updated_on_utc": 2}))
	}
	return p.doitPortal.GetJSON(ctx, path)
}

func (p *waitingPortal) Send(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var b m
	_ = json.Unmarshal(body, &b)
	key := string(body)
	if name, ok := b["name"].(string); ok {
		key = name
	}
	p.writes = append(p.writes, method+" "+path+" "+key)
	switch {
	case method == "PUT" && strings.HasSuffix(path, "/el-NEW0001/software/state"):
		p.asked = b["image_id"]
	case method == "PUT" && strings.HasPrefix(path, "/sdwan/v3.1/api/elements/el-"):
		if p.assigned == nil {
			p.assigned = map[string]m{}
		}
		p.assigned[strings.TrimPrefix(path, "/sdwan/v3.1/api/elements/")] = b
	}
	if method == "PUT" {
		if p.put == nil {
			p.put = map[string]m{}
		}
		p.put[path] = b
	}
	out, err := p.doitPortal.Send(ctx, method, path, body)
	if err == nil && method == "POST" && b != nil {
		var r m
		_ = json.Unmarshal(out, &r)
		b["id"] = r["id"]
		if pair, ok := b["bypass_pair"].(map[string]any); ok && b["name"] == nil {
			wan, _ := pair["wan"].(string)
			lan, _ := pair["lan"].(string)
			b["name"] = strings.TrimPrefix(wan, "if-") + strings.TrimPrefix(lan, "if-")
		}
		if p.created == nil {
			p.created = map[string][]any{}
		}
		p.created[path] = append(p.created[path], b)
	}
	return out, err
}

func routerRuntime(t *testing.T, p runtime.Portal, workflows ...string) *runtime.Runtime {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	WorkflowsFolder = filepath.Join(root, "none")
	WorkflowPaths = nil
	for _, w := range workflows {
		WorkflowPaths = append(WorkflowPaths, filepath.Join(filepath.Dir(ion1200Dir()), w))
	}
	t.Cleanup(func() { WorkflowsFolder, WorkflowPaths = "", nil })
	rt := NewRuntime(st, cfg)
	if w := rt.Warnings(); len(w) > 0 {
		t.Fatalf("the workflows did not load: %v", w)
	}
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	return rt
}

type killable struct {
	runtime.Portal
	raw    []string
	failAt int
	tries  int
}

func (p *killable) Send(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	p.tries++
	if p.failAt > 0 && p.tries >= p.failAt {
		return nil, errors.New("the connection dropped")
	}
	p.raw = append(p.raw, method+" "+path+" "+string(body))
	return p.Portal.Send(ctx, method, path, body)
}

func freshWaiting() *waitingPortal {
	return &waitingPortal{doitPortal: &doitPortal{sitePortal: storeSite(), claimed: map[string]bool{},
		bound: map[string]bool{}, bodies: map[string]any{}}}
}

func oneRunWrites(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ion1200Dir(), "dev", "writes.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string][]string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func runFlow(rt *runtime.Runtime, name string, args map[string]string, resume, exit bool) (string, error) {
	var lines []string
	c := &runtime.Call{Ctx: context.Background(), DoIt: true, Resume: resume, ExitOnWait: exit, Args: args,
		Out: func(s string) { lines = append(lines, s) }}
	err := rt.Exec(c, WorkflowPrefix+name)
	return strings.Join(lines, "\n"), err
}

func sameWrites(t *testing.T, what string, got, want []string, out string) {
	t.Helper()
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("%s: write %d of %d (want %d) is\n  %s\nwant\n  %s\n%s", what, i+1, len(got), len(want), g, w, out)
			return
		}
	}
}

var routerArgs = map[string]map[string]string{
	"rtr01": {"site": "S5037", "router": "RTR-01", "serial": "NEW0001"},
	"rtr02": {"site": "S5037", "router": "RTR-02", "serial": "NEW0002"},
}

func TestTheStepsSendWhatTheOneRunSent(t *testing.T) {
	want := oneRunWrites(t)
	site := freshWaiting()
	site.noSite = true
	for _, tt := range []struct {
		key, flow string
		args      map[string]string
		portal    runtime.Portal
	}{
		{"site", "ion1200-migrate-site", map[string]string{"site": "S5037"}, site},
		{"rtr01", "ion1200-migrate-router", routerArgs["rtr01"], freshWaiting()},
		{"rtr02", "ion1200-migrate-router", routerArgs["rtr02"], freshWaiting()},
	} {
		p := &killable{Portal: tt.portal}
		out, err := runFlow(routerRuntime(t, p, tt.flow), tt.flow, tt.args, false, false)
		if err != nil {
			t.Errorf("%s: %v\n%s", tt.key, err, out)
			continue
		}
		sameWrites(t, tt.key, p.raw, want[tt.key], out)
	}
}

func TestARunKilledAtAnyWriteIsFinishedByResume(t *testing.T) {
	want := oneRunWrites(t)
	for _, tt := range []struct {
		key, flow string
		args      map[string]string
		fresh     func() runtime.Portal
	}{
		{"site", "ion1200-migrate-site", map[string]string{"site": "S5037"}, func() runtime.Portal {
			p := freshWaiting()
			p.noSite = true
			return p
		}},
		{"rtr01", "ion1200-migrate-router", routerArgs["rtr01"], func() runtime.Portal { return freshWaiting() }},
		{"rtr02", "ion1200-migrate-router", routerArgs["rtr02"], func() runtime.Portal { return freshWaiting() }},
	} {
		for k := 1; k <= len(want[tt.key]); k++ {
			p := &killable{Portal: tt.fresh(), failAt: k}
			rt := routerRuntime(t, p, tt.flow)
			if out, err := runFlow(rt, tt.flow, tt.args, false, false); err == nil {
				t.Fatalf("%s: a run whose write %d failed went through\n%s", tt.key, k, out)
			}
			p.failAt = 0
			out, err := runFlow(rt, tt.flow, tt.args, true, false)
			if err != nil {
				t.Errorf("%s: after write %d failed, the resumed run: %v\n%s", tt.key, k, err, out)
				continue
			}
			sameWrites(t, fmt.Sprintf("%s killed at write %d", tt.key, k), p.raw, want[tt.key], out)
		}
		t.Logf("%s: killed at each of %d writes and finished by --resume", tt.key, len(want[tt.key]))
	}
}

func TestAResumedRunOfAFinishedRouterSendsNothing(t *testing.T) {
	p := &killable{Portal: freshWaiting()}
	rt := routerRuntime(t, p, "ion1200-migrate-router")
	if out, err := runFlow(rt, "ion1200-migrate-router", routerArgs["rtr01"], false, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sent := len(p.raw)
	out, err := runFlow(rt, "ion1200-migrate-router", routerArgs["rtr01"], true, false)
	if err != nil || len(p.raw) != sent || !strings.Contains(out, "resume: all 10 steps are done") {
		t.Errorf("a resumed run of a finished router sent %d writes: %v\n%s", len(p.raw)-sent, err, out)
	}
}

func TestARouterRunExitsAtEachWaitAndPicksUpFromThePortal(t *testing.T) {
	w := freshWaiting()
	w.claiming, w.upgrading = true, true
	p := &killable{Portal: w}
	rt := routerRuntime(t, p, "ion1200-migrate-router")
	run := func() (string, string) {
		t.Helper()
		out, err := runFlow(rt, "ion1200-migrate-router", routerArgs["rtr01"], true, true)
		var waiting *script.Waiting
		if errors.As(err, &waiting) {
			return waiting.What, out
		}
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return "", out
	}
	count := func(want string) int {
		n := 0
		for _, s := range p.raw {
			if strings.HasPrefix(s, want+" ") {
				n++
			}
		}
		return n
	}
	const claim, upgrade = "POST /sdwan/v2.5/api/machines/m1/operations", "PUT /sdwan/v2.0/api/elements/el-NEW0001/software/state"

	for n := 1; n <= 2; n++ {
		if on, out := run(); on != "the claim of NEW0001" || count(claim) != 1 {
			t.Fatalf("run %d during the claim waits on %q with %d claim(s), want the claim sent once:\n%s", n, on, count(claim), out)
		}
	}
	w.claiming = false
	for n := 1; n <= 2; n++ {
		if on, out := run(); on != "NEW0001's upgrade to 6.3.6-b9" || count(upgrade) != 1 {
			t.Fatalf("run %d during the upgrade waits on %q with %d upgrade(s), want the upgrade sent once:\n%s", n, on, count(upgrade), out)
		}
	}
	w.upgrading = false
	on, out := run()
	if on != "" {
		t.Fatalf("the last run waits on %q:\n%s", on, out)
	}
	sameWrites(t, "a run that exited at each wait", p.raw, oneRunWrites(t)["rtr01"], out)
}

func TestATruncatedRouterRunIsFinishedByResume(t *testing.T) {
	p := &killable{Portal: freshWaiting()}
	rt := routerRuntime(t, p, "ion1200-migrate-router", "ion1200-migrate-router-truncated")
	out, err := runFlow(rt, "ion1200-migrate-router-truncated", routerArgs["rtr01"], false, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cut := len(p.raw)
	out, err = runFlow(rt, "ion1200-migrate-router", routerArgs["rtr01"], true, false)
	if err != nil || !strings.Contains(out, "picking up at step 5, ion1200-interfaces") {
		t.Fatalf("after the cut, the resumed run: %v\n%s", err, out)
	}
	sameWrites(t, "a cut run and its resume", p.raw, oneRunWrites(t)["rtr01"], out)
	t.Logf("cut after %d writes, then --resume sent %d more", cut, len(p.raw)-cut)
}

func TestInterfaceNamesAreTranslatedInNTPAndTheRest(t *testing.T) {
	site := storeSite()
	for _, el := range []string{"e1", "e2"} {
		base := "/sites/s1/elements/" + el
		site.answers["/sdwan/v2.0/api/elements/"+el+"/ntp"] = items(m{"id": el + "-ntp", "name": "default", "source_interface_ids": ls(el + "-35")})
		site.answers["/sdwan/v2.0/api/elements/"+el+"/radii"] = items(m{"id": el + "-rad", "name": "radius", "source_interface_id": el + "-34"})
		site.answers["/sdwan/v2.0/api"+base+"/dnsservices"] = items(m{"id": el + "-dns", "name": "dns",
			"domains_to_interfaces": ls(m{"domain_names": ls("tsc.example"), "interface_id": el + "-31"})})
		site.answers["/sdwan/v2.0/api"+base+"/deviceidconfigs"] = items(m{"id": el + "-did", "name": el + "-35",
			"snmp_discovery_source_interface_id": el + "-ctl"})
	}
	run := migrate(t, site, nil, false)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, strings.Join(run.lines, "\n"))
	}
	for _, name := range []string{"S5037-RTR-01", "S5037-RTR-02"} {
		el := dig(run.plan, "elements", name)
		for _, part := range []string{"ntp", "radii", "dnsservices", "deviceidconfigs"} {
			text := asJSON(dig(el, part))
			if strings.Contains(text, `"3.`) || strings.Contains(text, `"controller"`) || !strings.Contains(text, `"vlan`) {
				t.Errorf("%s %s still names a 2000 interface: %s", name, part, text)
			}
		}
	}

	p := &killable{Portal: &waitingPortal{doitPortal: &doitPortal{sitePortal: site, claimed: map[string]bool{},
		bound: map[string]bool{}, bodies: map[string]any{}}}}
	out, err := runFlow(routerRuntime(t, p, "ion1200-migrate-router"), "ion1200-migrate-router", routerArgs["rtr01"], false, false)
	if err != nil {
		t.Fatalf("a --doit run on a site whose NTP names a sub-interface: %v\n%s", err, out)
	}
	for _, w := range p.raw {
		if strings.Contains(w, "/ntp/") && !strings.Contains(w, `"source_interface_ids":["new-`) {
			t.Errorf("the NTP write does not point at the new VLAN interface: %s", w)
		}
	}
}
