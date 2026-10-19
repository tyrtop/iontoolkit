package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// Transport answers the SASE REST calls for one tenant. Handing this to the
// real client means the request building, status handling and decoding under
// test are the production ones; only the server is fake.
func (s *Set) Transport(tsg string) http.RoundTripper {
	return &transport{set: s, tsg: tsg}
}

type transport struct {
	set *Set
	tsg string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	switch {
	case strings.HasSuffix(p, "/api/profile"):
		return t.json(req, http.StatusOK, t.profile())

	case strings.HasSuffix(p, "/api/sites"):
		return t.json(req, http.StatusOK, items(t.set.apiSites(t.tsg)))

	case strings.HasSuffix(p, "/api/elements"):
		return t.json(req, http.StatusOK, items(t.set.apiElements(t.tsg)))

	// Circuits. These come before the element routes because a circuit's
	// element-side state lives under /elements/{id}/waninterfaces/...
	case strings.HasSuffix(p, "/api/waninterfacelabels"):
		return t.json(req, http.StatusOK, items(fixtureLabels()))

	case strings.HasSuffix(p, "/api/wannetworks"):
		return t.json(req, http.StatusOK, items(fixtureNetworks()))

	// Security zones in two parts, as the real API has them: the tenant's
	// zone names, and one element's bindings of interfaces to zones.
	case strings.HasSuffix(p, "/api/securityzones"):
		return t.json(req, http.StatusOK, items(fixtureZones()))

	case strings.Contains(p, "/elements/") && strings.HasSuffix(p, "/securityzones"):
		eid := path.Base(strings.TrimSuffix(p, "/securityzones"))
		el := t.element(eid)
		if el == nil {
			return t.noElement(req, eid)
		}
		return t.json(req, http.StatusOK, items(el.apiZoneBindings()))

	case strings.Contains(p, "/waninterfaces"):
		return t.circuits(req, p)

	// The aggregate, which is tenant-wide: one POST for every cluster. Before
	// the per-cluster route below, which its path would also match.
	case strings.HasSuffix(p, "/spokeclusters/status/query"):
		return t.json(req, http.StatusOK, items(t.set.apiClusterStatuses(t.tsg)))

	case strings.Contains(p, "/spokeclusters/") && strings.HasSuffix(p, "/status"):
		cid := path.Base(strings.TrimSuffix(p, "/status"))
		st := t.set.apiClusterStatus(t.tsg, cid)
		if st == nil {
			return t.json(req, http.StatusNotFound,
				map[string]string{"error": "no spoke cluster " + cid + " in this tenant"})
		}
		return t.json(req, http.StatusOK, st)

	// Interfaces and their status hang off the site in the path, so these are
	// matched on the element segment rather than on what the path ends with.
	case strings.Contains(p, "/elements/") && strings.Contains(p, "/interfaces"):
		return t.interfaces(req, p)

	case strings.HasSuffix(p, "/status") && strings.Contains(p, "/api/elements/"):
		eid := path.Base(strings.TrimSuffix(p, "/status"))
		el := t.element(eid)
		if el == nil {
			return t.noElement(req, eid)
		}
		return t.json(req, http.StatusOK, el.apiStatus())

	case strings.Contains(p, "/api/elements/"):
		eid := path.Base(p)
		el := t.element(eid)
		if el == nil {
			return t.noElement(req, eid)
		}
		return t.json(req, http.StatusOK, el.apiElement())
	}

	return t.json(req, http.StatusNotFound,
		map[string]string{"error": fmt.Sprintf("fixture mode serves no %s", p)})
}

// interfaces answers both interface endpoints, whose paths are
// /sites/{site}/elements/{element}/interfaces and that path plus
// /{interface}/status.
func (t *transport) interfaces(req *http.Request, p string) (*http.Response, error) {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	var eid, iid string
	for i, s := range seg {
		switch s {
		case "elements":
			if i+1 < len(seg) {
				eid = seg[i+1]
			}
		case "interfaces":
			if i+1 < len(seg) {
				iid = seg[i+1]
			}
		}
	}

	el := t.element(eid)
	if el == nil {
		return t.noElement(req, eid)
	}
	if iid == "" {
		return t.json(req, http.StatusOK, items(el.apiInterfaces()))
	}
	st := el.apiInterfaceStatus(iid)
	if st == nil {
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": "interface " + iid + " not found on element " + eid})
	}
	return t.json(req, http.StatusOK, st)
}

// circuits answers the three circuit endpoints: the site's list, one
// circuit's status, and one circuit's state as seen by an element.
func (t *transport) circuits(req *http.Request, p string) (*http.Response, error) {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	var eid, cid string
	for i, s := range seg {
		switch s {
		case "elements":
			if i+1 < len(seg) {
				eid = seg[i+1]
			}
		case "waninterfaces":
			if i+1 < len(seg) {
				cid = seg[i+1]
			}
		}
	}

	// The list and the status are the site's, so any element on the site can
	// answer them; the fixture picks the first one it has there.
	el := t.element(eid)
	if el == nil {
		el = t.siteElementFor(p)
	}
	if el == nil {
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": "no element of this tenant on that site"})
	}

	switch {
	case cid == "":
		return t.json(req, http.StatusOK, items(el.apiCircuits()))
	case strings.HasSuffix(p, "/state"):
		if st := el.apiCircuitState(cid); st != nil {
			return t.json(req, http.StatusOK, st)
		}
	case strings.HasSuffix(p, "/status"):
		if st := el.apiCircuitStatus(cid); st != nil {
			return t.json(req, http.StatusOK, st)
		}
	}
	return t.json(req, http.StatusNotFound,
		map[string]string{"error": "no circuit " + cid + " on that site"})
}

// siteElementFor finds any of the tenant's elements on the site in the path,
// for the endpoints that are the site's rather than an element's.
func (t *transport) siteElementFor(p string) *Element {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	site := ""
	for i, s := range seg {
		if s == "sites" && i+1 < len(seg) {
			site = seg[i+1]
		}
	}
	if site == "" {
		return nil
	}
	ten := t.set.tenant(t.tsg)
	if ten == nil {
		return nil
	}
	for i := range ten.Elements {
		if ten.Elements[i].siteID() == site {
			return &ten.Elements[i]
		}
	}
	return nil
}

// element is the tenant's own element, or nil. Outside the tenant is a 404
// too: a tenant's token cannot see another tenant's elements, and a fixture
// that pretended otherwise would hide exactly the bug that looks like a site
// ID mistaken for an element.
func (t *transport) element(eid string) *Element {
	el := t.set.element(eid)
	if el == nil || el.tsg != t.tsg {
		return nil
	}
	return el
}

func (t *transport) noElement(req *http.Request, eid string) (*http.Response, error) {
	return t.json(req, http.StatusNotFound,
		map[string]string{"error": "element " + eid + " not found in this tenant"})
}

// The profile is what proves a token is accepted, so fixture mode has to answer
// it or every tenant looks unreachable.
func (t *transport) profile() map[string]any {
	name := t.tsg
	if ten := t.set.tenant(t.tsg); ten != nil {
		name = ten.Name
	}
	return map[string]any{
		"id":        "fixture",
		"email":     "fixture@example.invalid",
		"name":      name,
		"region":    "fixture",
		"tenant_id": t.tsg,
		"roles":     []map[string]string{{"name": "tenant_super"}},
	}
}

func items[T any](v []T) map[string]any {
	if v == nil {
		v = []T{}
	}
	return map[string]any{"items": v, "count": len(v), "total_count": len(v)}
}

func (t *transport) json(req *http.Request, status int, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(b)),
		ContentLength: int64(len(b)),
		Request:       req,
	}, nil
}
