package endpoint

import (
	"strings"
	"testing"
)

const sample = `host: https://api.example.com
values:
  client_id: scm
endpoints:
  - name: element
    path: /sdwan/v3.2/api/elements/{element_id}
  - name: delete_site
    method: delete
    path: /sdwan/v4.9/api/sites/{site}
  - name: session
    method: WS
    host: wss://ws.example.com
    path: /sdwan/v2.0/api/elements/{element_id}/ws?cols={cols}&rows={rows}
`

func TestAValueCanOnlyEverBeOneSegment(t *testing.T) {
	s, err := Parse("sample", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		v    map[string]string
		want string
		bad  string
	}{
		{name: "element", v: map[string]string{"element_id": "15"}, want: "https://api.example.com/sdwan/v3.2/api/elements/15"},
		{name: "element", v: map[string]string{"element_id": "x/../../sites"}, bad: "would change the path"},
		{name: "element", v: map[string]string{"element_id": ".."}, bad: "would change the path"},
		{name: "element", v: map[string]string{"element_id": "15?all=1"}, bad: "would change the path"},
		{name: "element", v: map[string]string{"element_id": "15#x"}, bad: "would change the path"},
		{name: "element", v: map[string]string{"element_id": "%2e%2e"}, bad: "would change the path"},
		{name: "element", v: map[string]string{}, bad: "needs element_id"},
		{name: "element", v: map[string]string{"element_id": ""}, bad: "needs element_id"},
		{name: "session", v: map[string]string{"element_id": "15", "cols": "80", "rows": "24"},
			want: "wss://ws.example.com/sdwan/v2.0/api/elements/15/ws?cols=80&rows=24"},
		{name: "nope", v: nil, bad: "has no endpoint nope"},
	} {
		got, err := s.URL(tt.name, tt.v)
		if tt.bad != "" {
			if err == nil || !strings.Contains(err.Error(), tt.bad) {
				t.Errorf("%s %v: got %q, %v; want an error with %q", tt.name, tt.v, got, err, tt.bad)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%s %v: got %q, %v; want %q", tt.name, tt.v, got, err, tt.want)
		}
	}
	if e, _ := s.Get("delete_site"); e.Method != "DELETE" {
		t.Errorf("method %q, want DELETE", e.Method)
	}
	if s.Value("client_id") != "scm" {
		t.Errorf("client_id = %q", s.Value("client_id"))
	}
}

func TestAFileThatCouldReachOutsideItsPathDoesNotLoad(t *testing.T) {
	for _, bad := range []string{
		"endpoints:\n  - name: a\n    path: sdwan/no-slash\n",
		"endpoints:\n  - name: a\n    path: /sdwan/../tenancy\n",
		"endpoints:\n  - name: a\n    method: TRACE\n    path: /x\n",
		"host: http://plain.example.com\nendpoints:\n  - name: a\n    path: /x\n",
		"host: https://a.example.com/prefix\nendpoints:\n  - name: a\n    path: /x\n",
		"endpoints:\n  - name: a\n    path: /x\n  - name: a\n    path: /y\n",
		"endpoints:\n  - name: a\n    path: /x\n    extra: 1\n",
		"endpoints:\n  - name: a\n    path: /sdwan/elements/{element_id\n",
	} {
		if _, err := Parse("bad", []byte(bad)); err == nil {
			t.Errorf("loaded:\n%s", bad)
		}
	}
}

func TestARequestIsMatchedToTheEndpointItCameFrom(t *testing.T) {
	s := MustParse("routes", []byte(`endpoints:
  - name: element
    path: /sdwan/{version}/api/elements/{element_id}
  - name: status
    path: /sdwan/{version}/api/elements/{element_id}/status
  - name: query
    method: POST
    path: /sdwan/{version}/api/spokeclusters/status/query
`))
	for _, tt := range []struct {
		method, path, name, id string
	}{
		{"GET", "/sdwan/v3.2/api/elements/15", "element", "15"},
		{"GET", "/sdwan/v2.6/api/elements/15/status", "status", "15"},
		{"POST", "/sdwan/v2.0/api/spokeclusters/status/query", "query", ""},
		{"PUT", "/sdwan/v3.2/api/elements/15", "", ""},
		{"GET", "/sdwan/v3.2/api/elements/", "", ""},
		{"GET", "/sdwan/v3.2/api/elements/15/status/x", "", ""},
	} {
		name, v, ok := s.Match(tt.method, tt.path)
		if name != tt.name || ok != (tt.name != "") || v["element_id"] != tt.id {
			t.Errorf("Match(%s %s) = %q %v %v", tt.method, tt.path, name, v, ok)
		}
	}
}
