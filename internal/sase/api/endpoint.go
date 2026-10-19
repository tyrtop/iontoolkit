package api

import "fmt"

// Paths and versions come from the portal's own resource map, which it fetches
// from GET /sdwan/v2.0/api/permissions: resource_uri_map gives each resource a
// template of the form /{ver}/api/tenants/{tsg}/<rest> and
// resource_version_map lists the versions the server accepts. The SASE form
// drops the tenant segment -- the token carries it -- so it is
// /sdwan/{ver}/api/<rest>, with monitor resources under /sdwan/monitor. The
// toolkit socket iontk already dials against hardware is that same
// derivation, which is what makes the rest of this table credible; the
// versions below are the highest each map offered, confirmed on pan.dev.
const apiHost = "https://api.sase.paloaltonetworks.com"

const (
	elementsVer      = "v3.2"
	sitesVer         = "v4.13"
	elementStatusVer = "v2.6"
	interfacesVer    = "v4.21"
	ifaceStatusVer   = "v3.9"
	clusterStatusVer = "v2.0"
)

const scmBase = apiHost + "/sdwan/" + elementsVer + "/api"

// Sites are on their own version. The portal's own traffic asks v4.13 for them
// while using v3.2 for elements, and v3.2/sites does not answer, which left
// every site name unresolved and showing a raw id instead.
const sitesBase = apiHost + "/sdwan/" + sitesVer + "/api"

// ElementURL is exported for the one caller that wants to label a raw body
// with where it came from.
func ElementURL(eid string) string {
	return scmBase + "/elements/" + eid
}

func elementStatusURL(eid string) string {
	return fmt.Sprintf("%s/sdwan/%s/api/elements/%s/status", apiHost, elementStatusVer, eid)
}

func interfacesURL(siteID, eid string) string {
	return fmt.Sprintf("%s/sdwan/%s/api/sites/%s/elements/%s/interfaces",
		apiHost, interfacesVer, siteID, eid)
}

func clusterStatusURL(siteID, cid string) string {
	return fmt.Sprintf("%s/sdwan/%s/api/sites/%s/spokeclusters/%s/status",
		apiHost, clusterStatusVer, siteID, cid)
}

func interfaceStatusURL(siteID, eid, iid string) string {
	return fmt.Sprintf("%s/sdwan/%s/api/sites/%s/elements/%s/interfaces/%s/status",
		apiHost, ifaceStatusVer, siteID, eid, iid)
}
