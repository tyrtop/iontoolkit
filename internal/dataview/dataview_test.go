package dataview

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fake struct {
	bodies map[string]string
	asked  []string
}

func (f *fake) GetJSON(_ context.Context, path string) ([]byte, error) {
	f.asked = append(f.asked, path)
	b, ok := f.bodies[path]
	if !ok {
		return nil, errNotFound(path)
	}
	return []byte(b), nil
}

type errNotFound string

func (e errNotFound) Error() string { return "status 404 Not Found: " + string(e) }

var builtinNames = []string{"Overview", "Interfaces", "Circuits", "HA"}

func TestBuiltinViewsLoad(t *testing.T) {
	set, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	want := builtinNames
	if got := set.Names(); len(got) != len(want) {
		t.Fatalf("views = %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := set.View(name); !ok {
			t.Errorf("no view named %q", name)
		}
	}
}

func TestLoadRejectsWhatAViewMayNotCall(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "a host of its own",
			yaml: `views: [{name: X, fetch: {a: "https://evil.example/x"}, sections: [{title: T, rows: [{label: L, value: V}]}]}]`,
			want: "names a host",
		},
		{
			name: "outside the sdwan api",
			yaml: `views: [{name: X, fetch: {a: "/iam/v1/tokens"}, sections: [{title: T, rows: [{label: L, value: V}]}]}]`,
			want: "does not start with /sdwan/",
		},
		{
			name: "walking up out of it",
			yaml: `views: [{name: X, fetch: {a: "/sdwan/../iam/v1/tokens"}, sections: [{title: T, rows: [{label: L, value: V}]}]}]`,
			want: "walks up",
		},
		{
			name: "a misspelled key",
			yaml: `views: [{name: X, sektions: []}]`,
			want: "field sektions not found",
		},
		{
			name: "columns with nothing to iterate",
			yaml: `views: [{name: X, sections: [{title: T, columns: [{header: H, value: V}]}]}]`,
			want: "no for_each",
		},
		{
			name: "rows and for_each at once",
			yaml: `views: [{name: X, sections: [{title: T, for_each: a.b, rows: [{label: L, value: V}], columns: [{header: H, value: V}]}]}]`,
			want: "one or the other",
		},
		{
			name: "two views of one name",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}]}, {name: x, sections: [{title: T, rows: [{label: L, value: V}]}]}]`,
			want: `two views are called "x"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("Parse() error = nil, want one naming %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestLoadMergesAUserFileOverTheBuiltins(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/views.yaml"
	body := `
views:
  - name: Overview
    fetch:
      el: /sdwan/v3.2/api/elements/{{ .element_id }}
    sections:
      - title: Mine
        rows:
          - label: Name
            value: "{{ .el.name }}"
  - name: Extra
    fetch:
      el: /sdwan/v3.2/api/elements/{{ .element_id }}
    sections:
      - title: Also mine
        rows:
          - label: ID
            value: "{{ .el.id }}"
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}

	set, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	names := set.Names()
	if len(names) != len(builtinNames)+1 || names[0] != "Overview" || names[len(names)-1] != "Extra" {
		t.Fatalf("views = %v, want the built-ins with Extra appended", names)
	}
	v, _ := set.View("Overview")
	if len(v.Sections) != 1 || v.Sections[0].Title != "Mine" {
		t.Errorf("Overview = %+v, want the user's", v.Sections)
	}

	plain, err := Load(dir + "/nothing.yaml")
	if err != nil {
		t.Fatalf("Load() on a missing file error = %v", err)
	}
	if len(plain.Names()) != len(builtinNames) {
		t.Errorf("views = %v, want the built-ins", plain.Names())
	}
}

func TestLoadKeepsTheBuiltinsWhenTheUserFileIsBroken(t *testing.T) {
	path := t.TempDir() + "/views.yaml"
	if err := writeFile(path, "views: [{name: X, fetch: {a: \"/iam/v1/tokens\"}, sections: [{title: T, rows: [{label: L, value: V}]}]}]"); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want the rejected path reported")
	}
	if set == nil || len(set.Names()) != len(builtinNames) {
		t.Fatalf("set = %v, want the built-ins anyway", set)
	}
}

const ifaceView = `
views:
  - name: Interfaces
    fetch:
      ifs: /sdwan/v4.21/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces
    sections:
      - title: Ports
        for_each: ifs.items
        fetch:
          st: /sdwan/v3.9/api/sites/{{ .site_id }}/elements/{{ .element_id }}/interfaces/{{ .item.id }}/status
        columns:
          - header: PORT
            value: "{{ .item.name }}"
          - header: STATE
            value: "{{ (unwrap .st).operational_state }}"
          - header: ADDRESS
            value: "{{ dash (first (unwrap .st).ipv4_addresses) }}"
`

func TestATableFetchesOncePerRowAndUnwrapsAnEnvelope(t *testing.T) {
	f := &fake{bodies: map[string]string{
		"/sdwan/v4.21/api/sites/16/elements/15/interfaces": `{"items":[
			{"id":"17","name":"1"},
			{"id":"18","name":"45"},
			{"id":"19","name":"3"}]}`,
		"/sdwan/v3.9/api/sites/16/elements/15/interfaces/17/status": `{"operational_state":"up","ipv4_addresses":["192.0.2.1/24"]}`,
		"/sdwan/v3.9/api/sites/16/elements/15/interfaces/18/status": `{"count":2,"items":[
			{"operational_state":"up","ipv4_addresses":null},
			{"operational_state":"up"}]}`,
	}}

	set, err := Parse([]byte(ifaceView))
	if err != nil {
		t.Fatal(err)
	}
	v := set.Views[0]

	d, err := Fetch(context.Background(), f, v, Vars{"element_id": "15", "site_id": "16"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(f.asked) != 4 {
		t.Errorf("asked %d endpoints, want 4: %v", len(f.asked), f.asked)
	}

	r := Render(v, d)
	if len(r.Sections) != 1 {
		t.Fatalf("sections = %+v", r.Sections)
	}
	sec := r.Sections[0]
	if len(sec.Table) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(sec.Table), sec.Table)
	}
	if got := sec.Table[0].Cells; got[0] != "1" || got[1] != "up" || got[2] != "192.0.2.1/24" {
		t.Errorf("row 0 = %q", got)
	}
	if got := sec.Table[1].Cells; got[1] != "up" || got[2] != "-" {
		t.Errorf("row 1 = %q, want the envelope unwrapped", got)
	}
	last := sec.Table[2]
	if last.Cells[0] != "3" || last.Cells[1] != "" {
		t.Errorf("row 2 = %q", last.Cells)
	}
	if !strings.Contains(last.Err, "404") {
		t.Errorf("row 2 error = %q, want the 404", last.Err)
	}
}

func TestMaxItemsBoundsTheRequestsATableMakes(t *testing.T) {
	items := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		items = append(items, map[string]any{"id": "x", "name": "p"})
	}
	list, _ := json.Marshal(map[string]any{"items": items})

	f := &fake{bodies: map[string]string{
		"/sdwan/v4.21/api/sites/16/elements/15/interfaces":         string(list),
		"/sdwan/v3.9/api/sites/16/elements/15/interfaces/x/status": `{"operational_state":"up"}`,
	}}
	set, err := Parse([]byte(strings.Replace(ifaceView, "for_each: ifs.items",
		"for_each: ifs.items\n        max_items: 3", 1)))
	if err != nil {
		t.Fatal(err)
	}
	v := set.Views[0]

	d, err := Fetch(context.Background(), f, v, Vars{"element_id": "15", "site_id": "16"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(f.asked) != 4 {
		t.Errorf("asked %d endpoints, want 1 list and 3 statuses: %v", len(f.asked), f.asked)
	}
	r := Render(v, d)
	if got := r.Sections[0].Truncated; got != 7 {
		t.Errorf("truncated = %d, want 7", got)
	}
}

func TestAnEmptyIdIsNotFetched(t *testing.T) {
	f := &fake{bodies: map[string]string{}}
	set, err := Parse([]byte(ifaceView))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Fetch(context.Background(), f, set.Views[0], Vars{"element_id": "15"})
	if err == nil {
		t.Fatal("Fetch() error = nil, want the empty site id reported")
	}
	if len(f.asked) != 0 {
		t.Errorf("asked %v, want nothing", f.asked)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want it to name the empty id", err)
	}
}

func TestSectionsAreGatedAndErrorsSurface(t *testing.T) {
	const view = `
views:
  - name: V
    fetch:
      el: /sdwan/v3.2/api/elements/{{ .element_id }}
      cl: /sdwan/v2.0/api/sites/{{ .site_id }}/spokeclusters/{{ .cluster_id }}/status
    sections:
      - title: Always
        rows:
          - label: Name
            value: "{{ .el.name }}"
      - title: HA
        when: "{{ .el.spoke_ha_config.cluster_id }}"
        rows:
          - label: Cluster
            value: "{{ .el.spoke_ha_config.cluster_id }}"
`
	f := &fake{bodies: map[string]string{
		"/sdwan/v3.2/api/elements/15": `{"id":"15","name":"S0100-RTR-02"}`,
	}}
	set, err := Parse([]byte(view))
	if err != nil {
		t.Fatal(err)
	}
	v := set.Views[0]

	d, err := Fetch(context.Background(), f, v, Vars{"element_id": "15", "site_id": "16", "cluster_id": "20"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	r := Render(v, d)
	if len(r.Sections) != 1 || r.Sections[0].Title != "Always" {
		t.Fatalf("sections = %+v, want only the ungated one", r.Sections)
	}
	if r.Sections[0].Rows[0].Value != "S0100-RTR-02" {
		t.Errorf("row = %+v", r.Sections[0].Rows[0])
	}
	if len(r.Errs) != 1 || !strings.Contains(r.Errs[0], "cl:") {
		t.Errorf("errs = %v, want the refused cluster fetch named", r.Errs)
	}
}

func TestLookupJoinsBothWaysRound(t *testing.T) {
	ctx := map[string]any{
		"wans": map[string]any{"items": []any{
			map[string]any{"id": "c1", "name": "ISP 1"},
			map[string]any{"id": "c2", "name": "ISP 2"},
		}},
		"ifs": map[string]any{"items": []any{
			map[string]any{"name": "1", "site_wan_interface_ids": []any{"c1"}},
			map[string]any{"name": "2", "site_wan_interface_ids": []any{"c2"}},
			map[string]any{"name": "3", "site_wan_interface_ids": nil},
		}},
	}

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{
			name: "a port names its circuit",
			tmpl: `{{ $p := index .ifs.items 1 }}{{ range $p.site_wan_interface_ids }}{{ with lookup $.wans.items "id" . }}{{ .name }}{{ end }}{{ end }}`,
			want: "ISP 2",
		},
		{
			name: "a circuit names its port",
			tmpl: `{{ with lookup .ifs.items "site_wan_interface_ids" "c1" }}{{ .name }}{{ end }}`,
			want: "1",
		},
		{
			name: "nothing bound comes out blank, not wrong",
			tmpl: `{{ with lookup .ifs.items "site_wan_interface_ids" "c9" }}{{ .name }}{{ end }}`,
			want: "",
		},
		{
			name: "an empty id matches nothing rather than the first row",
			tmpl: `{{ with lookup .wans.items "id" "" }}{{ .name }}{{ end }}`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eval(tt.tmpl, ctx); got != tt.want {
				t.Errorf("eval() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplateHelpers(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		ctx  map[string]any
		want string
	}{
		{
			name: "utc reads 100ns ticks",
			tmpl: "{{ utc .n }}",
			ctx:  map[string]any{"n": float64(17923299413050015)},
			want: "2026-10-18 13:25 UTC",
		},
		{
			name: "utc reads milliseconds",
			tmpl: "{{ utc .n }}",
			ctx:  map[string]any{"n": float64(1792307867657)},
			want: "2026-10-18 07:17 UTC",
		},
		{
			name: "utc reads seconds",
			tmpl: "{{ utc .n }}",
			ctx:  map[string]any{"n": float64(1784373284)},
			want: "2026-07-18 11:14 UTC",
		},
		{
			name: "a field that was never set is blank, not a date",
			tmpl: "{{ utc .n }}",
			ctx:  map[string]any{"n": float64(0)},
			want: "",
		},
		{
			name: "a field the element has not got is blank",
			tmpl: "{{ .el.nothing }}",
			ctx:  map[string]any{"el": map[string]any{}},
			want: "",
		},
		{
			name: "join",
			tmpl: `{{ join .tags ", " }}`,
			ctx:  map[string]any{"tags": []any{"HUB", "SPOKE"}},
			want: "HUB, SPOKE",
		},
		{
			name: "count of a list",
			tmpl: "{{ count .l }}",
			ctx:  map[string]any{"l": []any{1, 2, 3}},
			want: "3",
		},
		{
			name: "yesno",
			tmpl: "{{ yesno .b }}",
			ctx:  map[string]any{"b": true},
			want: "yes",
		},
		{
			name: "dash on nothing",
			tmpl: "{{ dash .s }}",
			ctx:  map[string]any{"s": ""},
			want: "-",
		},
		{
			name: "a broken template says so in place of the value",
			tmpl: "{{ nosuchfunc .x }}",
			ctx:  map[string]any{},
			want: "!",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval(tt.tmpl, tt.ctx)
			if tt.want == "!" {
				if !strings.HasPrefix(got, "!") {
					t.Errorf("eval() = %q, want an error in place of the value", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("eval() = %q, want %q", got, tt.want)
			}
		})
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
