package fixture

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/sase/api"
)

//go:embed routes.yaml
var routesYAML []byte

var routes = endpoint.MustParse("fixture routes", routesYAML)

func (s *Set) Transport(tsg string) http.RoundTripper {
	return &transport{set: s, tsg: tsg}
}

type transport struct {
	set *Set
	tsg string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	name, v, ok := routes.Match(req.Method, p)
	if !ok {
		switch req.Method {
		case http.MethodPut, http.MethodPatch, http.MethodDelete:
			return t.refuseWrite(req, p)
		}
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": fmt.Sprintf("fixture mode serves no %s", p)})
	}

	switch name {
	case "profile":
		return t.json(req, http.StatusOK, t.profile())
	case "sites":
		return t.json(req, http.StatusOK, items(t.set.apiSites(t.tsg)))
	case "elements":
		return t.json(req, http.StatusOK, items(t.set.apiElements(t.tsg)))
	case "wan_labels":
		return t.json(req, http.StatusOK, items(fixtureLabels()))
	case "wan_networks":
		return t.json(req, http.StatusOK, items(fixtureNetworks()))
	case "zones":
		return t.json(req, http.StatusOK, items(fixtureZones()))
	case "cluster_query":
		return t.json(req, http.StatusOK, items(t.set.apiClusterStatuses(t.tsg)))
	case "cluster_status":
		st := t.set.apiClusterStatus(t.tsg, v["cluster_id"])
		if st == nil {
			return t.json(req, http.StatusNotFound,
				map[string]string{"error": "no spoke cluster " + v["cluster_id"] + " in this tenant"})
		}
		return t.json(req, http.StatusOK, st)
	case "site_circuits", "circuit_status", "circuit_state":
		return t.circuits(req, name, v)
	}

	eid := v["element_id"]
	el := t.element(eid)
	if el == nil {
		return t.noElement(req, eid)
	}
	switch name {
	case "element":
		return t.json(req, http.StatusOK, el.apiElement())
	case "element_status":
		return t.json(req, http.StatusOK, el.apiStatus())
	case "element_zones":
		return t.json(req, http.StatusOK, items(el.apiZoneBindings()))
	case "interfaces":
		return t.json(req, http.StatusOK, items(t.set.withEdits(eid, el.apiInterfaces())))
	case "interface_status":
		st := el.apiInterfaceStatus(v["interface_id"])
		if st == nil {
			return t.json(req, http.StatusNotFound,
				map[string]string{"error": "interface " + v["interface_id"] + " not found on element " + eid})
		}
		return t.json(req, http.StatusOK, st)
	case "interface_put", "interface_patch":
		return t.editInterface(req, el, v["interface_id"])
	}
	return t.json(req, http.StatusNotFound,
		map[string]string{"error": fmt.Sprintf("fixture mode serves no %s", p)})
}

func (t *transport) circuits(req *http.Request, name string, v map[string]string) (*http.Response, error) {
	el := t.element(v["element_id"])
	if el == nil {
		el = t.siteElement(v["site_id"])
	}
	if el == nil {
		return t.json(req, http.StatusNotFound,
			map[string]string{"error": "no element of this tenant on that site"})
	}

	cid := v["circuit_id"]
	switch name {
	case "site_circuits":
		return t.json(req, http.StatusOK, items(el.apiCircuits()))
	case "circuit_state":
		if st := el.apiCircuitState(cid); st != nil {
			return t.json(req, http.StatusOK, st)
		}
	case "circuit_status":
		if st := el.apiCircuitStatus(cid); st != nil {
			return t.json(req, http.StatusOK, st)
		}
	}
	return t.json(req, http.StatusNotFound,
		map[string]string{"error": "no circuit " + cid + " on that site"})
}

func (t *transport) siteElement(site string) *Element {
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

func (t *transport) editInterface(req *http.Request, el *Element, iid string) (*http.Response, error) {
	eid := el.ID
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
