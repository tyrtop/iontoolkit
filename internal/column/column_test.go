package column

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestABareNameIsTheColumnAsShipped(t *testing.T) {
	set, err := Parse([]byte("columns:\n  - status\n  - name: site\n    max: 30\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(set.Columns) != 2 {
		t.Fatalf("%d columns, want 2", len(set.Columns))
	}
	if got := set.Columns[0]; got.Name != "status" || got.Value != "" {
		t.Errorf("a bare name parsed as %+v, want the name alone", got)
	}
	if got := set.Columns[1]; got.Name != "site" || got.Max != 30 {
		t.Errorf("an override parsed as %+v, want site capped at 30", got)
	}
	if got := set.Columns[1].Subject(); got != FromElement {
		t.Errorf("a column with no from reads %q, want %q", got, FromElement)
	}
}

func TestAValueAndItsStyleRulesAreCompiledAtLoad(t *testing.T) {
	set, err := Parse([]byte(`columns:
  - name: version
    header: Software
    from: element
    value: "{{ .software_version }}"
    style:
      - when: "{{ not .connected }}"
        role: err
      - role: dim
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	c := set.Columns[0]
	if c.Template == nil {
		t.Fatal("the value was not compiled")
	}
	if got := c.Template.Render(map[string]any{"software_version": "6.3.4-b2"}); got != "6.3.4-b2" {
		t.Errorf("value = %q, want the field", got)
	}
	if len(c.Rules) != 2 {
		t.Fatalf("%d style rules, want 2", len(c.Rules))
	}
	if c.Rules[0].When == nil || c.Rules[1].When != nil {
		t.Error("a rule with no when is the one that always matches, and keeps no template")
	}
}

func TestAFileThatCouldNotDrawIsRefusedByName(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"nothing in it", "columns: []\n", "no columns"},
		{"no name", "columns:\n  - header: Element\n", "column 1 has no name"},
		{"two of one name", "columns:\n  - name\n  - name\n", `two columns are called "name"`},
		{"an unknown subject", "columns:\n  - name: x\n    from: tenant\n    value: \"{{ .id }}\"\n", `reads "tenant"`},
		{"a site with no value", "columns:\n  - name: x\n    from: site\n", "reads the site but says no value"},
		{"a fetch with no value", "endpoints:\n  - name: elements\n    path: /sdwan/v2.0/api/elements\ncolumns:\n  - name: x\n    fetch: elements\n", "fetches and says no value"},
		{"a fetch and a from", "endpoints:\n  - name: e\n    path: /sdwan/v2.0/api/e\ncolumns:\n  - name: x\n    fetch: e\n    from: site\n    value: \"{{ .id }}\"\n", "drop the from:"},
		{"a bulk with no fetch", "endpoints:\n  - name: query\n    method: POST\n    path: /sdwan/v2.0/api/e/query\ncolumns:\n  - name: x\n    bulk: query\n    value: \"{{ .id }}\"\n", "no fetch: to fall back on"},
		{"a fetch off the api", "endpoints:\n  - name: elements\n    path: /api/elements\ncolumns:\n  - name: x\n    fetch: elements\n    value: \"{{ .id }}\"\n", "does not start with /sdwan/"},
		{"a role nothing draws", "columns:\n  - name: x\n    value: \"{{ .id }}\"\n    style:\n      - role: puce\n", `styled "puce"`},
		{"a value that will not parse", "columns:\n  - name: x\n    value: \"{{ .id\"\n", "value:"},
		{"a width inside out", "columns:\n  - name: site\n    min: 20\n    max: 8\n", "min 20 above max 8"},
		{"two marks", "columns:\n  - name: name\n    mark: true\n  - name: site\n    mark: true\n", "carry the selection mark"},
		{"a misspelled key", "columns:\n  - name: site\n    with: 20\n", "field with not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("Parse() of %q succeeded, want a refusal", tt.yaml)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to name %q", err, tt.want)
			}
		})
	}
}

func TestAFetchedColumnKeepsItsPathsAndKeys(t *testing.T) {
	set, err := Parse([]byte(`endpoints:
  - name: status
    path: /sdwan/v2.6/api/elements/{element_id}/status
  - name: query
    method: POST
    path: /sdwan/v2.6/api/elements/status/query
columns:
  - name: uptime
    header: Uptime
    fetch: status
    bulk: query
    value: "{{ ago .last_rebooted_time }}"
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	c := set.Columns[0]
	if !c.Fetches() {
		t.Error("the column does not report that it fetches")
	}
	if c.BulkEndpoint.Path != "/sdwan/v2.6/api/elements/status/query" || c.BulkEndpoint.Method != "POST" {
		t.Errorf("bulk = %+v, want the POST query the file declares", c.BulkEndpoint)
	}
	if c.Key != DefaultKey {
		t.Errorf("key = %q, want %q by default", c.Key, DefaultKey)
	}
	if c.Template == nil {
		t.Error("the value was not compiled")
	}
}

func TestNoFileLeavesTheColumnsIontkShips(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), FileName))
	if err != nil || set != nil {
		t.Errorf("Load() of a missing file = %v, %v; want nothing and no error", set, err)
	}
	if set, err := Load(""); err != nil || set != nil {
		t.Errorf("Load(\"\") = %v, %v; want nothing and no error", set, err)
	}
}

func TestABrokenFileIsNamedInTheError(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("columns:\n  - name: nope\n    from: site\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() of a broken file succeeded")
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("error = %v, want the file named", err)
	}
}
