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

	case strings.Contains(p, "/api/elements/"):
		eid := path.Base(p)
		el := t.set.element(eid)
		// Outside the tenant is a 404 too: a tenant's token cannot see another
		// tenant's elements, and a fixture that pretended otherwise would hide
		// exactly the bug that looks like a site ID mistaken for an element.
		if el == nil || el.tsg != t.tsg {
			return t.json(req, http.StatusNotFound,
				map[string]string{"error": "element " + eid + " not found in this tenant"})
		}
		return t.json(req, http.StatusOK, el.apiElement())
	}

	return t.json(req, http.StatusNotFound,
		map[string]string{"error": fmt.Sprintf("fixture mode serves no %s", p)})
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
