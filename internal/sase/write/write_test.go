package write

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func interfaceSpec(t *testing.T) Spec {
	t.Helper()
	set, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	w, ok := set.Spec("interface")
	if !ok {
		t.Fatal("no built-in interface write")
	}
	return w
}

func record() map[string]any {
	return map[string]any{
		"id":          "17",
		"name":        "1",
		"_etag":       float64(4),
		"description": "old",
		"admin_up":    true,
		"mtu":         float64(1500),
		"tags":        nil,
		"ipv4_config": nil,
	}
}

var vars = map[string]string{"site_id": "16", "element_id": "15"}

func TestThePlanSendsTheWholeRecordWithOnlyTheEditChanged(t *testing.T) {
	w := interfaceSpec(t)
	p, err := w.Plan(vars, record(), map[string]string{"description": "uplink to core", "mtu": "1500"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != "/sdwan/v4.21/api/sites/16/elements/15/interfaces/17" || p.Method != "PUT" {
		t.Errorf("plan is %s %s", p.Method, p.Path)
	}
	if len(p.Changes) != 1 || p.Changes[0].Field != "description" || p.Changes[0].To != "uplink to core" {
		t.Errorf("changes = %+v, want the description and not the unchanged mtu", p.Changes)
	}

	var sent map[string]any
	b, _ := p.Body()
	_ = json.Unmarshal(b, &sent)
	if sent["_etag"] != float64(4) || sent["name"] != "1" || sent["admin_up"] != true {
		t.Errorf("body %s lost a field it did not edit", b)
	}
	if p.Before["description"] != "old" {
		t.Error("planning changed the record it was given")
	}
}

func TestFieldsAreTyped(t *testing.T) {
	w := interfaceSpec(t)
	p, err := w.Plan(vars, record(), map[string]string{"admin_up": "false", "mtu": "9000", "tags": "core, wan ,"})
	if err != nil {
		t.Fatal(err)
	}
	if p.After["admin_up"] != false || p.After["mtu"] != float64(9000) {
		t.Errorf("after = %v", p.After)
	}
	if got := Show(p.After["tags"]); got != "core, wan" {
		t.Errorf("tags = %q", got)
	}

	for _, bad := range []map[string]string{
		{"admin_up": "maybe"},
		{"mtu": "big"},
		{"name": "2"},
	} {
		if _, err := w.Plan(vars, record(), bad); err == nil {
			t.Errorf("Plan(%v) took it", bad)
		}
	}
}

func TestClearingAnEmptyFieldIsNoChange(t *testing.T) {
	w := interfaceSpec(t)
	p, err := w.Plan(vars, record(), map[string]string{"tags": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("changes = %+v, want none: tags were null and are still empty", p.Changes)
	}
}

func TestADottedFieldIsMadeUnderANullObject(t *testing.T) {
	set, err := Parse([]byte(`
writes:
  - name: address
    list: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces
    item: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces/{{ .item.id }}
    fields:
      - key: ipv4_config.static_config.address
`))
	if err != nil {
		t.Fatal(err)
	}
	w, _ := set.Spec("address")
	p, err := w.Plan(vars, record(), map[string]string{"ipv4_config.static_config.address": "10.1.1.1/30"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := lookup(p.After, "ipv4_config.static_config.address")
	if got != "10.1.1.1/30" {
		t.Errorf("after = %v", p.After["ipv4_config"])
	}
}

func TestMatchPicksByTheSpecFieldOrAnyOther(t *testing.T) {
	w := interfaceSpec(t)
	rec := record()
	rec["used_for"] = "lan"
	tests := []struct {
		sel  string
		want bool
	}{
		{"1", true},
		{"2", false},
		{"used_for=LAN", true},
		{"used_for=public", false},
		{"nosuch=1", false},
	}
	for _, tt := range tests {
		if got := w.Matches(rec, tt.sel); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.sel, got, tt.want)
		}
	}
}

func TestBadSpecsAreNamed(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"no fields", `writes: [{name: a, list: /sdwan/x, item: /sdwan/x/1}]`, "no fields"},
		{"a host", `writes: [{name: a, list: "https://x/sdwan/x", item: /sdwan/x/1, fields: [{key: d}]}]`, "names a host"},
		{"delete", `writes: [{name: a, method: delete, list: /sdwan/x, item: /sdwan/x/1, fields: [{key: d}]}]`, "PUT or PATCH"},
		{"type", `writes: [{name: a, list: /sdwan/x, item: /sdwan/x/1, fields: [{key: d, type: date}]}]`, "type"},
		{"twice", `writes: [{name: a, list: /sdwan/x, item: /sdwan/x/1, fields: [{key: d}]}, {name: A, list: /sdwan/x, item: /sdwan/x/1, fields: [{key: d}]}]`, "two writes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse() error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestAUserFileOverridesTheBuiltinByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`
writes:
  - name: interface
    verified: true
    list: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces
    item: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces/{{ .item.id }}
    fields:
      - key: description
`), 0o600)
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := set.Spec("interface")
	if !w.Verified || len(w.Fields) != 1 || w.Source != path {
		t.Errorf("interface = %+v, want the user's", w)
	}
}

type getter map[string]string

func (g getter) GetJSON(_ context.Context, path string) ([]byte, error) {
	if b, ok := g[path]; ok {
		return []byte(b), nil
	}
	return nil, errors.New("404 " + path)
}

func TestRecordsUnwrapTheEnvelope(t *testing.T) {
	w := interfaceSpec(t)
	g := getter{"/sdwan/v4.21/api/sites/16/elements/15/interfaces": `{"count":2,"items":[{"id":"1","name":"1"},{"id":"2","name":"2"}]}`}
	recs, err := w.Records(context.Background(), g, vars)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[1]["name"] != "2" {
		t.Errorf("records = %v", recs)
	}
	if _, err := w.Records(context.Background(), g, map[string]string{"element_id": "15"}); err == nil {
		t.Error("a list path with no site in it was fetched")
	}
}

type sender struct {
	calls  int
	method string
	path   string
	body   []byte
	err    error
}

func (s *sender) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	s.calls++
	s.method, s.path, s.body = method, path, body
	return []byte(`{}`), s.err
}

func TestTheGuardWillNotSendAnUnverifiedSpec(t *testing.T) {
	w := interfaceSpec(t)
	p, _ := w.Plan(vars, record(), map[string]string{"description": "x"})
	s := &sender{}
	_, err := Guard{}.Apply(context.Background(), s, w, p)
	if err == nil || !strings.Contains(err.Error(), "not verified") || s.calls != 0 {
		t.Errorf("Apply() error = %v, %d sent", err, s.calls)
	}
}

func TestTheGuardLogsWhatItSends(t *testing.T) {
	w := interfaceSpec(t)
	w.Verified = true
	p, _ := w.Plan(vars, record(), map[string]string{"description": "new"})
	log := filepath.Join(t.TempDir(), LogName)
	g := Guard{Tenant: "Acme", Log: log, now: func() time.Time { return time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC) }}

	s := &sender{}
	if _, err := g.Apply(context.Background(), s, w, p); err != nil {
		t.Fatal(err)
	}
	s.err = errors.New("409 etag mismatch")
	if _, err := g.Apply(context.Background(), s, w, p); err == nil {
		t.Fatal("a refused write came back as sent")
	}

	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want one per attempt:\n%s", len(lines), b)
	}
	var first, second entry
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if !first.OK || first.Tenant != "Acme" || first.Method != "PUT" || len(first.Changes) != 1 || first.At != "2026-10-23T13:00:00Z" {
		t.Errorf("first = %+v", first)
	}
	if second.OK || !strings.Contains(second.Error, "etag") {
		t.Errorf("second = %+v", second)
	}
	if fi, _ := os.Stat(log); fi.Mode().Perm() != 0o600 {
		t.Errorf("log is %v, want 0600", fi.Mode().Perm())
	}
}
