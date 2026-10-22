package api

import "fmt"

// paths and versions come from GET /sdwan/v2.0/api/permissions, the portal's own
// resource map; the SASE form drops the map's tenant segment, the token carries it
const apiHost = "https://api.sase.paloaltonetworks.com"

const (
	elementsVer      = "v3.2"
	sitesVer         = "v4.13"
	elementStatusVer = "v2.6"
	interfacesVer    = "v4.21"
	ifaceStatusVer   = "v3.9"
	clusterStatusVer = "v2.0"
)

// the two the element list is built from, which a kind: list file writes out
// and overrides; everything else a file names reaches the generic readers
const (
	ElementsPath = "/sdwan/" + elementsVer + "/api/elements"
	SitesPath    = "/sdwan/" + sitesVer + "/api/sites"
)

const scmBase = apiHost + "/sdwan/" + elementsVer + "/api"

// its own version because v3.2/sites does not answer, which left site names raw
const sitesBase = apiHost + "/sdwan/" + sitesVer + "/api"

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

func clusterQueryURL() string {
	return fmt.Sprintf("%s/sdwan/%s/api/spokeclusters/status/query", apiHost, clusterStatusVer)
}

func interfaceStatusURL(siteID, eid, iid string) string {
	return fmt.Sprintf("%s/sdwan/%s/api/sites/%s/elements/%s/interfaces/%s/status",
		apiHost, ifaceStatusVer, siteID, eid, iid)
}
