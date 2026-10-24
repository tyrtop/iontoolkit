package siteconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Options struct {
	DoIt  bool
	Say   func(string)
	Every time.Duration
	Limit time.Duration
}

type pusher struct {
	ctx context.Context
	p   Portal
	o   Options

	site       string
	siteName   string
	wan, lan   map[string]string
	cluster    map[string]string
	assigned   map[string]map[string]any
	unassigned map[string]bool
	machines   []any
}

func madeUp(id string) bool { return strings.HasPrefix(id, "dry-run") }

// a dry run has nothing to read for a site or element it only planned
func (s *pusher) planned(el string) bool { return madeUp(el) || madeUp(s.site) || s.unassigned[el] }

// do_site compares before it nulls what an element GET leaves out, sw_obj among them
func unchangedElement(body, cur map[string]any) bool {
	cmp := copyMap(body)
	for k, x := range cmp {
		if _, ok := cur[k]; !ok && x == nil {
			delete(cmp, k)
		}
	}
	return same(cmp, keep(cur, elementPut))
}

func (s *pusher) say(format string, a ...any) { s.o.Say(strings.TrimSpace(fmt.Sprintf(format, a...))) }

func (s *pusher) get(path string, v vars) (map[string]any, error) {
	body, err := s.p.Get(fill(path, v))
	return asMap(body), err
}

func (s *pusher) send(method, path string, body any, v vars) (map[string]any, error) {
	got, err := s.p.Send(method, fill(path, v), body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, fill(path, v), err)
	}
	return asMap(got), nil
}

func (s *pusher) list(path string, v vars) ([]any, error) {
	for _, id := range v {
		if madeUp(id) || s.unassigned[id] {
			return nil, nil
		}
	}
	l, err := list(s.p, path, v)
	if notFound(err) {
		return []any{}, nil
	}
	return l, err
}

func field(want map[string]any, key string) string {
	if v := str(want[key]); v != "" {
		return v
	}
	return str(asMap(want["record"])[key])
}

var pushed = map[string]bool{
	"source": true, "site": true, "waninterfaces": true, "lannetworks": true, "spokeclusters": true,
	"dhcpservers": true, "natlocalprefixes": true, "sitesecurityzones": true, "elements": true,
	"wan_ids": true, "lan_ids": true, "cluster_ids": true, "multicastsources": true, "site_extensions": true,
	"ipfixlocalprefixes": true, "deviceidconfigs": true,
}

var pushedElement = map[string]bool{
	"record": true, "interfaces": true, "static": true, "bgp_global": true, "peers": true, "routing": true,
	"zones": true, "syslog": true, "ntp": true, "probe": true, "toolkit": true, "multicast": true,
	"snmp_agents": true, "serial_number": true, "software_version": true, "model_name": true,
	"deviceidconfigs": true, "radii": true, "multicastrps": true, "dnsservices": true, "ipfix": true,
}

func notEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

func unpushable(model map[string]any) []string {
	var out []string
	for _, k := range names(model) {
		if !pushed[k] && notEmpty(model[k]) {
			out = append(out, k)
		}
	}
	els := asMap(model["elements"])
	for _, n := range names(els) {
		el := asMap(els[n])
		for _, k := range names(el) {
			if !pushedElement[k] && notEmpty(el[k]) {
				out = append(out, n+" "+k)
			}
		}
	}
	return out
}

// the migrate script ran do_site with every timeout at 1200s
const Limit = 1200 * time.Second

func (s *pusher) wait(what string, done func() (bool, error)) error {
	if !s.o.DoIt {
		return nil
	}
	limit := s.o.Limit
	if limit <= 0 {
		limit = Limit
	}
	start := time.Now()
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		if time.Since(start) >= limit {
			return fmt.Errorf("gave up waiting for %s after %s", what, limit)
		}
		s.say("%s: still waiting (%s)", what, time.Since(start).Round(time.Second))
		select {
		case <-s.ctx.Done():
			return fmt.Errorf("%s: %w", what, s.ctx.Err())
		case <-time.After(s.o.Every):
		}
	}
}

func same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func by(key string) func(rec, body map[string]any) bool {
	return func(rec, body map[string]any) bool { return rec[key] != nil && str(rec[key]) == str(body[key]) }
}

// do_site's lookups go in turn: the first rule that matches anything wins
func find(have []any, body map[string]any, rules ...func(rec, body map[string]any) bool) map[string]any {
	for _, rule := range rules {
		for _, h := range have {
			if rec := asMap(h); rule(rec, body) {
				return rec
			}
		}
	}
	return nil
}

func (s *pusher) upsert(what string, have []any, key string, body any, many, one string, v vars) (map[string]any, error) {
	b := asMap(body)
	return s.save(what, str(b[key]), find(have, b, by(key)), b, many, one, v)
}

// do_site's modify_* PUT only what changed; its create_* POST the rest
func (s *pusher) save(what, label string, cur, body map[string]any, many, one string, v vars) (map[string]any, error) {
	if cur == nil {
		s.say("make %s %s", what, label)
		return s.send("POST", many, clean(body), v)
	}
	out := merge(cur, clean(body))
	if same(out, record(cur)) {
		s.say("keep %s %s", what, label)
		return cur, nil
	}
	s.say("update %s %s", what, label)
	at := vars{}
	for k, x := range v {
		at[k] = x
	}
	at["id"] = str(cur["id"])
	got, err := s.send("PUT", one, out, at)
	if err != nil {
		return nil, err
	}
	if got["id"] == nil {
		got["id"] = cur["id"]
	}
	for k, x := range got {
		cur[k] = x
	}
	return got, nil
}

// pull.py strips the name from what it does not key by name, so do_site never sends one
func unnamed(v any) map[string]any {
	out := copyMap(v)
	delete(out, "name")
	return out
}

func resolve(ids map[string]string, name any, what string) (any, error) {
	if name == nil {
		return nil, nil
	}
	id, ok := ids[str(name)]
	if !ok {
		return nil, fmt.Errorf("nothing on the site is %s %s", what, str(name))
	}
	return id, nil
}

// do_site sets a reference only where the config has one
func resolveIn(rec map[string]any, key string, ids map[string]string, what string) error {
	v, ok := rec[key]
	if !ok {
		return nil
	}
	id, err := resolve(ids, v, what)
	rec[key] = id
	return err
}

func resolveAllIn(rec map[string]any, key string, ids map[string]string, what string) error {
	v, ok := rec[key]
	if !ok {
		return nil
	}
	l, err := resolveAll(ids, v, what)
	rec[key] = l
	return err
}

func resolveAll(ids map[string]string, names any, what string) (any, error) {
	l, ok := names.([]any)
	if !ok {
		return names, nil
	}
	out := make([]any, len(l))
	for i, n := range l {
		id, err := resolve(ids, n, what)
		if err != nil {
			return nil, err
		}
		out[i] = id
	}
	return out, nil
}

func swap(v any, ids map[string]string) any {
	switch t := v.(type) {
	case string:
		if id, ok := ids[t]; ok {
			return id
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = swap(e, ids)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = swap(e, ids)
		}
		return out
	}
	return v
}

func Push(ctx context.Context, p Portal, model map[string]any, o Options) error {
	if o.Every <= 0 {
		o.Every = 10 * time.Second
	}
	s := &pusher{ctx: ctx, p: p, o: o, wan: map[string]string{}, lan: map[string]string{},
		cluster: map[string]string{}, assigned: map[string]map[string]any{}, unassigned: map[string]bool{}}
	if left := unpushable(model); len(left) > 0 {
		return fmt.Errorf("site.push does not make %s; nothing was sent", strings.Join(left, ", "))
	}
	els := asMap(model["elements"])
	order := names(els)
	for _, n := range order {
		if _, err := s.machine(field(asMap(els[n]), "serial_number")); err != nil {
			return err
		}
	}
	machines := map[string]map[string]any{}
	for _, n := range order {
		m, err := s.claimStart(asMap(els[n]))
		if err != nil {
			return err
		}
		machines[n] = m
	}
	for _, n := range order {
		if err := s.claimWait(asMap(els[n]), machines[n]); err != nil {
			return err
		}
	}
	target, err := s.siteID(str(asMap(model["site"])["name"]))
	if err != nil {
		return err
	}
	elements := map[string]map[string]any{}
	for _, n := range order {
		el, err := s.elementFor(field(asMap(els[n]), "serial_number"))
		if err != nil {
			return err
		}
		if at := str(el["site_id"]); at != "" && at != "1" && at != target {
			return fmt.Errorf("%s is on another site (%s); it must be unassigned before it can be used", str(el["serial_number"]), at)
		}
		if err := s.elementState(el, "ready", "bound"); err != nil {
			return err
		}
		elements[n] = el
	}
	images := map[string]map[string]any{}
	for _, n := range order {
		img, err := s.upgradeStart(elements[n], field(asMap(els[n]), "software_version"))
		if err != nil {
			return err
		}
		images[n] = img
	}
	if err := s.pushSite(model); err != nil {
		return err
	}
	for _, n := range order {
		want := field(asMap(els[n]), "software_version")
		for img := images[n]; img != nil; {
			if err := s.upgradeWait(elements[n], str(img["version"]), img); err != nil {
				return err
			}
			if !s.o.DoIt {
				break
			}
			was := img
			if img, err = s.upgradeStart(elements[n], want); err != nil {
				return err
			}
			if img != nil && str(img["id"]) == str(was["id"]) {
				return fmt.Errorf("%s still reads as not on %s after upgrading to it", str(elements[n]["serial_number"]), str(was["version"]))
			}
		}
		if err := s.deviceMode(elements[n], asMap(els[n])); err != nil {
			return err
		}
	}
	for _, n := range order {
		if err := s.router(n, elements[n], asMap(els[n])); err != nil {
			return err
		}
	}
	return s.siteState(model)
}

func (s *pusher) siteID(name string) (string, error) {
	all, err := list(s.p, pSites, nil)
	for _, x := range all {
		if str(asMap(x)["name"]) == name {
			return str(asMap(x)["id"]), err
		}
	}
	return "", err
}

// do_site's set_site_state: the site's admin state again once its elements are in
func (s *pusher) siteState(model map[string]any) error {
	want := asMap(model["site"])["admin_state"]
	if want == nil {
		want = "disabled"
	}
	if madeUp(s.site) {
		return nil
	}
	cur, err := s.get(pSite, vars{"site": s.site})
	if err != nil {
		return err
	}
	if str(cur["admin_state"]) == str(want) {
		return nil
	}
	s.say("set %s %s", s.siteName, str(want))
	body := record(cur)
	body["admin_state"] = want
	_, err = s.send("PUT", pSite, body, vars{"site": s.site})
	return err
}

func (s *pusher) deviceMode(el, want map[string]any) error {
	mode := str(asMap(want["record"])["device_mode"])
	id := str(el["id"])
	if mode == "" || madeUp(id) {
		return nil
	}
	cur, err := s.get(pElement, vars{"el": id})
	if err != nil || str(cur["device_mode"]) == mode {
		return err
	}
	s.say("change %s to %s mode", str(el["serial_number"]), mode)
	if _, err := s.send("POST", pElementOperations, map[string]any{"action": "change_device_mode", "parameters": []any{mode}},
		vars{"el": id}); err != nil {
		return err
	}
	return s.wait(str(el["serial_number"])+" to be in "+mode+" mode", func() (bool, error) {
		now, err := s.get(pElement, vars{"el": id})
		return str(now["device_change_mode_state"]) == "element_change_mode_init" || str(now["device_mode"]) == mode, err
	})
}

func (s *pusher) pushSite(model map[string]any) error {
	rec := asMap(model["site"])
	name := str(rec["name"])
	all, err := list(s.p, pSites, nil)
	if err != nil {
		return err
	}
	var found map[string]any
	for _, x := range all {
		if str(asMap(x)["name"]) == name {
			found = asMap(x)
			break
		}
	}
	var made map[string]any
	switch {
	case found != nil && same(merge(found, clean(rec)), record(found)):
		s.say("keep site %s", name)
		made = found
	case found != nil:
		s.say("update site %s", name)
		if made, err = s.send("PUT", pSite, merge(found, clean(rec)), vars{"site": str(found["id"])}); err != nil {
			return err
		}
		if made["id"] == nil {
			made["id"] = found["id"]
		}
	default:
		s.say("make site %s", name)
		if made, err = s.send("POST", pSites, clean(rec), nil); err != nil {
			return err
		}
	}
	s.site, s.siteName = str(made["id"]), name
	v := vars{"site": s.site}

	// do_site matches a WAN interface by its label before its name
	if err := s.named("WAN interface", model["waninterfaces"], pWANInterfaces, pWANInterface, v, s.wan,
		by("label_id"), by("name")); err != nil {
		return err
	}
	if err := s.named("LAN network", model["lannetworks"], pLANNetworks, pLANNetwork, v, s.lan, by("name")); err != nil {
		return err
	}
	if err := s.each("DHCP server", "subnet", model["dhcpservers"], pDHCPServers, pDHCPServer, v); err != nil {
		return err
	}
	if err := s.multicastSources(model, v); err != nil {
		return err
	}
	have, err := s.list(pSiteExtensions, v)
	if err != nil {
		return err
	}
	for _, x := range asList(model["site_extensions"]) {
		body := copyMap(x)
		if e := str(body["entity_id"]); s.wan[e] != "" {
			body["entity_id"] = s.wan[e]
		} else if s.lan[e] != "" {
			body["entity_id"] = s.lan[e]
		}
		if _, err := s.upsert("site extension", have, "name", body, pSiteExtensions, pSiteExtension, v); err != nil {
			return err
		}
	}
	if have, err = s.list(pSiteZones, v); err != nil {
		return err
	}
	for _, z := range asList(model["sitesecurityzones"]) {
		body := unnamed(z)
		for _, n := range asList(body["networks"]) {
			net := asMap(n)
			switch str(net["network_type"]) {
			case "wan_network":
				if id, ok := s.wan[str(net["network_id"])]; ok {
					net["network_id"] = id
				}
			case "lan_network":
				if id, ok := s.lan[str(net["network_id"])]; ok {
					net["network_id"] = id
				}
			}
		}
		if _, err := s.upsert("site security zone", have, "zone_id", body, pSiteZones, pSiteZone, v); err != nil {
			return err
		}
	}
	if err := s.named("spoke cluster", model["spokeclusters"], pSpokeClusters, pSpokeCluster, v, s.cluster, by("name")); err != nil {
		return err
	}
	if err := s.each("NAT local prefix", "prefix_id", model["natlocalprefixes"], pNATLocalPrefixes, pNATLocalPrefix, v); err != nil {
		return err
	}
	if err := s.each("IPFIX local prefix", "prefix_id", model["ipfixlocalprefixes"], pIPFIXPrefixes, pIPFIXPrefix, v); err != nil {
		return err
	}
	return s.deviceID(model, v)
}

func (s *pusher) named(what string, in any, many, one string, v vars, into map[string]string,
	rules ...func(rec, body map[string]any) bool) error {
	have, err := s.list(many, v)
	if err != nil {
		return err
	}
	byName := asMap(in)
	for _, n := range names(byName) {
		body := asMap(byName[n])
		got, err := s.save(what, n, find(have, body, rules...), body, many, one, v)
		if err != nil {
			return err
		}
		into[n] = str(got["id"])
	}
	return nil
}

func (s *pusher) each(what, key string, in any, many, one string, v vars) error {
	have, err := s.list(many, v)
	if err != nil {
		return err
	}
	for _, x := range asList(in) {
		if _, err := s.upsert(what, have, key, unnamed(x), many, one, v); err != nil {
			return err
		}
	}
	return nil
}

// do_site updates whichever multicast source config the site has, else makes one
func (s *pusher) multicastSources(model map[string]any, v vars) error {
	want := asList(model["multicastsources"])
	if len(want) == 0 {
		return nil
	}
	have, err := s.list(pMulticastSources, v)
	if err != nil {
		return err
	}
	var cur map[string]any
	if len(have) > 0 {
		cur = asMap(have[len(have)-1])
	}
	for _, x := range want {
		got, err := s.save("multicast source config", s.siteName, cur, unnamed(x), pMulticastSources, pMulticastSource, v)
		if err != nil {
			return err
		}
		cur = got
	}
	return nil
}

// do_site only ever updates the device ID config the controller made for the site
func (s *pusher) deviceID(model map[string]any, v vars) error {
	want := asList(model["deviceidconfigs"])
	if len(want) == 0 {
		return nil
	}
	body := unnamed(want[0])
	nodes := asList(body["snmpdiscoverystartnodes"])
	delete(body, "snmpdiscoverystartnodes")
	// cloudgenix_config CGSDW-19512: no profile is written as a null one
	if _, ok := body["deviceid_profile_id"]; !ok {
		body["deviceid_profile_id"] = nil
	}
	have, err := s.list(pDeviceIDConfigs, v)
	if err != nil {
		return err
	}
	var cur map[string]any
	switch {
	case len(have) > 0:
		cur = asMap(have[0])
	case madeUp(s.site):
		cur = map[string]any{"id": "dry-run-deviceid-" + s.site}
	case len(nodes) == 0:
		s.say("skip device ID config: %s has none to update", s.siteName)
		return nil
	default:
		return fmt.Errorf("%s has no device ID config to put SNMP discovery start nodes on", s.siteName)
	}
	got, err := s.save("device ID config", s.siteName, cur, body, pDeviceIDConfigs, pDeviceIDConfig, v)
	if err != nil {
		return err
	}
	at := vars{"site": s.site, "dev": str(got["id"])}
	old, err := s.list(pSNMPDiscoveryNodes, at)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, n := range nodes {
		node := asMap(n)
		made, err := s.save("SNMP discovery start node", str(node["name"]), find(old, node, by("name")), node,
			pSNMPDiscoveryNodes, pSNMPDiscoveryNode, at)
		if err != nil {
			return err
		}
		kept[str(made["id"])] = true
	}
	var gone []any
	for _, o := range old {
		if id := str(asMap(o)["id"]); !kept[id] {
			gone = append(gone, map[string]any{"id": id})
		}
	}
	if len(gone) > 0 {
		s.say("delete %d SNMP discovery start nodes", len(gone))
		if _, err := s.send("POST", pSNMPDiscoveryDelete, map[string]any{"start_nodes": gone}, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *pusher) machine(serial string) (map[string]any, error) {
	if s.machines == nil {
		all, err := list(s.p, pMachines, nil)
		if err != nil {
			return nil, err
		}
		s.machines = all
	}
	for _, m := range s.machines {
		if str(asMap(m)["sl_no"]) == serial {
			return asMap(m), nil
		}
	}
	return nil, fmt.Errorf("no ION with serial %s is in this tenant's inventory", serial)
}

func (s *pusher) claimStart(want map[string]any) (map[string]any, error) {
	serial := field(want, "serial_number")
	m, err := s.machine(serial)
	if err != nil {
		return nil, err
	}
	state := strings.ToLower(str(m["machine_state"]))
	if state == "claimed" {
		s.say("%s is claimed already", serial)
		return nil, nil
	}
	switch state {
	case "claim_pending", "manufactured_cic_issued", "manufactured_cic_issue_pending", "manufactured_cic_operational":
		return m, nil
	}
	id := vars{"id": str(m["id"])}
	if err := s.wait(serial+" to come online", func() (bool, error) {
		now, err := s.get(pMachine, id)
		return now["connected"] == true, err
	}); err != nil {
		return nil, err
	}
	s.say("claim %s", serial)
	if _, err := s.send("POST", pMachineOperations, map[string]any{"inventory_op": "claim"}, id); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *pusher) claimWait(want, m map[string]any) error {
	if m == nil {
		return nil
	}
	serial := field(want, "serial_number")
	failed := false
	err := s.wait(serial+" to be claimed", func() (bool, error) {
		now, err := s.get(pMachine, vars{"id": str(m["id"])})
		if err != nil {
			return false, err
		}
		switch strings.ToLower(str(now["machine_state"])) {
		case "claim_failed":
			s.say("%s's claim failed for now (claim_failed)", serial)
			failed = true
		case "claimed":
			return true, nil
		}
		return false, nil
	})
	if err != nil && failed {
		return fmt.Errorf("%w; its last claim failed (claim_failed): check the router, then push again", err)
	}
	return err
}

func (s *pusher) elementFor(serial string) (map[string]any, error) {
	var found map[string]any
	look := func() (bool, error) {
		all, err := list(s.p, pElements, nil)
		for _, e := range all {
			if str(asMap(e)["serial_number"]) == serial {
				found = asMap(e)
				return true, nil
			}
		}
		return false, err
	}
	ok, err := look()
	if err != nil || ok {
		return found, err
	}
	if !s.o.DoIt {
		return map[string]any{"id": "dry-run-element-" + serial, "serial_number": serial, "site_id": "1"}, nil
	}
	return found, s.wait(serial+"'s element to appear", look)
}

func (s *pusher) elementState(el map[string]any, states ...string) error {
	return s.wait(str(el["serial_number"])+" to be "+strings.Join(states, " or "), func() (bool, error) {
		now, err := s.get(pElement, vars{"el": str(el["id"])})
		for _, st := range states {
			if str(now["state"]) == st {
				return true, err
			}
		}
		return false, err
	})
}

// cloudgenix_config's upgrade_path_regex and downgrade_path_regex
var upgradePath = []struct {
	from string
	to   []string
}{
	{`4\.5\..*`, []string{"4.7.1"}}, {`4\.7\..*`, []string{"5.0.3"}}, {`5\.0\..*`, []string{"5.2.7"}},
	{`5\.1\..*`, []string{"5.2.7"}}, {`5\.2\..*`, []string{"5.5..*", "5.4..*", "5.3..*"}},
	{`5\.3\..*`, []string{"5.5..*", "5.4..*"}}, {`5\.4\..*`, []string{"5.6..*", "5.5..*"}},
	{`5\.5\..*`, []string{"6.0..*", "5.6..*"}}, {`5\.6\..*`, []string{"6.1..*", "6.0..*"}},
	{`6\.0\..*`, []string{"6.2..*", "6.1..*"}}, {`6\.1\..*`, []string{"6.2..*", "6.3..*"}},
	{`6\.2\..*`, []string{"6.3..*"}},
}

var downgradePath = []struct {
	from string
	to   []string
}{
	{`4\.7\..*`, []string{"4.5.3"}}, {`5\.0\..*`, []string{"4.7.1"}}, {`5\.1\..*`, []string{"4.7.1"}},
	{`5\.2\..*`, []string{"5.0.3"}}, {`5\.3\..*`, []string{"5.2.7"}}, {`5\.4\..*`, []string{"5.2..*", "5.3..*"}},
	{`5\.5\..*`, []string{"5.2..*", "5.3..*", "5.4..*"}}, {`5\.6\..*`, []string{"5.4..*", "5.5..*"}},
	{`6\.0\..*`, []string{"5.5..*", "5.6..*"}}, {`6\.1\..*`, []string{"5.6..*", "6.0..*"}},
	{`6\.2\..*`, []string{"6.0..*", "6.1..*"}}, {`6\.3\..*`, []string{"6.2..*", "6.1..*"}},
}

var majorMinorRE = regexp.MustCompile(`(\d+)\.(\d+)\..+`)

// compared as text, as major_minor's results are
func majorMinor(v string) string {
	if m := majorMinorRE.FindStringSubmatch(v); m != nil {
		return m[1] + "." + m[2]
	}
	return v
}

func exactVersion(pattern string, versions []string) string {
	re, err := regexp.Compile("^(?:" + pattern + ")")
	if err != nil {
		return ""
	}
	for _, v := range versions {
		if re.MatchString(v) {
			return v
		}
	}
	return ""
}

func nextVersion(active, want string, versions []string) string {
	paths, up := upgradePath, true
	switch {
	case majorMinor(want) > majorMinor(active):
	case majorMinor(want) < majorMinor(active):
		paths, up = downgradePath, false
	default:
		return want
	}
	for _, p := range paths {
		if !regexp.MustCompile("^(?:" + p.from + ")").MatchString(active) {
			continue
		}
		for _, step := range p.to {
			if (up && majorMinor(want) > majorMinor(step)) || (!up && majorMinor(want) < majorMinor(step)) {
				if v := exactVersion(step, versions); v != "" || len(p.to) == 1 {
					return v
				}
				continue
			}
			return want
		}
	}
	// a factory ION 1200 runs 0.2.AS5.x, which the controller takes to a release in one step
	if strings.HasPrefix(active, "0.") {
		return want
	}
	// do_site stops when no upgrade or downgrade path starts at the active version
	return ""
}

func (s *pusher) upgradeStart(el map[string]any, want string) (map[string]any, error) {
	if want == "" {
		return nil, nil
	}
	if str(el["software_version"]) == want {
		s.say("%s runs %s already", str(el["serial_number"]), want)
		return nil, nil
	}
	all, err := list(s.p, pElementImages, nil)
	if err != nil {
		return nil, err
	}
	byID, byVersion := map[string]string{}, map[string]map[string]any{}
	var versions []string
	var target map[string]any
	for _, i := range all {
		img := asMap(i)
		if str(img["state"]) == "no-support" {
			continue
		}
		ver := str(img["version"])
		byID[str(img["id"])], byVersion[ver] = ver, img
		versions = append(versions, ver)
		if strings.EqualFold(ver, want) {
			target = img
		}
	}
	if target == nil {
		sort.Strings(versions)
		return nil, fmt.Errorf("the tenant has no element image %s; it has %s", want, strings.Join(versions, ", "))
	}
	active := str(el["software_version"])
	elID := str(el["id"])
	v := vars{"el": elID}
	if !madeUp(elID) {
		st, err := s.get(pSoftwareStatus, v)
		if err != nil {
			return nil, err
		}
		id := str(st["active_image_id"])
		if latest := newest(asList(st["items"])); id == "" && latest != nil {
			id = str(latest["active_image_id"])
		}
		if ver, ok := byID[id]; ok {
			active = ver
		}
	}
	if active == want {
		s.say("%s runs %s already", str(el["serial_number"]), want)
		return nil, nil
	}
	next := nextVersion(active, want, versions)
	if madeUp(elID) && active == "" {
		next = want
	}
	image := byVersion[next]
	if strings.EqualFold(next, want) {
		image = target
	}
	if next == "" || image == nil {
		return nil, fmt.Errorf("%s runs %s, and the tenant has no image for the next step toward %s", str(el["serial_number"]), active, want)
	}
	state := map[string]any{}
	if !madeUp(elID) {
		got, err := s.get(pSoftwareState, v)
		if err != nil {
			return nil, err
		}
		state = record(got)
	}
	state["image_id"] = image["id"]
	if str(image["version"]) == str(target["version"]) {
		s.say("upgrade %s to %s", str(el["serial_number"]), want)
	} else {
		s.say("upgrade %s to %s on the way to %s", str(el["serial_number"]), str(image["version"]), want)
	}
	if _, err := s.send("PUT", pSoftwareState, state, v); err != nil {
		return nil, err
	}
	return image, nil
}

func (s *pusher) upgradeWait(el map[string]any, want string, image map[string]any) error {
	if image == nil {
		return nil
	}
	v := vars{"el": str(el["id"])}
	return s.wait(str(el["serial_number"])+" to run "+want, func() (bool, error) {
		all, err := list(s.p, pSoftwareStatus, v)
		latest := newest(all)
		return latest != nil && str(latest["active_image_id"]) == str(image["id"]), err
	})
}

// do_site's upgrade wait reads the status updated last
func newest(all []any) map[string]any {
	var latest map[string]any
	at := -1.0
	for _, st := range all {
		when, _ := asMap(st)["_updated_on_utc"].(float64)
		if when > at {
			latest, at = asMap(st), when
		}
	}
	return latest
}

// the controller moves an element's _etag on by itself after claims and upgrades
func (s *pusher) putElement(id string, body map[string]any) error {
	start := time.Now()
	for {
		_, err := s.send("PUT", pElement, body, vars{"el": id})
		if err == nil || !strings.Contains(err.Error(), "INVALID_JSON_COMMON_ATTR_MISMATCH") || time.Since(start) > 600*time.Second {
			return err
		}
		s.say("%s changed under the update; reading its etag again", id)
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(s.o.Every):
		}
		cur, gerr := s.get(pElement, vars{"el": id})
		if gerr != nil {
			return gerr
		}
		body["_etag"] = cur["_etag"]
	}
}

func (s *pusher) assign(el, want map[string]any) error {
	id := str(el["id"])
	cur := el
	if !madeUp(id) {
		got, err := s.get(pElement, vars{"el": id})
		if err != nil {
			return err
		}
		cur = got
	}
	if at := str(cur["site_id"]); at != "" && at != "1" && at != s.site {
		return fmt.Errorf("%s is on another site (%s); it must be unassigned before it can be used", str(el["serial_number"]), at)
	}
	body := merge(keep(cur, elementPut), keep(want["record"], elementPut))
	body["id"], body["_etag"], body["_schema"] = el["id"], cur["_etag"], cur["_schema"]
	body["spoke_ha_config"], body["cluster_id"], body["sw_obj"] = nil, nil, nil
	if str(cur["site_id"]) == s.site && s.site != "" {
		body["spoke_ha_config"] = cur["spoke_ha_config"]
	}
	body["site_id"] = s.site
	if unchangedElement(body, cur) {
		s.say("keep %s on %s as %s", str(el["serial_number"]), s.siteName, str(asMap(want["record"])["name"]))
	} else {
		s.say("assign %s to %s as %s", str(el["serial_number"]), s.siteName, str(asMap(want["record"])["name"]))
		if err := s.putElement(id, body); err != nil {
			return err
		}
	}
	s.assigned[id] = body
	if !s.o.DoIt && str(cur["site_id"]) != s.site {
		s.unassigned[id] = true
	}
	return s.elementState(el, "bound")
}

// cloudgenix_config's member_port
func memberDefault() map[string]any {
	return map[string]any{
		"admin_up": true, "attached_lan_networks": nil, "bound_interfaces": nil, "bypass_pair": nil,
		"cellular_config": nil, "description": nil, "devicemgmt_policysetstack_id": nil, "dhcp_relay": nil,
		"directed_broadcast": false, "ethernet_port": map[string]any{"full_duplex": false, "speed": 0},
		"ipv4_config": nil, "mac_address": nil, "mtu": 1500, "multicast_config": nil, "nat_address": nil,
		"nat_pools": nil, "nat_port": 0, "nat_zone_id": nil, "network_context_id": nil, "parent": nil,
		"pppoe_config": nil, "scope": "local", "service_link_config": nil, "site_wan_interface_ids": nil,
		"secondary_ip_configs": nil, "static_arp_configs": nil, "sub_interface": nil, "tags": nil,
		"type": "port", "used_for": "none", "ipv6_config": nil, "nat_address_v6": nil, "nat_port_v6": 0,
		"ipfixcollectorcontext_id": nil, "ipfixfiltercontext_id": nil,
		"peer_bypasspair_wan_port_type": "none", "vrf_context_id": nil,
	}
}

// cloudgenix_config's modify_interface keeps name, type and what it is built on
func interfaceUpdate(cur, want map[string]any) map[string]any {
	body := clean(want)
	delete(body, "parent_type")
	delete(body, "name")
	out := merge(cur, body)
	for _, k := range []string{"bypass_pair", "sub_interface", "pppoe_config"} {
		if cur[k] != nil && body[k] == nil {
			out[k] = cur[k]
		}
	}
	if cur["parent"] != nil {
		out["parent"] = cur["parent"]
	}
	switch t := str(cur["type"]); t {
	case "subinterface", "pppoe":
		out["mtu"] = 0
		if body["used_for"] == nil {
			out["used_for"] = cur["used_for"]
		}
	case "virtual_interface":
		if body["bound_interfaces"] == nil {
			out["bound_interfaces"] = cur["bound_interfaces"]
		}
	}
	out["name"], out["type"] = cur["name"], cur["type"]
	return out
}

// cloudgenix_config names only these on create; the controller names the rest
var nameable = map[string]bool{"service_link": true, "virtual_interface": true, "vlan": true}

func createBody(rec map[string]any) map[string]any {
	body := clean(rec)
	delete(body, "parent_type")
	if !nameable[str(body["type"])] {
		body["name"] = nil
	}
	return body
}

// do_site's order of interface types
var interfaceRank = map[string]int{"loopback": 1, "cellular": 2, "virtual_interface": 3, "bypasspair": 4,
	"pppoe": 5, "subinterface": 6, "port": 7, "service_link": 8, "vlan": 9, "switch_port": 10}

var creatable = map[string]bool{"pppoe": true, "subinterface": true, "loopback": true, "service_link": true,
	"bypasspair": true, "virtual_interface": true, "vlan": true}

func interfaceOrder(ifs map[string]any) []string {
	rank := func(n string) int {
		if r, ok := interfaceRank[str(asMap(ifs[n])["type"])]; ok {
			return r
		}
		return 11
	}
	out := names(ifs)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// do_site configures a service link's parent; these it only resets
func builtOn(ifs map[string]any) (members, parents map[string]string) {
	members, parents = map[string]string{}, map[string]string{}
	for n, i := range ifs {
		rec := asMap(i)
		if bp, ok := rec["bypass_pair"].(map[string]any); ok {
			for _, k := range []string{"wan", "lan"} {
				if bp[k] != nil {
					members[str(bp[k])] = n
				}
			}
		}
		for _, b := range asList(rec["bound_interfaces"]) {
			if b != nil {
				members[str(b)] = n
			}
		}
		if t := str(rec["type"]); rec["parent"] != nil && (t == "subinterface" || t == "pppoe") {
			parents[str(rec["parent"])] = n
		}
	}
	return members, parents
}

func encodeSecret(body map[string]any) {
	sl, _ := body["service_link_config"].(map[string]any)
	ipsec, _ := sl["ipsec_config"].(map[string]any)
	auth, _ := ipsec["authentication"].(map[string]any)
	// cloudgenix_config CGCBL-533: the portal takes the secret base64-encoded
	if secret := str(auth["secret"]); secret != "" {
		auth["secret"] = base64.StdEncoding.EncodeToString([]byte(secret))
	}
}

var trailing = regexp.MustCompile(`(\d+)$`)

func byNumber(names []string) []string {
	num := func(n string) int {
		var i int
		if m := trailing.FindString(n); m != "" {
			fmt.Sscan(m, &i)
		}
		return i
	}
	sort.SliceStable(names, func(i, j int) bool { return num(names[i]) < num(names[j]) })
	return names
}

// do_site finds controller-named loopbacks, PPPoE, subinterfaces and bypass pairs by order, parent or ports
func alias(ifs map[string]any, all []any, have map[string]map[string]any) {
	var mine, theirs []string
	for _, n := range names(ifs) {
		if str(asMap(ifs[n])["type"]) == "loopback" {
			mine = append(mine, n)
		}
	}
	byID := map[string]map[string]any{}
	for _, i := range all {
		rec := asMap(i)
		byID[str(rec["id"])] = rec
		if str(rec["type"]) == "loopback" {
			theirs = append(theirs, str(rec["name"]))
		}
	}
	mine, theirs = byNumber(mine), byNumber(theirs)
	found := map[string]map[string]any{}
	for k := 0; k < len(mine) && k < len(theirs); k++ {
		found[mine[k]] = have[theirs[k]]
	}
	for _, n := range mine {
		delete(have, n)
	}
	for n, rec := range found {
		have[n] = rec
	}
	idOf := func(n any) string { return str(have[str(n)]["id"]) }
	for _, n := range names(ifs) {
		want := asMap(ifs[n])
		t := str(want["type"])
		if _, ok := have[n]; ok && t != "loopback" {
			continue
		}
		for _, i := range all {
			rec := asMap(i)
			if str(rec["type"]) != t {
				continue
			}
			switch t {
			case "pppoe":
				if want["parent"] != nil && str(rec["parent"]) == idOf(want["parent"]) {
					have[n] = rec
				}
			case "subinterface":
				vlan := asMap(want["sub_interface"])["vlan_id"]
				if want["parent"] != nil && str(rec["parent"]) == idOf(want["parent"]) && vlan != nil &&
					str(asMap(rec["sub_interface"])["vlan_id"]) == str(vlan) {
					have[n] = rec
				}
			case "bypasspair":
				wbp, rbp := asMap(want["bypass_pair"]), asMap(rec["bypass_pair"])
				rn := []rune(n)
				for a, b := 0, len(rn)-1; a < b; a, b = a+1, b-1 {
					rn[a], rn[b] = rn[b], rn[a]
				}
				if str(rec["name"]) == string(rn) || (wbp["wan"] != nil && str(rbp["wan"]) == idOf(wbp["wan"]) &&
					str(rbp["lan"]) == idOf(wbp["lan"])) {
					have[n] = rec
				}
			}
		}
	}
}

func (s *pusher) interfaces(el, want map[string]any, ids map[string]string) error {
	elID, serial, name := str(el["id"]), str(el["serial_number"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	ifs := asMap(want["interfaces"])
	members, parents := builtOn(ifs)
	have := map[string]map[string]any{}
	modules := map[string]string{}
	if s.planned(elID) {
		for n, i := range ifs {
			if t := str(asMap(i)["type"]); !creatable[t] {
				have[n] = map[string]any{"id": "dry-run-if-" + serial + "-" + n, "name": n, "type": t}
			}
		}
		for n := range members {
			have[n] = map[string]any{"id": "dry-run-if-" + serial + "-" + n, "name": n, "type": "port"}
		}
		for n := range parents {
			if _, ok := ifs[n]; !ok {
				have[n] = map[string]any{"id": "dry-run-if-" + serial + "-" + n, "name": n, "type": "port"}
			}
		}
	} else {
		all, err := list(s.p, pInterfaces, v)
		if err != nil {
			return err
		}
		for _, i := range all {
			have[str(asMap(i)["name"])] = asMap(i)
		}
		alias(ifs, all, have)
		mods, err := s.list(pCellularModules, vars{"el": elID})
		if err != nil {
			return err
		}
		for _, m := range mods {
			modules[str(asMap(m)["name"])] = str(asMap(m)["id"])
		}
	}
	for n, i := range have {
		ids[n] = str(i["id"])
	}
	put := func(n string, body map[string]any) (map[string]any, error) {
		at := vars{"site": s.site, "el": elID, "id": str(have[n]["id"])}
		cur := have[n]
		if !madeUp(str(cur["id"])) {
			got, err := s.get(pInterface, at)
			if err != nil {
				return nil, err
			}
			cur = got
		}
		out := interfaceUpdate(cur, body)
		if same(out, record(cur)) {
			s.say("keep %s interface %s", name, n)
			return cur, nil
		}
		s.say("set %s interface %s", name, n)
		got, err := s.send("PUT", pInterface, out, at)
		if err != nil {
			return nil, err
		}
		got["id"] = have[n]["id"]
		return got, nil
	}
	reset := map[string]bool{}
	clear := func(port, forIf string) error {
		if reset[port] {
			return nil
		}
		if _, ok := have[port]; !ok {
			return fmt.Errorf("%s has no interface %s for %s", name, port, forIf)
		}
		reset[port] = true
		cur := have[port]
		if !madeUp(str(cur["id"])) {
			got, err := s.get(pInterface, vars{"site": s.site, "el": elID, "id": str(cur["id"])})
			if err != nil {
				return err
			}
			cur = got
		}
		out := interfaceUpdate(cur, memberDefault())
		if same(out, record(cur)) {
			return nil
		}
		s.say("clear %s interface %s for %s", name, port, forIf)
		_, err := s.send("PUT", pInterface, out, vars{"site": s.site, "el": elID, "id": str(cur["id"])})
		return err
	}
	// cloudgenix_config check_api_subifs_for_parent: only a parent's first subinterface resets it
	hasSubinterface := func(port string) bool {
		id := str(have[port]["id"])
		for _, i := range have {
			if str(i["type"]) == "subinterface" && str(i["parent"]) == id {
				return true
			}
		}
		return false
	}
	for _, n := range interfaceOrder(ifs) {
		body := copyMap(ifs[n])
		kind := str(body["type"])
		var err error
		if err = resolveIn(body, "parent", ids, "an interface called"); err != nil {
			return err
		}
		if bp, ok := body["bypass_pair"].(map[string]any); ok {
			if err = resolveIn(bp, "wan", ids, "an interface called"); err != nil {
				return err
			}
			if err = resolveIn(bp, "lan", ids, "an interface called"); err != nil {
				return err
			}
		}
		if _, ok := body["site_wan_interface_ids"]; ok {
			if err = resolveAllIn(body, "site_wan_interface_ids", s.wan, "a WAN interface called"); err != nil {
				return err
			}
		}
		for _, a := range asList(body["attached_lan_networks"]) {
			att := asMap(a)
			if err = resolveIn(att, "lan_network_id", s.lan, "a LAN network called"); err != nil {
				return err
			}
		}
		if _, ok := body["bound_interfaces"].([]any); ok {
			if err = resolveAllIn(body, "bound_interfaces", ids, "an interface called"); err != nil {
				return err
			}
		}
		if cell, ok := body["cellular_config"].(map[string]any); ok && cell["parent_module_id"] != nil && len(modules) > 0 {
			if err = resolveIn(cell, "parent_module_id", modules, "a cellular module called"); err != nil {
				return err
			}
		}
		var relayLater map[string]any
		if relay, ok := body["dhcp_relay"].(map[string]any); ok && relay["source_interface"] != nil {
			if _, exists := have[n]; str(relay["source_interface"]) == n && !exists {
				relayLater, body["dhcp_relay"] = relay, nil
			} else if err = resolveIn(relay, "source_interface", ids, "an interface called"); err != nil {
				return err
			}
		}
		encodeSecret(body)

		var got map[string]any
		_, exists := have[n]
		switch {
		case (members[n] != "" || (parents[n] != "" && kind != "bypasspair")) && exists:
			s.say("leave %s interface %s to what is built on it", name, n)
			continue
		case exists:
			got, err = put(n, body)
		case !creatable[kind]:
			s.say("skip %s interface %s: there is no %s %s on it, and a %s cannot be made", name, n, kind, n, kind)
			continue
		default:
			switch kind {
			case "bypasspair":
				bp := asMap(asMap(ifs[n])["bypass_pair"])
				for _, m := range []string{str(bp["wan"]), str(bp["lan"])} {
					if err := clear(m, "bypass pair "+n); err != nil {
						return err
					}
				}
			case "virtual_interface":
				for _, m := range asList(asMap(ifs[n])["bound_interfaces"]) {
					if err := clear(str(m), "virtual interface "+n); err != nil {
						return err
					}
				}
			case "subinterface", "pppoe":
				if parent := str(asMap(ifs[n])["parent"]); parent != "" && (kind == "pppoe" || !hasSubinterface(parent)) {
					if err := clear(parent, kind+" "+n); err != nil {
						return err
					}
				}
			}
			s.say("make %s interface %s", name, n)
			if got, err = s.send("POST", pInterfaces, createBody(body), v); err == nil {
				have[n] = got
			}
		}
		if err != nil {
			return err
		}
		ids[n] = str(got["id"])
		if relayLater != nil {
			relayLater["source_interface"] = ids[n]
			body["dhcp_relay"] = relayLater
			s.say("set %s interface %s DHCP relay from itself", name, n)
			if _, err := put(n, body); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *pusher) routing(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	old2new := map[string]string{}
	routing := asMap(want["routing"])
	for _, kind := range []struct{ key, path string }{
		{"aspathaccesslists", pASPathLists}, {"ipcommunitylists", pCommunityLists},
		{"prefixlists", pPrefixLists}, {"routemaps", pRouteMaps},
	} {
		have, err := s.list(kind.path, v)
		if err != nil {
			return err
		}
		for _, r := range asList(routing[kind.key]) {
			rec := asMap(r)
			body := asMap(swap(clean(rec), old2new))
			got, err := s.upsert(name+" "+kind.key, have, "name", body, kind.path, kind.path+"/{id}", v)
			if err != nil {
				return err
			}
			old2new[str(rec["id"])] = str(got["id"])
		}
	}

	global := func(after bool) error {
		g := want["bgp_global"]
		if g == nil {
			return nil
		}
		have, err := s.list(pBGPConfigs, v)
		if err != nil {
			return err
		}
		cur := map[string]any{"id": "dry-run-bgp-" + str(el["serial_number"])}
		switch {
		case len(have) > 0:
			cur = asMap(have[0])
		case !s.planned(elID):
			return fmt.Errorf("%s has no BGP global config to update", name)
		}
		// do_site sets a global config with no AS number after the peers
		if (merge(cur, clean(g))["local_as_num"] == nil) != after {
			return nil
		}
		_, err = s.save(name+" BGP", "global config", cur, unnamed(g), pBGPConfigs, pBGPConfig, v)
		return err
	}
	if err := global(false); err != nil {
		return err
	}
	peersHave, err := s.list(pBGPPeers, v)
	if err != nil {
		return err
	}
	peers := asMap(want["peers"])
	for _, n := range names(peers) {
		body := asMap(swap(clean(peers[n]), old2new))
		cur := find(peersHave, body, by("name"), by("peer_ip"))
		// cloudgenix_config: the controller makes a new core or edge peer's route maps
		if t := str(body["peer_type"]); cur == nil && (t == "core" || t == "edge") {
			body["route_map_in_id"], body["route_map_out_id"] = nil, nil
		}
		if _, err := s.save(name+" BGP peer", n, cur, body, pBGPPeers, pBGPPeer, v); err != nil {
			return err
		}
	}

	have, err := s.list(pStaticRoutes, v)
	if err != nil {
		return err
	}
	static := asMap(want["static"])
	for _, n := range names(static) {
		body := clean(static[n])
		for _, h := range asList(body["nexthops"]) {
			hop := asMap(h)
			if err = resolveIn(hop, "nexthop_interface_id", ifs, "an interface called"); err != nil {
				return err
			}
		}
		cur := find(have, body, by("destination_prefix"))
		if _, err := s.save(name+" static route", n, cur, body, pStaticRoutes, pStaticRoute, v); err != nil {
			return err
		}
	}
	return global(true)
}

// do_site writes a multicast global config only when it finds it by name
func (s *pusher) multicast(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	if len(asList(want["multicast"])) == 0 {
		return nil
	}
	have, err := s.list(pMulticastGlobals, v)
	if err != nil {
		return err
	}
	for _, x := range asList(want["multicast"]) {
		body := asMap(x)
		cur := find(have, body, by("name"))
		if cur == nil && str(body["name"]) == "" {
			s.say("leave %s multicast: the plan's config has no name to find it by", name)
			continue
		}
		if cur == nil {
			s.say("leave %s multicast: none on it is called %q", name, str(body["name"]))
			continue
		}
		if _, err := s.save(name+" multicast", str(body["name"]), cur, body, pMulticastGlobals, pMulticastGlobal, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *pusher) snmp(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	have, err := s.list(pSNMPAgents, v)
	if err != nil {
		return err
	}
	// do_site writes every agent onto the element's first
	var cur map[string]any
	if len(have) > 0 {
		cur = asMap(have[0])
	}
	for _, a := range asList(want["snmp_agents"]) {
		if _, err := s.save(name+" SNMP agent", "", cur, unnamed(a), pSNMPAgents, pSNMPAgent, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *pusher) syslog(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	var have []any
	var err error
	if have, err = s.list(pSyslogServers, v); err != nil {
		return err
	}
	for _, x := range asList(want["syslog"]) {
		body := clean(x)
		// cloudgenix_config CGCBL-516: a profile's server takes no port of its own
		if body["syslog_profile_id"] != nil {
			body["server_port"] = nil
		}
		if err = resolveIn(body, "source_interface", ifs, "an interface called"); err != nil {
			return err
		}
		if _, err := s.upsert(name+" syslog server", have, "name", body, pSyslogServers, pSyslogServer, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *pusher) ntp(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	var err error
	if ntp := want["ntp"]; ntp != nil {
		body := clean(ntp)
		if err = resolveAllIn(body, "source_interface_ids", ifs, "an interface called"); err != nil {
			return err
		}
		have, err := s.list(pNTP, vars{"el": elID})
		if err != nil {
			return err
		}
		cur := map[string]any{"id": "dry-run-ntp-" + str(el["serial_number"])}
		switch {
		case len(have) > 0:
			cur = asMap(have[0])
		case !s.planned(elID):
			return fmt.Errorf("%s has no NTP config to update", name)
		}
		if _, err := s.save(name+" NTP", str(body["name"]), cur, body, pNTP, pNTPOne, vars{"el": elID}); err != nil {
			return err
		}
	}
	return nil
}

// do_site deletes every changed zone before it makes any again
func (s *pusher) zones(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	have, err := s.list(pZones, v)
	if err != nil {
		return err
	}
	var bodies, again, old []map[string]any
	for _, z := range asList(want["zones"]) {
		body := clean(unnamed(z))
		if err = resolveAllIn(body, "interface_ids", ifs, "an interface called"); err != nil {
			return err
		}
		if err = resolveAllIn(body, "waninterface_ids", s.wan, "a WAN interface called"); err != nil {
			return err
		}
		if err = resolveAllIn(body, "lannetwork_ids", s.lan, "a LAN network called"); err != nil {
			return err
		}
		bodies = append(bodies, body)
	}
	for _, body := range bodies {
		cur := find(have, body, by("zone_id"))
		switch {
		case cur == nil:
		case same(merge(cur, body), record(cur)):
			s.say("keep %s zone %s", name, str(body["zone_id"]))
			continue
		default:
			s.say("delete %s zone %s to make it again", name, str(body["zone_id"]))
			if _, err := s.send("DELETE", pZone, nil, vars{"site": s.site, "el": elID, "id": str(cur["id"])}); err != nil {
				return s.putBack(name, append(old, cur), v, err)
			}
		}
		again, old = append(again, body), append(old, cur)
	}
	for i, body := range again {
		if _, err := s.save(name+" zone", str(body["zone_id"]), nil, body, pZones, pZone, v); err != nil {
			return s.missing(name, again[i:], v, err)
		}
	}
	return nil
}

func (s *pusher) gone(zones []map[string]any, v vars) ([]map[string]any, error) {
	now, err := s.list(pZones, v)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, z := range zones {
		if z != nil && find(now, z, by("zone_id")) == nil {
			out = append(out, z)
		}
	}
	return out, nil
}

func zoneIDs(zones []map[string]any) string {
	var ids []string
	for _, z := range zones {
		if z != nil {
			ids = append(ids, str(z["zone_id"]))
		}
	}
	return strings.Join(ids, ", ")
}

// the controller binds an interface to one zone
func (s *pusher) missing(name string, zones []map[string]any, v vars, failed error) error {
	lost, err := s.gone(zones, v)
	if err != nil {
		return fmt.Errorf("%w; the zones could not be read again, so %s zones %s may be missing", failed, name, zoneIDs(zones))
	}
	if len(lost) > 0 {
		return fmt.Errorf("%w; %s has no zone %s until the push runs again", failed, name, zoneIDs(lost))
	}
	return failed
}

func (s *pusher) putBack(name string, zones []map[string]any, v vars, failed error) error {
	lost, err := s.gone(zones, v)
	if err != nil {
		return fmt.Errorf("%w; the zones could not be read again, so %s zones %s may be missing", failed, name, zoneIDs(zones))
	}
	for _, z := range lost {
		s.say("put %s zone %s back as it was", name, str(z["zone_id"]))
		s.send("POST", pZones, clean(z), v)
	}
	return s.missing(name, lost, v, failed)
}

func (s *pusher) toolkit(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	if tk := want["toolkit"]; tk != nil {
		cur := map[string]any{"id": "dry-run-toolkit-" + str(el["serial_number"])}
		if !s.planned(elID) {
			got, err := s.p.Get(fill(pAccessConfigs, vars{"el": elID}))
			if err != nil {
				return err
			}
			if cur = one(got); cur == nil {
				return fmt.Errorf("%s has no toolkit access config to update", name)
			}
		}
		if _, err := s.save(name+" toolkit", "access", cur, unnamed(tk), pAccessConfigs, pAccessConfig, vars{"el": elID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *pusher) probe(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	var err error
	if probe := want["probe"]; probe != nil {
		body := clean(probe)
		if err = resolveIn(body, "source_interface_id", ifs, "an interface called"); err != nil {
			return err
		}
		cur := map[string]any{}
		if !s.planned(elID) {
			got, err := s.get(pAppProbe, v)
			switch {
			case err != nil && probeAbsent(err):
				s.say("skip %s application probe: %s", name, err)
				body = nil
			case err != nil:
				return err
			default:
				cur = got
			}
		}
		if body != nil {
			if _, err := s.save(name+" application probe", str(body["name"]), cur, body, pAppProbe, pAppProbe, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *pusher) spokeHA(el, want map[string]any, ifs map[string]string) error {
	// do_site writes a null spoke HA config when the config has none
	ha, _ := asMap(want["record"])["spoke_ha_config"].(map[string]any)
	if ha != nil {
		ha = copyMap(ha)
	}
	var err error
	if err = resolveIn(ha, "cluster_id", s.cluster, "a spoke cluster called"); err != nil {
		return err
	}
	if err = resolveIn(ha, "source_interface", ifs, "an interface called"); err != nil {
		return err
	}
	if track, ok := ha["track"].(map[string]any); ok {
		for _, t := range asList(track["interfaces"]) {
			tr := asMap(t)
			if err = resolveIn(tr, "interface_id", ifs, "an interface called"); err != nil {
				return err
			}
		}
		for _, t := range asList(track["waninterfaces"]) {
			tr := asMap(t)
			if err = resolveIn(tr, "wan_interface_id", s.wan, "a WAN interface called"); err != nil {
				return err
			}
		}
	}
	id := str(el["id"])
	now := s.assigned[id]
	if !madeUp(id) && !s.unassigned[id] {
		if now, err = s.get(pElement, vars{"el": id}); err != nil {
			return err
		}
	}
	body := keep(now, elementPut)
	body["id"], body["spoke_ha_config"], body["sw_obj"] = el["id"], nil, nil
	if ha != nil {
		body["spoke_ha_config"] = ha
	}
	who := str(asMap(want["record"])["name"])
	switch {
	case unchangedElement(body, now):
		s.say("keep %s spoke HA", who)
		return nil
	case ha == nil:
		s.say("clear %s spoke HA", who)
	default:
		s.say("set %s spoke HA, priority %s", who, str(ha["priority"]))
	}
	return s.putElement(id, body)
}

// cloudgenix_config's name_lookup_in_template: an unknown name goes as it is
func lookup(ids map[string]string, name any) any {
	if id, ok := ids[str(name)]; ok && name != nil {
		return id
	}
	return name
}

// do_site makes an element's device ID config only when it has none
func (s *pusher) elementDeviceID(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	if len(asList(want["deviceidconfigs"])) == 0 {
		return nil
	}
	have, err := s.list(pElementDeviceID, v)
	if err != nil || len(have) > 0 {
		return err
	}
	for _, x := range asList(want["deviceidconfigs"]) {
		body := clean(x)
		body["name"] = lookup(ifs, body["name"])
		body["snmp_discovery_source_interface_id"] = lookup(ifs, body["snmp_discovery_source_interface_id"])
		s.say("make %s device ID config", name)
		if _, err := s.send("POST", pElementDeviceID, body, v); err != nil {
			return err
		}
	}
	return nil
}

// do_site updates the element's one RADIUS config, else makes it
func (s *pusher) radii(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"el": elID}
	if len(asList(want["radii"])) == 0 {
		return nil
	}
	have, err := s.list(pRadii, v)
	if err != nil {
		return err
	}
	var cur map[string]any
	if len(have) > 0 {
		cur = asMap(have[0])
	}
	for _, x := range asList(want["radii"]) {
		body := clean(x)
		body["source_interface_id"] = lookup(ifs, body["source_interface_id"])
		if cur != nil {
			body["name"] = cur["name"]
			if same(merge(cur, body), record(cur)) {
				s.say("keep %s RADIUS %s", name, str(cur["name"]))
				continue
			}
			// the controller never exports RADIUS secrets; cloudgenix_config asks it to keep its own
			for _, r := range asList(body["radius_configuration"]) {
				if srv := asMap(r); srv["shared_secret"] == nil || srv["shared_secret"] == "" {
					srv["shared_secret"], srv["retain_shared_secret"] = "********", "True"
				}
			}
		}
		got, err := s.save(name+" RADIUS", str(body["name"]), cur, body, pRadii, pRadius, v)
		if err != nil {
			return err
		}
		cur = got
	}
	return nil
}

func (s *pusher) multicastRPs(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	if len(asList(want["multicastrps"])) == 0 {
		return nil
	}
	have, err := s.list(pMulticastRPs, v)
	if err != nil {
		return err
	}
	for _, x := range asList(want["multicastrps"]) {
		if _, err := s.upsert(name+" multicast RP", have, "name", x, pMulticastRPs, pMulticastRP, v); err != nil {
			return err
		}
	}
	return nil
}

// do_site takes whichever DNS service the element has, and makes it again if it changed
func (s *pusher) dnsServices(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	if len(asList(want["dnsservices"])) == 0 {
		return nil
	}
	have, err := s.list(pDNSServices, v)
	if err != nil {
		return err
	}
	var cur map[string]any
	if len(have) > 0 {
		cur = asMap(have[0])
	}
	for _, x := range asList(want["dnsservices"]) {
		body := clean(x)
		for _, r := range asList(body["dnsservicerole_bindings"]) {
			for _, i := range asList(asMap(r)["interfaces"]) {
				asMap(i)["interface_id"] = lookup(ifs, asMap(i)["interface_id"])
				delete(asMap(i), "parent_type")
			}
		}
		for _, d := range asList(body["domains_to_interfaces"]) {
			asMap(d)["interface_id"] = lookup(ifs, asMap(d)["interface_id"])
			delete(asMap(d), "parent_type")
		}
		if x := asMap(x); x["element_id"] != nil {
			body["element_id"] = elID
		}
		var old map[string]any
		if cur != nil && !same(merge(cur, body), record(cur)) {
			s.say("delete %s DNS service %s to make it again", name, str(cur["name"]))
			if _, err := s.send("DELETE", pDNSService, nil, vars{"site": s.site, "el": elID, "id": str(cur["id"])}); err != nil {
				return s.dnsBack(name, cur, elID, v, err)
			}
			old, cur = cur, nil
		}
		var got map[string]any
		if cur == nil {
			s.say("make %s DNS service %s", name, str(body["name"]))
			got, err = s.send("POST", pDNSServices, body, v)
		} else {
			got, err = s.save(name+" DNS service", str(body["name"]), cur, body, pDNSServices, pDNSService, v)
		}
		if err != nil && old != nil {
			return s.dnsBack(name, old, elID, v, err)
		}
		if err != nil {
			return err
		}
		cur = got
	}
	return nil
}

// do_site takes an IPFIX config by name, else the element's first
func (s *pusher) ipfix(el, want map[string]any, ifs map[string]string) error {
	elID, name := str(el["id"]), str(asMap(want["record"])["name"])
	v := vars{"site": s.site, "el": elID}
	if len(asList(want["ipfix"])) == 0 {
		return nil
	}
	have, err := s.list(pIPFIX, v)
	if err != nil {
		return err
	}
	for _, x := range asList(want["ipfix"]) {
		body := asMap(x)
		cur := find(have, body, by("name"))
		if cur == nil && len(have) > 0 {
			cur = asMap(have[0])
		}
		if _, err := s.save(name+" IPFIX", str(body["name"]), cur, body, pIPFIX, pIPFIXOne, v); err != nil {
			return err
		}
	}
	return nil
}

// do_site clears spoke HA before interfaces when what it tracks or sources from changes
func (s *pusher) haReset(el, want map[string]any, _ map[string]string) error {
	id := str(el["id"])
	if s.planned(id) {
		return nil
	}
	now, err := s.get(pElement, vars{"el": id})
	if err != nil {
		return err
	}
	cur := asMap(now["spoke_ha_config"])
	if now["spoke_ha_config"] == nil {
		return nil
	}
	if ha, ok := asMap(want["record"])["spoke_ha_config"].(map[string]any); ok {
		all, err := s.list(pInterfaces, vars{"site": s.site, "el": id})
		if err != nil {
			return err
		}
		ids := map[string]string{}
		for _, i := range all {
			ids[str(asMap(i)["name"])] = str(asMap(i)["id"])
		}
		ha = copyMap(ha)
		ha["source_interface"] = lookup(ids, ha["source_interface"])
		if track, ok := ha["track"].(map[string]any); ok {
			for _, t := range asList(track["interfaces"]) {
				asMap(t)["interface_id"] = lookup(ids, asMap(t)["interface_id"])
			}
			for _, t := range asList(track["waninterfaces"]) {
				asMap(t)["wan_interface_id"] = lookup(s.wan, asMap(t)["wan_interface_id"])
			}
		}
		if same(ha["source_interface"], cur["source_interface"]) && same(ha["track"], cur["track"]) {
			return nil
		}
	}
	body := merge(keep(now, elementPut), keep(want["record"], elementPut))
	body["id"], body["_etag"], body["_schema"] = el["id"], now["_etag"], now["_schema"]
	body["spoke_ha_config"], body["cluster_id"], body["sw_obj"], body["site_id"] = nil, nil, nil, s.site
	s.say("clear %s spoke HA before its interfaces change", str(asMap(want["record"])["name"]))
	return s.putElement(id, body)
}

func (s *pusher) dnsBack(name string, old map[string]any, elID string, v vars, failed error) error {
	now, err := s.list(pDNSServices, v)
	switch {
	case err != nil:
		return fmt.Errorf("%w; the DNS services could not be read again, so %s may have none", failed, name)
	case len(now) > 0:
		return failed
	}
	s.say("put %s DNS service %s back as it was", name, str(old["name"]))
	back := clean(old)
	back["element_id"] = elID
	s.send("POST", pDNSServices, back, v)
	switch now, err := s.list(pDNSServices, v); {
	case err != nil:
		return fmt.Errorf("%w; the DNS services could not be read again, so %s may have none", failed, name)
	case len(now) == 0:
		return fmt.Errorf("%w; %s has no DNS service until the push runs again", failed, name)
	}
	return failed
}

func (s *pusher) router(name string, el, want map[string]any) error {
	s.say("-- %s (%s)", name, field(want, "serial_number"))
	if err := s.assign(el, want); err != nil {
		return err
	}
	ifs := map[string]string{}
	for _, step := range []func(el, want map[string]any, ifs map[string]string) error{
		s.haReset, s.interfaces, s.elementDeviceID, s.radii, s.spokeHA, s.routing, s.multicast, s.multicastRPs, s.snmp,
		s.syslog, s.ntp, s.dnsServices, s.ipfix, s.zones, s.toolkit, s.probe,
	} {
		if err := step(el, want, ifs); err != nil {
			return err
		}
	}
	return nil
}
