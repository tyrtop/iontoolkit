package siteconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/script"
)

type m = map[string]any

func items(v ...any) m { return m{"items": v} }

type fake struct {
	answers map[string]any
	sent    []string
}

func (f *fake) GetJSON(_ context.Context, path string) ([]byte, error) {
	v, ok := f.answers[path]
	if !ok && strings.HasSuffix(path, "/application_probe") {
		return nil, errors.New(`400: {"_error":[{"code":"APPLICATION_PROBE_CONFIG_NOT_PRESENT"}]}`)
	}
	if !ok {
		return nil, errors.New(path + ": status 404 Not Found")
	}
	return json.Marshal(v)
}

func (f *fake) Send(_ context.Context, method, path string, _ []byte) ([]byte, error) {
	f.sent = append(f.sent, method+" "+path)
	return []byte(`{"id":"new"}`), nil
}

type adapter struct{ p script.Portal }

func (a adapter) Get(path string) (any, error) { return a.p.Get()(path, nil) }
func (a adapter) Send(method, path string, body any) (any, error) {
	return a.p.Send()(method, path, body, nil)
}

func site() *fake {
	el := "/sdwan/v4.17/api/sites/s1/elements/e1"
	f := &fake{answers: map[string]any{
		"/sdwan/v4.9/api/sites":                               items(m{"id": "s1", "name": "S1", "admin_state": "active", "_etag": 3.0, "_request_id": "r"}),
		"/sdwan/v4.9/api/sites/s1":                            m{"id": "s1", "name": "S1", "admin_state": "active", "_etag": 3.0},
		"/sdwan/v2.7/api/sites/s1/waninterfaces":              items(m{"id": "w1", "name": "ISP", "_etag": 1.0}),
		"/sdwan/v3.3/api/sites/s1/lannetworks":                items(),
		"/sdwan/v2.0/api/sites/s1/spokeclusters":              items(m{"id": "c1", "name": "HA"}),
		"/sdwan/v2.3/api/sites/s1/dhcpservers":                items(m{"id": "d1", "subnet": "10.0.0.0/24", "_etag": 2.0}),
		"/sdwan/v2.0/api/sites/s1/natlocalprefixes":           items(),
		"/sdwan/v2.0/api/sites/s1/sitesecurityzones":          items(),
		"/sdwan/v2.0/api/sites/s1/extensions":                 items(),
		"/sdwan/v2.0/api/sites/s1/ipfixlocalprefixes":         items(),
		"/sdwan/v2.0/api/sites/s1/multicastsourcesiteconfigs": items(),
		"/sdwan/v3.1/api/elements": items(m{"id": "e1", "name": "R1", "site_id": "s1", "serial_number": "SER1",
			"software_version": "6.3.6", "nat_policysetstack_id": nil,
			"spoke_ha_config": m{"cluster_id": "c1", "source_interface": "i2", "priority": 150.0,
				"track": m{"interfaces": []any{m{"interface_id": "i1"}}, "waninterfaces": nil}}}),
		el + "/interfaces": items(
			m{"id": "i1", "name": "1", "type": "port", "site_wan_interface_ids": []any{"w1"}},
			m{"id": "i2", "name": "2", "type": "port"},
			m{"id": "i3", "name": "3", "type": "port"},
			m{"id": "i4", "name": "4", "type": "port"},
			m{"id": "i34", "name": "34", "type": "bypasspair", "parent": nil, "bypass_pair": m{"wan": "i3", "lan": "i4"}},
			m{"id": "iv5", "name": "vlan5", "type": "vlan", "parent": nil},
		),
		"/sdwan/v2.3/api/sites/s1/elements/e1/staticroutes": items(m{"id": "r1", "name": "default",
			"nexthops": []any{m{"nexthop_interface_id": "i1"}}}),
		"/sdwan/v2.3/api/sites/s1/elements/e1/bgpconfigs":                items(),
		"/sdwan/v2.4/api/sites/s1/elements/e1/bgppeers":                  items(),
		"/sdwan/v2.1/api/sites/s1/elements/e1/routing_prefixlists":       items(),
		"/sdwan/v2.1/api/sites/s1/elements/e1/routing_aspathaccesslists": items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/routing_ipcommunitylists":  items(),
		"/sdwan/v2.2/api/sites/s1/elements/e1/routing_routemaps":         items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/securityzones": items(m{"id": "z1", "zone_id": "gz",
			"interface_ids": []any{"i2"}, "waninterface_ids": []any{"w1"}}),
		"/sdwan/v2.2/api/sites/s1/elements/e1/syslogservers":          items(),
		"/sdwan/v2.1/api/sites/s1/elements/e1/snmpagents":             items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/snmptraps":              items(),
		"/sdwan/v2.1/api/sites/s1/elements/e1/multicastglobalconfigs": items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/extensions":             items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/dnsservices":            items(),
		"/sdwan/v2.0/api/elements/e1/ntp":                             items(),
		"/sdwan/v2.0/api/elements/e1/radii":                           items(),
		"/sdwan/v2.2/api/elements/e1/elementaccessconfigs":            items(),
		"/sdwan/v2.5/api/machines": items(
			m{"id": "m1", "sl_no": "SER1", "machine_state": "claimed"},
			m{"id": "m2", "sl_no": "SER2", "machine_state": "allocated", "connected": true},
			m{"id": "m3", "sl_no": "SER3", "machine_state": "claim_pending"}),
		"/sdwan/v2.5/api/machines/m3":                                   m{"id": "m3", "machine_state": "claim_failed"},
		"/sdwan/v2.2/api/element_images":                                items(m{"id": "img1", "version": "6.3.6"}),
		"/sdwan/v2.1/api/wannetworks":                                   items(m{"id": "n1", "name": "Internet"}),
		"/sdwan/v4.0/api/sites/s1/hubclusters":                          items(),
		"/sdwan/v2.1/api/sites/s1/deviceidconfigs":                      items(),
		"/sdwan/v2.0/api/elements/e1/cellular_modules":                  items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/multicastrps":             items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/ipfix":                    items(),
		"/sdwan/v2.0/api/sites/s1/elements/e1/deviceidconfigs":          items(),
		"/sdwan/v2.1/api/elements/dry-run-element-SER2/software/status": items(),
	}}
	return f
}

func portal(f *fake, doit bool) (adapter, *[]script.Write) {
	var writes []script.Write
	return adapter{script.Portal{Ctx: context.Background(), Client: f, DoIt: doit, Planned: &writes}}, &writes
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestPullWritesEveryReferenceAsAName(t *testing.T) {
	f := site()
	p, _ := portal(f, false)
	model, err := Pull(p, "S1", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	r1 := asMap(asMap(model["elements"])["R1"])
	ifs := asMap(r1["interfaces"])
	for _, c := range []struct {
		want string
		got  any
	}{
		{`["ISP"]`, asMap(ifs["1"])["site_wan_interface_ids"]},
		{`{"lan":"4","wan":"3"}`, asMap(ifs["34"])["bypass_pair"]},
		{`"HA"`, asMap(asMap(r1["record"])["spoke_ha_config"])["cluster_id"]},
		{`"2"`, asMap(asMap(r1["record"])["spoke_ha_config"])["source_interface"]},
		{`[{"nexthop_interface_id":"1"}]`, asMap(asMap(r1["static"])["default"])["nexthops"]},
		{`["ISP"]`, asMap(asList(r1["zones"])[0])["waninterface_ids"]},
	} {
		if asJSON(c.got) != c.want {
			t.Errorf("got %s, want %s", asJSON(c.got), c.want)
		}
	}
	if _, ok := asMap(ifs["2"])["site_wan_interface_ids"]; ok {
		t.Error("a field the record never had was written in")
	}

	if _, err := Pull(p, "S9", func(string) {}); err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Errorf("a missing site: %v", err)
	}
}

func newSite(t *testing.T, f *fake, serial string) map[string]any {
	t.Helper()
	p, _ := portal(f, false)
	model, err := Pull(p, "S1", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	asMap(model["site"])["name"] = "S1-NEW"
	r1 := asMap(asMap(model["elements"])["R1"])
	r1["serial_number"], r1["software_version"] = serial, "6.3.6"
	return model
}

func TestADryRunPushListsEveryWriteInDoSitesOrder(t *testing.T) {
	f := site()
	model := newSite(t, f, "SER2")
	p, writes := portal(f, false)
	var said []string
	if err := Push(context.Background(), p, model, Options{Say: func(l string) { said = append(said, l) }}); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(said, "\n"))
	}
	if len(f.sent) != 0 {
		t.Errorf("a dry run sent %v", f.sent)
	}
	var seq []string
	for _, w := range *writes {
		seq = append(seq, w.Method+" "+w.Path)
	}
	all := strings.Join(seq, "\n")
	at := 0
	for _, want := range []string{
		"POST /sdwan/v2.5/api/machines/m2/operations",
		"PUT /sdwan/v2.0/api/elements/dry-run-element-SER2/software/state",
		"POST /sdwan/v4.9/api/sites",
		"POST /sdwan/v2.7/api/sites/dry-run-",
		"PUT /sdwan/v3.1/api/elements/dry-run-element-SER2",
		"/interfaces/dry-run-if-SER2-3",
		"/interfaces/dry-run-if-SER2-4",
		"/elements/dry-run-element-SER2/interfaces\n",
		"/interfaces/dry-run-if-SER2-1",
		"PUT /sdwan/v3.1/api/elements/dry-run-element-SER2",
		"/staticroutes",
		"/securityzones",
		"/elementaccessconfigs/",
	} {
		i := strings.Index(all[at:]+"\n", want)
		if i < 0 {
			t.Fatalf("no %q after %d:\n%s", want, at, all)
		}
		at += i + len(want) - 1
	}
	for _, w := range *writes {
		if w.Method == "POST" && (asMap(w.Body)["id"] != nil || asMap(w.Body)["_etag"] != nil) {
			t.Errorf("%s carries the old site's id or etag: %s", w.Path, asJSON(w.Body))
		}
	}
	for _, w := range *writes {
		if b := asMap(w.Body); w.Method == "POST" && b["type"] == "vlan" && b["name"] != "vlan5" {
			t.Errorf("a new VLAN lost its name: %s", asJSON(b))
		}
		if b := asMap(w.Body); w.Method == "POST" && strings.HasSuffix(w.Path, "/interfaces") && b["type"] == "bypasspair" {
			if name, has := b["name"]; !has || name != nil {
				t.Errorf("a new bypass pair went with a name: %s", asJSON(b))
			}
		}
	}
	text := strings.Join(said, "\n")
	if strings.Contains(text, "leave R1 interface 3") {
		t.Errorf("pull.py drops a bypass pair's ports, so there is nothing to leave:\n%s", text)
	}
	for _, want := range []string{"clear R1 interface 3 for bypass pair 34", "make site S1-NEW"} {
		if !strings.Contains(text, want) {
			t.Errorf("never said %q:\n%s", want, text)
		}
	}
}

func TestPushUpdatesWhatTheSiteHasByItsKey(t *testing.T) {
	f := site()
	p, _ := portal(f, false)
	model, err := Pull(p, "S1", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	model["elements"] = m{}
	asMap(model["site"])["description"] = "moved"
	asMap(asMap(model["waninterfaces"])["ISP"])["link_bw_down"] = 50.0
	asMap(asList(model["dhcpservers"])[0])["gateway"] = "10.0.0.1"
	p, writes := portal(f, false)
	var said []string
	if err := Push(context.Background(), p, model, Options{Say: func(l string) { said = append(said, l) }}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range *writes {
		got[w.Method+" "+w.Path] = asJSON(w.Body)
	}
	for _, want := range []string{"PUT /sdwan/v4.9/api/sites/s1", "PUT /sdwan/v2.7/api/sites/s1/waninterfaces/w1",
		"PUT /sdwan/v2.3/api/sites/s1/dhcpservers/d1"} {
		if _, ok := got[want]; !ok {
			t.Errorf("no %s in %v", want, got)
		}
	}
	if body := got["PUT /sdwan/v4.9/api/sites/s1"]; !strings.Contains(body, `"_etag":3`) || strings.Contains(body, "_request_id") {
		t.Errorf("the site update = %s, want its etag and none of the response's own fields", body)
	}
	if len(got) != 3 {
		t.Errorf("writes %v, want only what changed, and nothing made twice", got)
	}
	if text := strings.Join(said, "\n"); !strings.Contains(text, "keep spoke cluster HA") {
		t.Errorf("an unchanged spoke cluster was not kept:\n%s", text)
	}
}

func TestAFailedClaimStopsThePush(t *testing.T) {
	f := site()
	model := newSite(t, f, "SER3")
	p, _ := portal(f, true)
	err := Push(context.Background(), p, model, Options{DoIt: true, Every: time.Millisecond, Limit: 20 * time.Millisecond, Say: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "claim_failed") || len(f.sent) != 0 {
		t.Errorf("Push() = %v, sent %v", err, f.sent)
	}
}

type scripted struct {
	gets  map[string]any
	fails map[string][]error
	sent  []string
}

func (s *scripted) Get(path string) (any, error) {
	if v, ok := s.gets[path]; ok {
		return v, nil
	}
	return nil, errors.New(path + ": status 404 Not Found")
}

func (s *scripted) Send(method, path string, body any) (any, error) {
	s.sent = append(s.sent, method+" "+path+" "+asJSON(body))
	if errs := s.fails[path]; len(errs) > 0 {
		s.fails[path] = errs[1:]
		return nil, errs[0]
	}
	if l, ok := asMap(s.gets[path])["items"].([]any); ok && method == "POST" {
		rec := copyMap(body)
		rec["id"] = fmt.Sprintf("made%d", len(s.sent))
		s.gets[path] = items(append(l, rec)...)
	}
	if i := strings.LastIndex(path, "/"); method == "DELETE" && i > 0 {
		if l, ok := asMap(s.gets[path[:i]])["items"].([]any); ok {
			var kept []any
			for _, x := range l {
				if str(asMap(x)["id"]) != path[i+1:] {
					kept = append(kept, x)
				}
			}
			s.gets[path[:i]] = items(kept...)
		}
	}
	return body, nil
}

func TestAnElementUpdateRetriesAnEtagTheControllerMovedOn(t *testing.T) {
	p := &scripted{
		gets:  map[string]any{"/sdwan/v3.1/api/elements/e1": m{"id": "e1", "_etag": 9.0}},
		fails: map[string][]error{"/sdwan/v3.1/api/elements/e1": {errors.New(`400: {"code":"INVALID_JSON_COMMON_ATTR_MISMATCH"}`)}},
	}
	s := &pusher{ctx: context.Background(), p: p, o: Options{DoIt: true, Every: time.Millisecond, Say: func(string) {}}}
	if err := s.putElement("e1", m{"id": "e1", "_etag": 1.0}); err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 2 || !strings.Contains(p.sent[1], `"_etag":9`) {
		t.Errorf("sent %v, want the second try with the etag read again", p.sent)
	}

	p.fails["/sdwan/v3.1/api/elements/e1"] = []error{errors.New("400: INTERFACE_CONFIG_INVALID")}
	if err := s.putElement("e1", m{"id": "e1"}); err == nil {
		t.Error("an error that is not an etag mismatch was retried")
	}
}

func TestAnInterfaceUpdateKeepsWhatTheControllerSaysItIs(t *testing.T) {
	cur := m{"id": "i5", "name": "5", "type": "switch_port", "_etag": 4.0, "parent": "i9",
		"bypass_pair": m{"wan": "i3", "lan": "i4"}, "description": "old"}
	got := interfaceUpdate(cur, m{"id": "old", "name": "five", "type": "port", "parent": nil,
		"bypass_pair": nil, "description": "new", "parent_type": "bypasspair_34"})
	want := `{"_etag":4,"bypass_pair":{"lan":"i4","wan":"i3"},"description":"new","id":"i5","name":"5","parent":"i9","type":"switch_port"}`
	if asJSON(got) != want {
		t.Errorf("update = %s\nwant     %s", asJSON(got), want)
	}
	sub := interfaceUpdate(m{"name": "1.10", "type": "subinterface", "used_for": "lan", "mtu": 1500.0}, m{"mtu": 9000})
	if sub["mtu"] != 0 || sub["used_for"] != "lan" {
		t.Errorf("subinterface update = %s", asJSON(sub))
	}
}

func TestTheToolkitIsOneRecordEitherWay(t *testing.T) {
	if r := one(m{"id": "t1", "ssh_outbound_enabled": false}); r["id"] != "t1" {
		t.Errorf("a single record: %v", r)
	}
	if r := one(items(m{"id": "t2"})); r["id"] != "t2" {
		t.Errorf("a list of one: %v", r)
	}
	if one(items()) != nil {
		t.Error("an empty list read as a record")
	}
}

func TestCoreAndEdgePeersGoWithoutRouteMaps(t *testing.T) {
	p := &scripted{gets: map[string]any{}}
	s := &pusher{ctx: context.Background(), p: p, o: Options{Say: func(string) {}}, site: "dry-run-1", unassigned: map[string]bool{}}
	want := m{"record": m{"name": "R1"}, "routing": m{}, "peers": m{
		"core": m{"name": "core", "peer_type": "core", "route_map_in_id": "rm1"},
		"cust": m{"name": "cust", "peer_type": "classic", "route_map_in_id": "rm1"},
	}}
	if err := s.routing(m{"id": "dry-run-element-X", "serial_number": "X"}, want, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.sent, "\n")
	if !strings.Contains(text, `"name":"core","peer_type":"core","route_map_in_id":null,"route_map_out_id":null`) ||
		!strings.Contains(text, `"name":"cust","peer_type":"classic","route_map_in_id":"rm1"`) {
		t.Errorf("sent:\n%s", text)
	}
}

func TestASyslogServerOnAProfileGoesWithoutAPort(t *testing.T) {
	p := &scripted{gets: map[string]any{}}
	s := &pusher{ctx: context.Background(), p: p, o: Options{Say: func(string) {}}, site: "dry-run-1", unassigned: map[string]bool{}}
	want := m{"record": m{"name": "R1"}, "syslog": []any{
		m{"name": "QRadar", "server_port": 0.0, "syslog_profile_id": "sp1", "source_interface": nil},
		m{"name": "plain", "server_port": 514.0, "syslog_profile_id": nil, "source_interface": nil},
	}}
	if err := s.syslog(m{"id": "dry-run-element-X", "serial_number": "X"}, want, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.sent, "\n")
	if !strings.Contains(text, `"name":"QRadar","server_port":null`) || !strings.Contains(text, `"name":"plain","server_port":514`) {
		t.Errorf("sent:\n%s", text)
	}
}

func TestNamesComeOutAsCheckNameHasThem(t *testing.T) {
	n := nameCheck{}
	got := []string{n.check("ISP", "Waninterface"), n.check("ISP", "Waninterface"), n.check("", "Static Route"),
		n.check("ISP", "Waninterface"), n.check("", "Static Route")}
	if strings.Join(got, "|") != "ISP|ISP 2|Static Route 1|ISP 3|Static Route 2" {
		t.Errorf("names = %v", got)
	}
}

func TestTheUpgradePathIsWalkedAsDoSiteWalksIt(t *testing.T) {
	images := []string{"5.6.9-b1", "6.1.4-b2", "6.2.5-b3", "6.3.6-b9"}
	for _, c := range []struct{ from, want, next string }{
		{"5.5.3-b1", "6.3.6-b9", "5.6.9-b1"},
		{"6.1.4-b2", "6.3.6-b9", "6.2.5-b3"},
		{"6.2.5-b3", "6.3.6-b9", "6.3.6-b9"},
		{"0.2.AS5.157", "6.3.6-b9", "6.3.6-b9"},
		{"6.3.1-b2", "6.4.1-b1", ""},
		{"6.3.6-b9", "6.1.4-b2", "6.2.5-b3"},
	} {
		if got := nextVersion(c.from, c.want, images); got != c.next {
			t.Errorf("from %s to %s: next %s, want %s", c.from, c.want, got, c.next)
		}
	}
}

func TestInterfacesGoInDoSitesOrderAndAServiceLinkParentIsConfigured(t *testing.T) {
	ifs := m{
		"5":       m{"type": "switch_port"},
		"vlan10":  m{"type": "vlan"},
		"1":       m{"type": "port"},
		"1.20":    m{"type": "subinterface", "parent": "1"},
		"2":       m{"type": "port"},
		"PA-SL":   m{"type": "service_link", "parent": "2"},
		"34":      m{"type": "bypasspair", "bypass_pair": m{"wan": "3", "lan": "4"}},
		"lo1":     m{"type": "loopback"},
		"pppoe-1": m{"type": "pppoe", "parent": "6"},
	}
	if got := strings.Join(interfaceOrder(ifs), " "); got != "lo1 34 pppoe-1 1.20 1 2 PA-SL vlan10 5" {
		t.Errorf("order = %s", got)
	}
	members, parents := builtOn(ifs)
	if members["3"] != "34" || members["4"] != "34" || parents["1"] != "1.20" || parents["6"] != "pppoe-1" {
		t.Errorf("members %v parents %v", members, parents)
	}
	if _, ok := parents["2"]; ok {
		t.Error("a service link's parent was left unconfigured")
	}
}

func TestAPushRefusesWhatItCannotMake(t *testing.T) {
	model := m{"site": m{"name": "S1"}, "hubclusters": []any{m{"name": "dc"}}, "site_extensions": []any{},
		"elements": m{"R1": m{"record": m{}, "snmp_traps": []any{m{"id": "t"}}, "dnsservices": []any{}}}}
	p := &scripted{gets: map[string]any{}}
	err := Push(context.Background(), p, model, Options{Say: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "hubclusters, R1 snmp_traps") || len(p.sent) != 0 {
		t.Errorf("Push() = %v, sent %v", err, p.sent)
	}
}

func TestPullDropsGeneratedRoutingAndLoopbackCircuits(t *testing.T) {
	f := site()
	base := "/sdwan/v4.17/api/sites/s1/elements/e1"
	ifs := f.answers[base+"/interfaces"].(m)["items"].([]any)
	f.answers[base+"/interfaces"] = items(append(ifs, m{"id": "il", "name": "lo1", "type": "loopback", "site_wan_interface_ids": []any{"w1"}})...)
	f.answers["/sdwan/v2.2/api/sites/s1/elements/e1/routing_routemaps"] = items(
		m{"id": "rm-auto", "name": "AUTO", "auto_generated": true},
		m{"id": "rm1", "name": "mine", "route_map_entries": []any{m{"match": m{"ip_prefix_list_id": "pl-auto"}}}})
	f.answers["/sdwan/v2.1/api/sites/s1/elements/e1/routing_prefixlists"] = items(m{"id": "pl-auto", "name": "P", "auto_generated": true})
	f.answers["/sdwan/v2.4/api/sites/s1/elements/e1/bgppeers"] = items(m{"id": "b1", "name": "core", "route_map_in_id": "rm-auto"})
	p, _ := portal(f, false)
	model, err := Pull(p, "S1", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	r1 := asMap(asMap(model["elements"])["R1"])
	if lo := asMap(asMap(r1["interfaces"])["lo1"]); lo["site_wan_interface_ids"] != nil {
		t.Errorf("a loopback kept its circuits: %v", lo)
	}
	routing := asMap(r1["routing"])
	if len(asList(routing["routemaps"])) != 1 || len(asList(routing["prefixlists"])) != 0 {
		t.Errorf("routing = %s", asJSON(routing))
	}
	if got := asJSON(asList(routing["routemaps"])[0]); !strings.Contains(got, `"ip_prefix_list_id":null`) {
		t.Errorf("a route map still names a generated prefix list: %s", got)
	}
	if peer := asMap(asMap(r1["peers"])["core"]); peer["route_map_in_id"] != nil {
		t.Errorf("a peer still names a generated route map: %v", peer)
	}
}

func TestTheSiteDeviceIDConfigIsUpdatedAsDoSiteDoes(t *testing.T) {
	dev := "/sdwan/v2.1/api/sites/s9/deviceidconfigs"
	nodes := "/sdwan/v2.0/api/sites/s9/deviceidconfigs/d1/snmpdiscoverystartnodes"
	p := &scripted{gets: map[string]any{
		dev:   items(m{"id": "d1", "name": "S9 devices", "config_enabled": false, "_etag": 1.0}),
		nodes: items(m{"id": "n1", "name": "core", "ipv4_address": "10.0.0.1"}, m{"id": "n2", "name": "old"}),
	}}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", siteName: "S9", o: Options{DoIt: true, Say: func(string) {}}}
	model := m{"deviceidconfigs": []any{m{"id": "d0", "name": "S1 devices", "config_enabled": true,
		"snmpdiscoverystartnodes": []any{m{"name": "core", "ipv4_address": "10.0.0.2"}, m{"name": "new", "ipv4_address": "10.0.0.3"}}}}}
	if err := s.deviceID(model, vars{"site": "s9"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PUT ` + dev + `/d1 {"_etag":1,"config_enabled":true,"deviceid_profile_id":null,"id":"d1","name":"S9 devices"}`,
		`PUT ` + nodes + `/n1 {"id":"n1","ipv4_address":"10.0.0.2","name":"core"}`,
		`POST ` + nodes + ` {"ipv4_address":"10.0.0.3","name":"new"}`,
		`POST /sdwan/v2.0/api/sites/s9/deviceidconfigs/d1/bulkdelete_snmpdiscoverystartnodes {"start_nodes":[{"id":"n2"}]}`,
	}
	if got := strings.Join(p.sent, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("sent:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestAnElementsRoutingIsMatchedAsDoSiteMatchesIt(t *testing.T) {
	base := "/sites/s9/elements/e1"
	p := &scripted{gets: map[string]any{
		"/sdwan/v2.3/api" + base + "/staticroutes":           items(m{"id": "r1", "name": "other", "destination_prefix": "10.0.0.0/8", "_etag": 1.0}),
		"/sdwan/v2.4/api" + base + "/bgppeers":               items(m{"id": "p1", "name": "old name", "peer_ip": "10.1.1.1", "peer_type": "core", "_etag": 1.0}),
		"/sdwan/v2.3/api" + base + "/bgpconfigs":             items(m{"id": "g1", "local_as_num": "65001"}),
		"/sdwan/v2.1/api" + base + "/multicastglobalconfigs": items(m{"id": "mc1", "name": "default", "dr_priority": 2.0}),
	}}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", o: Options{DoIt: true, Say: func(string) {}}}
	want := m{"record": m{"name": "R1"}, "bgp_global": m{"name": "x", "local_as_num": "65001"},
		"peers":     m{"core": m{"name": "core", "peer_ip": "10.1.1.1", "peer_type": "core", "route_map_in_id": "rm9"}},
		"static":    m{"LAN": m{"name": "LAN", "destination_prefix": "10.0.0.0/8"}},
		"multicast": []any{m{"dr_priority": 1.0}}}
	el := m{"id": "e1", "serial_number": "SER"}
	for _, step := range []func(el, want map[string]any, ifs map[string]string) error{s.routing, s.multicast} {
		if err := step(el, want, map[string]string{}); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(p.sent, "\n")
	for _, w := range []string{"PUT /sdwan/v2.4/api" + base + "/bgppeers/p1 ", `"route_map_in_id":"rm9"`,
		"PUT /sdwan/v2.3/api" + base + "/staticroutes/r1 "} {
		if !strings.Contains(got, w) {
			t.Errorf("no %s in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "POST") || strings.Contains(got, "bgpconfigs") || strings.Contains(got, "multicast") {
		t.Errorf("made, or wrote what had not changed or was not found by name:\n%s", got)
	}
}

func TestAnElementThatIsAlreadySoIsNotWrittenAgain(t *testing.T) {
	cur := m{"id": "e1", "name": "R1", "site_id": "s9", "_etag": 4.0, "description": "Router 1", "spoke_ha_config": nil}
	p := &scripted{gets: map[string]any{"/sdwan/v3.1/api/elements/e1": cur}}
	p.gets["/sdwan/v3.1/api/elements/e1"] = m{"id": "e1", "name": "R1", "site_id": "s9", "_etag": 4.0, "description": "Router 1",
		"spoke_ha_config": nil, "state": "bound"}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", siteName: "S9", assigned: map[string]map[string]any{},
		unassigned: map[string]bool{}, o: Options{DoIt: true, Every: time.Millisecond, Say: func(string) {}}}
	if err := s.assign(m{"id": "e1", "serial_number": "SER"}, m{"record": m{"name": "R1", "description": "Router 1"}}); err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 0 {
		t.Errorf("an unchanged element was written: %v", p.sent)
	}
}

func TestAZoneDeletedToBeMadeAgainIsPutBackWhenALaterWriteFails(t *testing.T) {
	zones := "/sdwan/v2.0/api/sites/s9/elements/e1/securityzones"
	p := &scripted{gets: map[string]any{zones: items(m{"id": "z1", "zone_id": "a", "interface_ids": []any{"i1"}},
		m{"id": "z2", "zone_id": "b", "interface_ids": []any{"i2"}})},
		fails: map[string][]error{zones + "/z2": {errors.New("400 no")}}}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", o: Options{DoIt: true, Say: func(string) {}}}
	want := m{"record": m{"name": "R1"}, "zones": []any{m{"zone_id": "a", "interface_ids": []any{"1"}}, m{"zone_id": "b", "interface_ids": []any{"1"}}}}
	err := s.zones(m{"id": "e1"}, want, map[string]string{"1": "i9"})
	got := strings.Join(p.sent, "\n")
	if err == nil || !strings.Contains(got, "POST "+zones+` {"interface_ids":["i1"],"zone_id":"a"}`) || strings.Contains(err.Error(), "no zone") {
		t.Errorf("err %v, sent %d:\n%s", err, len(p.sent), got)
	}
}

func TestAZoneTheModelDoesNotChangeIsKept(t *testing.T) {
	zones := "/sdwan/v2.0/api/sites/s9/elements/e1/securityzones"
	p := &scripted{gets: map[string]any{zones: items(m{"id": "z1", "zone_id": "a", "interface_ids": []any{"i9"}, "_etag": 1.0})}}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", o: Options{DoIt: true, Say: func(string) {}}}
	want := m{"record": m{"name": "R1"}, "zones": []any{m{"zone_id": "a", "interface_ids": []any{"1"}}}}
	if err := s.zones(m{"id": "e1"}, want, map[string]string{"1": "i9"}); err != nil || len(p.sent) != 0 {
		t.Errorf("err %v, sent %v", err, p.sent)
	}
}

func TestSpokeHAIsClearedWhenTheModelHasNone(t *testing.T) {
	el := "/sdwan/v3.1/api/elements/e1"
	p := &scripted{gets: map[string]any{el: m{"id": "e1", "name": "R1", "site_id": "s9", "_etag": 2.0, "spoke_ha_config": m{"cluster_id": "c1"}}}}
	s := &pusher{ctx: context.Background(), p: p, site: "s9", o: Options{DoIt: true, Say: func(string) {}}}
	if err := s.spokeHA(m{"id": "e1"}, m{"record": m{"name": "R1"}}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], `"spoke_ha_config":null`) {
		t.Errorf("sent %v", p.sent)
	}
}

func TestLoopbacksPairByOrderAlone(t *testing.T) {
	ifs := map[string]any{"loopback1": m{"type": "loopback"}, "loopback2": m{"type": "loopback"}}
	all := []any{m{"id": "L2", "name": "loopback2", "type": "loopback"}}
	have := map[string]map[string]any{"loopback2": asMap(all[0])}
	alias(ifs, all, have)
	if str(have["loopback1"]["id"]) != "L2" || have["loopback2"] != nil {
		t.Errorf("have = %v; want loopback1 on L2 and loopback2 made", have)
	}
}

func TestSpokeHAIsClearedBeforeInterfacesOnlyWhenItWouldChange(t *testing.T) {
	el := "/sdwan/v3.1/api/elements/e1"
	ifs := "/sdwan/v4.17/api/sites/s9/elements/e1/interfaces"
	for _, c := range []struct {
		source string
		writes int
	}{{"vlan99", 0}, {"vlan5", 1}} {
		p := &scripted{gets: map[string]any{
			el:  m{"id": "e1", "name": "R1", "site_id": "s9", "_etag": 2.0, "spoke_ha_config": m{"source_interface": "i99", "track": nil}},
			ifs: items(m{"id": "i99", "name": "vlan99"}, m{"id": "i5", "name": "vlan5"}),
		}}
		s := &pusher{ctx: context.Background(), p: p, site: "s9", o: Options{DoIt: true, Say: func(string) {}}}
		want := m{"record": m{"name": "R1", "spoke_ha_config": m{"source_interface": c.source, "track": nil}}}
		if err := s.haReset(m{"id": "e1"}, want, nil); err != nil {
			t.Fatal(err)
		}
		if len(p.sent) != c.writes || (c.writes == 1 && !strings.Contains(p.sent[0], `"spoke_ha_config":null`)) {
			t.Errorf("source %s: sent %v", c.source, p.sent)
		}
	}
}
