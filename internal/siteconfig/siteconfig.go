package siteconfig

import (
	"fmt"
	"sort"
	"strings"
)

type Portal interface {
	Get(path string) (any, error)
	Send(method, path string, body any) (any, error)
}

// each path at the version cloudgenix SDK 6.3.1b1 uses
const (
	pSites               = "/sdwan/v4.9/api/sites"
	pSite                = "/sdwan/v4.9/api/sites/{site}"
	pWANInterfaces       = "/sdwan/v2.7/api/sites/{site}/waninterfaces"
	pWANInterface        = "/sdwan/v2.7/api/sites/{site}/waninterfaces/{id}"
	pLANNetworks         = "/sdwan/v3.3/api/sites/{site}/lannetworks"
	pLANNetwork          = "/sdwan/v3.3/api/sites/{site}/lannetworks/{id}"
	pDHCPServers         = "/sdwan/v2.3/api/sites/{site}/dhcpservers"
	pDHCPServer          = "/sdwan/v2.3/api/sites/{site}/dhcpservers/{id}"
	pNATLocalPrefixes    = "/sdwan/v2.0/api/sites/{site}/natlocalprefixes"
	pNATLocalPrefix      = "/sdwan/v2.0/api/sites/{site}/natlocalprefixes/{id}"
	pSpokeClusters       = "/sdwan/v2.0/api/sites/{site}/spokeclusters"
	pSpokeCluster        = "/sdwan/v2.0/api/sites/{site}/spokeclusters/{id}"
	pSiteZones           = "/sdwan/v2.0/api/sites/{site}/sitesecurityzones"
	pSiteZone            = "/sdwan/v2.0/api/sites/{site}/sitesecurityzones/{id}"
	pSiteExtensions      = "/sdwan/v2.0/api/sites/{site}/extensions"
	pSiteExtension       = "/sdwan/v2.0/api/sites/{site}/extensions/{id}"
	pIPFIXPrefixes       = "/sdwan/v2.0/api/sites/{site}/ipfixlocalprefixes"
	pIPFIXPrefix         = "/sdwan/v2.0/api/sites/{site}/ipfixlocalprefixes/{id}"
	pMulticastSources    = "/sdwan/v2.0/api/sites/{site}/multicastsourcesiteconfigs"
	pMulticastSource     = "/sdwan/v2.0/api/sites/{site}/multicastsourcesiteconfigs/{id}"
	pElements            = "/sdwan/v3.1/api/elements"
	pElement             = "/sdwan/v3.1/api/elements/{el}"
	pMachines            = "/sdwan/v2.5/api/machines"
	pMachine             = "/sdwan/v2.5/api/machines/{id}"
	pMachineOperations   = "/sdwan/v2.5/api/machines/{id}/operations"
	pElementImages       = "/sdwan/v2.2/api/element_images"
	pSoftwareState       = "/sdwan/v2.0/api/elements/{el}/software/state"
	pSoftwareStatus      = "/sdwan/v2.1/api/elements/{el}/software/status"
	pInterfaces          = "/sdwan/v4.17/api/sites/{site}/elements/{el}/interfaces"
	pInterface           = "/sdwan/v4.17/api/sites/{site}/elements/{el}/interfaces/{id}"
	pStaticRoutes        = "/sdwan/v2.3/api/sites/{site}/elements/{el}/staticroutes"
	pStaticRoute         = "/sdwan/v2.3/api/sites/{site}/elements/{el}/staticroutes/{id}"
	pBGPConfigs          = "/sdwan/v2.3/api/sites/{site}/elements/{el}/bgpconfigs"
	pBGPConfig           = "/sdwan/v2.3/api/sites/{site}/elements/{el}/bgpconfigs/{id}"
	pBGPPeers            = "/sdwan/v2.4/api/sites/{site}/elements/{el}/bgppeers"
	pBGPPeer             = "/sdwan/v2.4/api/sites/{site}/elements/{el}/bgppeers/{id}"
	pPrefixLists         = "/sdwan/v2.1/api/sites/{site}/elements/{el}/routing_prefixlists"
	pASPathLists         = "/sdwan/v2.1/api/sites/{site}/elements/{el}/routing_aspathaccesslists"
	pCommunityLists      = "/sdwan/v2.0/api/sites/{site}/elements/{el}/routing_ipcommunitylists"
	pRouteMaps           = "/sdwan/v2.2/api/sites/{site}/elements/{el}/routing_routemaps"
	pZones               = "/sdwan/v2.0/api/sites/{site}/elements/{el}/securityzones"
	pZone                = "/sdwan/v2.0/api/sites/{site}/elements/{el}/securityzones/{id}"
	pSyslogServers       = "/sdwan/v2.2/api/sites/{site}/elements/{el}/syslogservers"
	pSyslogServer        = "/sdwan/v2.2/api/sites/{site}/elements/{el}/syslogservers/{id}"
	pSNMPAgents          = "/sdwan/v2.1/api/sites/{site}/elements/{el}/snmpagents"
	pSNMPAgent           = "/sdwan/v2.1/api/sites/{site}/elements/{el}/snmpagents/{id}"
	pSNMPTraps           = "/sdwan/v2.0/api/sites/{site}/elements/{el}/snmptraps"
	pMulticastGlobals    = "/sdwan/v2.1/api/sites/{site}/elements/{el}/multicastglobalconfigs"
	pMulticastGlobal     = "/sdwan/v2.1/api/sites/{site}/elements/{el}/multicastglobalconfigs/{id}"
	pAppProbe            = "/sdwan/v2.0/api/sites/{site}/elements/{el}/application_probe"
	pElementExtensions   = "/sdwan/v2.0/api/sites/{site}/elements/{el}/extensions"
	pDNSServices         = "/sdwan/v2.0/api/sites/{site}/elements/{el}/dnsservices"
	pNTP                 = "/sdwan/v2.0/api/elements/{el}/ntp"
	pNTPOne              = "/sdwan/v2.0/api/elements/{el}/ntp/{id}"
	pRadii               = "/sdwan/v2.0/api/elements/{el}/radii"
	pAccessConfigs       = "/sdwan/v2.2/api/elements/{el}/elementaccessconfigs"
	pAccessConfig        = "/sdwan/v2.2/api/elements/{el}/elementaccessconfigs/{id}"
	pWANNetworks         = "/sdwan/v2.1/api/wannetworks"
	pHubClusters         = "/sdwan/v4.0/api/sites/{site}/hubclusters"
	pDeviceIDConfigs     = "/sdwan/v2.1/api/sites/{site}/deviceidconfigs"
	pDeviceIDConfig      = "/sdwan/v2.1/api/sites/{site}/deviceidconfigs/{id}"
	pSNMPDiscoveryNodes  = "/sdwan/v2.0/api/sites/{site}/deviceidconfigs/{dev}/snmpdiscoverystartnodes"
	pSNMPDiscoveryNode   = "/sdwan/v2.0/api/sites/{site}/deviceidconfigs/{dev}/snmpdiscoverystartnodes/{id}"
	pSNMPDiscoveryDelete = "/sdwan/v2.0/api/sites/{site}/deviceidconfigs/{dev}/bulkdelete_snmpdiscoverystartnodes"
	pCellularModules     = "/sdwan/v2.0/api/elements/{el}/cellular_modules"
	pSIMSecurity         = "/sdwan/v2.0/api/elements/{el}/cellular_modules/{id}/sim_security"
	pMulticastRPs        = "/sdwan/v2.0/api/sites/{site}/elements/{el}/multicastrps"
	pIPFIX               = "/sdwan/v2.0/api/sites/{site}/elements/{el}/ipfix"
	pElementDeviceID     = "/sdwan/v2.0/api/sites/{site}/elements/{el}/deviceidconfigs"
	pMulticastRP         = "/sdwan/v2.0/api/sites/{site}/elements/{el}/multicastrps/{id}"
	pDNSService          = "/sdwan/v2.0/api/sites/{site}/elements/{el}/dnsservices/{id}"
	pIPFIXOne            = "/sdwan/v2.0/api/sites/{site}/elements/{el}/ipfix/{id}"
	pRadius              = "/sdwan/v2.0/api/elements/{el}/radii/{id}"
	pElementOperations   = "/sdwan/v2.0/api/elements/{el}/operations"
)

// cloudgenix_config's element_put_items
var elementPut = []string{
	"_etag", "_schema", "cluster_insertion_mode", "cluster_id", "description", "id",
	"l3_direct_private_wan_forwarding", "l3_lan_forwarding", "name",
	"nat_policysetstack_id", "network_policysetstack_id", "priority_policysetstack_id",
	"site_id", "spoke_ha_config", "sw_obj", "tags", "vpn_to_vpn_forwarding",
	"switch_config", "device_profile_id", "main_power_usage_threshold", "led_config",
}

// the API client's errors read "<what>: status 404 Not Found: <body>"
func notFound(err error) bool { return err != nil && strings.Contains(err.Error(), "status 404") }

type vars map[string]string

func fill(path string, v vars) string {
	for k, s := range v {
		path = strings.ReplaceAll(path, "{"+k+"}", s)
	}
	return path
}

func list(p Portal, path string, v vars) ([]any, error) {
	body, err := p.Get(fill(path, v))
	if err != nil {
		return nil, err
	}
	if m, ok := body.(map[string]any); ok {
		if items, ok := m["items"].([]any); ok {
			return items, nil
		}
	}
	return []any{}, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func one(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if items, ok := m["items"].([]any); ok {
		if len(items) == 0 {
			return nil
		}
		return asMap(items[0])
	}
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		if s == float64(int64(s)) {
			return fmt.Sprintf("%d", int64(s))
		}
	}
	return fmt.Sprint(v)
}

func names(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

func copyMap(v any) map[string]any { return asMap(deepCopy(v)) }

func clean(v any) map[string]any {
	out := copyMap(v)
	for k := range out {
		if strings.HasPrefix(k, "_") {
			delete(out, k)
		}
	}
	delete(out, "id")
	delete(out, "site_id")
	delete(out, "element_id")
	return out
}

// the controller checks _etag and _schema on an update
func record(v any) map[string]any {
	out := copyMap(v)
	for k := range out {
		if strings.HasPrefix(k, "_") && k != "_etag" && k != "_schema" {
			delete(out, k)
		}
	}
	return out
}

func merge(have, want any) map[string]any {
	out := record(have)
	for k, v := range asMap(want) {
		out[k] = deepCopy(v)
	}
	return out
}

func keep(rec any, fields []string) map[string]any {
	in := asMap(rec)
	out := map[string]any{}
	for _, f := range fields {
		if v, ok := in[f]; ok {
			out[f] = deepCopy(v)
		}
	}
	return out
}
