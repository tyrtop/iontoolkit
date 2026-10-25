package tui

import (
	"strings"
	"testing"
	"tyrtop.com/iontk/internal/runtime"

	"tyrtop.com/iontk/internal/column"
)

func TestEveryShippedColumnNamesTheCallItComesFrom(t *testing.T) {
	set, err := column.Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	for _, c := range set.Columns {
		if c.Go != "" {
			continue
		}
		if c.Fetch == "" {
			t.Errorf("column %q says neither a fetch: nor a go:", c.Name)
		}
	}
	if got := pathOf(t, set, column.ElementsSrc); got != "/sdwan/v3.2/api/elements" {
		t.Errorf("the element list reads %q", got)
	}
	if got := pathOf(t, set, column.SitesSrc); got != "/sdwan/v4.13/api/sites" {
		t.Errorf("the site names read %q", got)
	}
}

func TestTheListIsFetchedFromThePathTheFileNames(t *testing.T) {
	m := listModelFrom(t, `kind: list
endpoints:
  - name: elements
    path: /sdwan/v9.9/api/elements
  - name: sites
    path: /sdwan/v8.8/api/sites
columns:
  - name: name
    fetch: elements
    value: "{{ .label }}"
  - name: where
    header: Where
    fetch: sites
    on: site_id
    value: "{{ .name }}"
`, 2)
	if m.columnsErr != "" {
		t.Fatalf("the file was refused: %s", m.columnsErr)
	}
	if got, _ := m.runtime().ListPath(column.ElementsSrc, nil); got != "/sdwan/v9.9/api/elements" {
		t.Errorf("the loader would ask for %q, want the path the file names", got)
	}
	if got, _ := m.runtime().ListPath(column.SitesSrc, nil); got != "/sdwan/v8.8/api/sites" {
		t.Errorf("sites would come from %q", got)
	}
}

func TestAColumnOffTheElementListCostsNoCallOfItsOwn(t *testing.T) {
	m := listModelFrom(t, `kind: list
endpoints:
  - name: elements
    path: /sdwan/v3.2/api/elements
columns:
  - name: name
    fetch: elements
    value: "{{ .label }}"
  - name: model
    header: Model
    fetch: elements
    value: "{{ .model_name }}"
`, 2)
	if jobs := m.runtime().TablesWanted("1"); len(jobs) != 0 {
		t.Errorf("a column reading the element list asked the portal again: %v", jobs)
	}
	if got := plain(columnCell(t, &m, 1, "model")); got != "ion 1200" {
		t.Errorf("model = %q, want it read off the record the list already has", got)
	}
}

func TestAJoinOnSomethingAnElementHasNotGotIsRefused(t *testing.T) {
	m := listModelFrom(t, `kind: list
endpoints:
  - name: elements
    path: /sdwan/v3.2/api/elements
  - name: securityzones
    path: /sdwan/v2.2/api/securityzones
columns:
  - name: name
    fetch: elements
    value: "{{ .label }}"
  - name: zone
    fetch: securityzones
    on: zone_id
    value: "{{ .name }}"
`, 2)
	if m.columnsErr == "" {
		t.Fatal("a join on a field an element has not got loaded")
	}
	for _, want := range []string{"zone_id", "site_id"} {
		if !strings.Contains(m.columnsErr, want) {
			t.Errorf("columnsErr = %q, want it to name %q", m.columnsErr, want)
		}
	}
}

func TestAFleetCallTheTenantRefusesSaysSo(t *testing.T) {
	m := listModelFrom(t, `kind: list
endpoints:
  - name: elements
    path: /sdwan/v3.2/api/elements
  - name: securityzones
    path: /sdwan/v2.2/api/securityzones
columns:
  - name: name
    fetch: elements
    value: "{{ .label }}"
  - name: zone
    header: Zone
    fetch: securityzones
    on: site_id
    value: "{{ .name }}"
`, 2)
	m.runtime().ApplyTable(runtime.TableResult{Name: "securityzones", TSG: "1", Err: "403 forbidden"})
	m = settle(t, m)

	if !strings.Contains(m.status, "403") {
		t.Errorf("status = %q, want the reason the call was refused", m.status)
	}
	if got := plain(columnCell(t, &m, 0, "zone")); got != "-" {
		t.Errorf("row 0 = %q, want a dash once the call has failed", got)
	}
	if jobs := m.runtime().TablesWanted("1"); len(jobs) != 1 {
		t.Errorf("the tenant's next load asked %v, want the refused table asked again", jobs)
	}
}

func pathOf(t *testing.T, set *column.Set, name string) string {
	t.Helper()
	p, err := set.Calls().Path(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
