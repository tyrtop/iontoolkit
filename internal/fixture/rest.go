package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"tyrtop.com/iontk/internal/sase/api"
)

func (s *Set) Transport(tsg string) http.RoundTripper {
	return &transport{set: s, tsg: tsg}
}

type transport struct {
	set *Set
	tsg string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	switch req.Method {
	case http.MethodPut, http.MethodPatch:
		if strings.Contains(p, "/elements/") && strings.Contains(p, "/interfaces/") {
			return t.editInterface(req, p)
		}
		return t.refuseWrite(req, p)
	case http.MethodDelete:
		return t.refuseWrite(req, p)
	}

	switch {
	case strings.HasSuffix(p, "/api/profile"):
		return t.json(req, http.StatusOK, t.profile())

	case strings.HasSuffix(p, "/api/sites"):
		return t.json(req, http.StatusOK, items(t.set.apiSites(t.tsg)))

	case strings.HasSuffix(p, "/api/elements"):
		return t.json(req, http.StatusOK, items(t.set.apiElements(t.tsg)))

	case strings.HasSuffix(p, "/api/waninterfacelabels"):
		return t.json(req, http.StatusOK, items(fixtureLabels()))

	case strings.HasSuffix(p, "/api/wannetworks"):
		return t.json(req, http.StatusOK, items(fixtureNetworks()))

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

func interfaceIDs(p string) (eid, iid string) {
	seg := strings.Split(strings.Trim(p, "/"), "/")
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
	return eid, iid
}

func (t *transport) interfaces(req *http.Request, p string) (*http.Response, error) {
	eid, iid := interfaceIDs(p)
	el := t.element(eid)
	if el == nil {
		return t.noElement(req, eid)
	}
	if iid == "" {
		return t.json(req, http.StatusOK, items(t.set.withEdits(eid, el.apiInterfaces())))
	}
	st := el.apiInterfaceStatus(iid)
	if st == nil {
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": "interface " + iid + " not found on element " + eid})
	}
	return t.json(req, http.StatusOK, st)
}

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

func (t *transport) editInterface(req *http.Request, p string) (*http.Response, error) {
	eid, iid := interfaceIDs(p)
	el := t.element(eid)
	if el == nil {
		return t.noElement(req, eid)
	}
	if strings.HasSuffix(p, "/status") || iid == "" {
		return t.refuseWrite(req, p)
	}
	known := false
	for _, in := range el.apiInterfaces() {
		known = known || in.ID == iid
	}
	if !known {
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": "interface " + iid + " not found on element " + eid})
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return t.json(req, http.StatusBadRequest, map[string]string{"error": "body is not a JSON object: " + err.Error()})
	}
	return t.json(req, http.StatusOK, t.set.saveEdit(eid+"/"+iid, body, req.Method == http.MethodPatch))
}

func (t *transport) refuseWrite(req *http.Request, p string) (*http.Response, error) {
	return t.json(req, http.StatusMethodNotAllowed,
		map[string]string{"error": fmt.Sprintf("fixture mode takes edits to interfaces only, not %s %s", req.Method, p)})
}

func (s *Set) saveEdit(key string, body map[string]any, merge bool) map[string]any {
	s.editMu.Lock()
	defer s.editMu.Unlock()
	if s.edits == nil {
		s.edits = map[string]map[string]any{}
	}
	if have := s.edits[key]; merge && have != nil {
		for k, v := range body {
			have[k] = v
		}
		body = have
	}
	s.edits[key] = body
	return body
}

func (s *Set) withEdits(eid string, ifs []api.Interface) []map[string]any {
	b, _ := json.Marshal(ifs)
	var out []map[string]any
	_ = json.Unmarshal(b, &out)

	s.editMu.Lock()
	defer s.editMu.Unlock()
	for _, rec := range out {
		id, _ := rec["id"].(string)
		for k, v := range s.edits[eid+"/"+id] {
			rec[k] = v
		}
	}
	return out
}
