package dataview

import (
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/ioncli"
)

const dumpInterfaceStatus4 = `Interface	: 4
Device		: e382
ID		: 1724777596590008596
MAC Address	: 04:47:2a:18:0e:05
State		: down
Last Change	: 2026-07-18 13:35:26.686 (2190h12m13s ago)
Status		: Not connected
VRF Name	: Global`

func TestCLIHelpersReadDeviceOutput(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{"a key and its value", `{{ kv .out "State" }}`, "down"},
		{"a key holding a space", `{{ kv .out "MAC Address" }}`, "04:47:2a:18:0e:05"},
		{"a key that is a prefix of another", `{{ kv .out "Status" }}`, "Not connected"},
		{"a key the firmware did not print", `{{ kv .out "Speed" }}`, ""},
		{"case does not matter", `{{ kv .out "state" }}`, "down"},
		{"grep keeps the lines holding it", `{{ count (grep .out "VRF") }}`, "1"},
		{"grep finds nothing", `{{ count (grep .out "tunnel") }}`, "0"},
		{"lines are every non-blank one", `{{ count (lines .out) }}`, "8"},
		{"field counts from one", `{{ field (kv .out "Last Change") 1 }}`, "2026-07-18"},
		{"field past the end", `{{ field (kv .out "State") 9 }}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval(tt.tmpl, map[string]any{"out": dumpInterfaceStatus4})
			if got != tt.want {
				t.Errorf("%s = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

func TestVerdictIgnoresCaseAndSpacing(t *testing.T) {
	tests := []struct {
		name           string
		portal, device string
		want           Verdict
	}{
		{"the same answer", "up", "up", Agree},
		{"spelled differently", "Active", "active", Agree},
		{"padded by the device", "6.3.4-b2", " 6.3.4-b2 ", Agree},
		{"columns of space", "ion 1200", "ion  1200", Agree},
		{"two answers", "up", "down", Differ},
		{"the device said nothing", "up", "", Unknown},
		{"the portal has not got it", "", "up", Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verdict(tt.portal, tt.device); got != tt.want {
				t.Errorf("verdict(%q, %q) = %v, want %v", tt.portal, tt.device, got, tt.want)
			}
		})
	}
}

func TestCompareCommandsAreDeduplicated(t *testing.T) {
	views := []View{
		{Name: "A", Compare: &Compare{Commands: map[string]string{"dev": "dump device status"}}},
		{Name: "B", Compare: &Compare{Commands: map[string]string{
			"same":  "dump device status",
			"other": "dump spoke-ha status",
		}}},
		{Name: "C"},
	}
	got := CompareCommands(views)
	want := []string{"dump device status", "dump spoke-ha status"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("CompareCommands() = %v, want %v", got, want)
	}
}

func TestCompareViewsSkipWhatTheElementHasNotGot(t *testing.T) {
	views := []View{
		{Name: "Always", Compare: &Compare{Commands: map[string]string{"a": "dump device status"}}},
		{Name: "HA", Compare: &Compare{
			When:     "{{ .cluster_id }}",
			Commands: map[string]string{"a": "dump spoke-ha status"},
		}},
		{Name: "No comparison"},
	}

	clustered := CompareViews(views, Vars{"cluster_id": "123"})
	if len(clustered) != 2 {
		t.Errorf("clustered element compares %d views, want both", len(clustered))
	}
	spoke := CompareViews(views, Vars{"cluster_id": ""})
	if len(spoke) != 1 || spoke[0].Name != "Always" {
		t.Errorf("unclustered element compares %v, want only the ungated view", names(spoke))
	}
	if cmds := CompareCommands(spoke); len(cmds) != 1 {
		t.Errorf("commands for an unclustered element = %v, want only the ungated one", cmds)
	}
}

func names(views []View) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Name)
	}
	return out
}

func TestRenderCompareMarksEachRow(t *testing.T) {
	set, err := Parse([]byte(`
views:
  - name: Check
    fetch:
      el: /sdwan/v3.2/api/elements/{{ .element_id }}
    sections:
      - title: Device
        rows:
          - label: Name
            value: "{{ .el.name }}"
    compare:
      fetch:
        el: /sdwan/v3.2/api/elements/{{ .element_id }}
      commands:
        dev: dump device status
      rows:
        - label: Serial
          portal: "{{ .el.serial_number }}"
          device: "{{ kv .cli.dev \"Serial\" }}"
        - label: Software
          portal: "{{ .el.software_version }}"
          device: "{{ kv .cli.dev \"Version\" }}"
        - label: Uptime
          portal: "{{ .el.uptime }}"
          device: "{{ kv .cli.dev \"Uptime\" }}"
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	v := set.Views[0]

	f := &fake{bodies: map[string]string{
		"/sdwan/v3.2/api/elements/1": `{"name":"S0100-RTR-02","serial_number":"ION0001","software_version":"6.3.4-b2"}`,
	}}
	vars := Vars{"element_id": "1"}
	d, err := FetchCompare(t.Context(), f, v, vars)
	if err != nil {
		t.Fatalf("FetchCompare() error = %v", err)
	}
	if len(f.asked) != 1 {
		t.Errorf("asked %v, want the compare block's own fetch", f.asked)
	}

	out := DeviceOutput{"dump device status": "Serial\t: ION0001\nVersion\t: 6.3.4-b1\n"}
	cmp := RenderCompare(v, d, out)
	if len(cmp.Commands) != 1 || cmp.Commands[0] != "dump device status" {
		t.Errorf("Commands = %v, want what the device was asked", cmp.Commands)
	}

	want := []Compared{
		{Label: "Serial", Portal: "ION0001", Device: "ION0001", Verdict: Agree},
		{Label: "Software", Portal: "6.3.4-b2", Device: "6.3.4-b1", Verdict: Differ},
		{Label: "Uptime", Portal: "", Device: "", Verdict: Unknown},
	}
	if len(cmp.Rows) != len(want) {
		t.Fatalf("rows = %+v, want %d", cmp.Rows, len(want))
	}
	for i, w := range want {
		if cmp.Rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, cmp.Rows[i], w)
		}
	}
}

func TestRenderCompareReportsAFailedFetch(t *testing.T) {
	v := View{
		Name: "Check",
		Compare: &Compare{
			Fetch:    map[string]string{"el": "/sdwan/v3.2/api/elements/{{ .element_id }}"},
			Commands: map[string]string{"dev": "dump device status"},
			Rows: []CompareRow{{
				Label:  "Serial",
				Portal: "{{ .el.serial_number }}",
				Device: `{{ kv .cli.dev "Serial" }}`,
			}},
		},
	}
	f := &fake{bodies: map[string]string{}}
	d, err := FetchCompare(t.Context(), f, v, Vars{"element_id": "1"})
	if err == nil {
		t.Fatal("FetchCompare() error = nil, want the 404 reported")
	}

	cmp := RenderCompare(v, d, DeviceOutput{"dump device status": "Serial\t: ION0001"})
	if len(cmp.Errs) != 1 || !strings.Contains(cmp.Errs[0], "404") {
		t.Errorf("Errs = %v, want the fetch failure", cmp.Errs)
	}
	row := cmp.Rows[0]
	if row.Device != "ION0001" || row.Verdict != Unknown {
		t.Errorf("row = %+v, want the device answer kept and nothing to compare it to", row)
	}
}

func TestLoadRejectsABadCompareBlock(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "a command that writes",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}], compare: {commands: {a: "config interface 1 enabled=false"}, rows: [{label: L, portal: P, device: D}]}}]`,
			want: "not read-only",
		},
		{
			name: "no rows",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}], compare: {commands: {a: "dump device status"}}}]`,
			want: "compares nothing",
		},
		{
			name: "nothing asked of the device",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}], compare: {rows: [{label: L, portal: P, device: D}]}}]`,
			want: "without asking the device",
		},
		{
			name: "one side only",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}], compare: {commands: {a: help}, rows: [{label: Serial, portal: P}]}}]`,
			want: "both a portal and a device side",
		},
		{
			name: "a fetch outside the api",
			yaml: `views: [{name: X, sections: [{title: T, rows: [{label: L, value: V}]}], compare: {fetch: {a: /iam/v1/tokens}, commands: {a: help}, rows: [{label: L, portal: P, device: D}]}}]`,
			want: "does not start with /sdwan/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("Parse() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestBuiltinComparisonsOnlyRead(t *testing.T) {
	set, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	var withCompare int
	for _, v := range set.Views {
		if v.Compare == nil {
			continue
		}
		withCompare++
		for name, cmd := range v.Compare.Commands {
			if !ioncli.IsReadOnly(cmd) {
				t.Errorf("view %q command %q runs %q, which writes", v.Name, name, cmd)
			}
		}
	}
	if withCompare == 0 {
		t.Error("no built-in view compares anything, so the keypress has nothing to run")
	}
}
