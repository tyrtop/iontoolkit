package tui

import (
	"encoding/json"
	"strings"
	"testing"

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
	if got := set.Source(column.ElementsSrc); got != "/sdwan/v3.2/api/elements" {
		t.Errorf("the element list reads %q", got)
	}
	if got := set.Source(column.SitesSrc); got != "/sdwan/v4.13/api/sites" {
		t.Errorf("the site names read %q", got)
	}
}

func TestTheListIsFetchedFromThePathTheFileNames(t *testing.T) {
	m := listModelFrom(t, `kind: list
columns:
  - name: name
    fetch: /sdwan/v9.9/api/elements
    value: "{{ .label }}"
  - name: where
    header: Where
    fetch: /sdwan/v8.8/api/sites
    on: site_id
    value: "{{ .name }}"
`, 2)
	if m.columnsErr != "" {
		t.Fatalf("the file was refused: %s", m.columnsErr)
	}
	if got := m.runtime().ListPaths().Elements; got != "/sdwan/v9.9/api/elements" {
		t.Errorf("the loader would ask for %q, want the path the file names", got)
	}
	if got := m.runtime().ListPaths().Sites; got != "/sdwan/v8.8/api/sites" {
		t.Errorf("sites would come from %q", got)
	}
}

func TestAColumnOffTheElementListCostsNoCallOfItsOwn(t *testing.T) {
	m := listModelFrom(t, `kind: list
columns:
  - name: name
    fetch: /sdwan/v3.2/api/elements
    value: "{{ .label }}"
  - name: model
    header: Model
    fetch: /sdwan/v3.2/api/elements
    value: "{{ .model_name }}"
`, 2)
	if cmd := m.readFleets(); cmd != nil {
		t.Error("a column reading the element list asked the portal again")
	}
	if got := plain(columnCell(t, &m, 1, "model")); got != "ion 1200" {
		t.Errorf("model = %q, want it read off the record the list already has", got)
	}
}

func TestAThirdFleetCallIsFetchedOnceAndJoined(t *testing.T) {
	m := listModelFrom(t, `kind: list
columns:
  - name: name
    fetch: /sdwan/v3.2/api/elements
    value: "{{ .label }}"
  - name: zone
    header: Zone
    fetch: /sdwan/v2.2/api/securityzones
    key: id
    on: site_id
    value: "{{ .name }}"
`, 3)
	if m.columnsErr != "" {
		t.Fatalf("the file was refused: %s", m.columnsErr)
	}

	specs := m.fleetSpecs()
	var zone fleetSpec
	for _, f := range specs {
		if f.name == "zone" {
			zone = f
		}
	}
	if zone.path == "" {
		t.Fatalf("fleetSpecs() = %+v, want the zone column among them", specs)
	}

	portal := &fakePortal{}
	rows := []map[string]any{
		{"id": "0", "name": "store floor"},
		{"id": "1", "name": "back office"},
	}
	portal.fleet = func(string) ([]byte, error) {
		return json.Marshal(map[string]any{"count": len(rows), "items": rows})
	}

	cmd := m.readFleet(portal, zone, "1")
	if cmd == nil {
		t.Fatal("readFleet built nothing")
	}
	msg, ok := cmd().(fleetReadMsg)
	if !ok {
		t.Fatalf("message = %T, want the fleet read", msg)
	}
	next, _ := m.onFleetRead(msg)
	m = next.(tuiModel)

	if len(portal.gets) != 1 {
		t.Errorf("gets = %v, want the one call for the fleet", portal.gets)
	}
	if got := plain(columnCell(t, &m, 1, "zone")); got != "back office" {
		t.Errorf("row 1 = %q, want the zone its site_id matched", got)
	}
	if got := plain(columnCell(t, &m, 2, "zone")); got != "-" {
		t.Errorf("row 2 = %q, want a dash: nothing in the answer matched it", got)
	}
}

func TestAJoinOnSomethingAnElementHasNotGotIsRefused(t *testing.T) {
	m := listModelFrom(t, `kind: list
columns:
  - name: name
    fetch: /sdwan/v3.2/api/elements
    value: "{{ .label }}"
  - name: zone
    fetch: /sdwan/v2.2/api/securityzones
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
columns:
  - name: name
    fetch: /sdwan/v3.2/api/elements
    value: "{{ .label }}"
  - name: zone
    header: Zone
    fetch: /sdwan/v2.2/api/securityzones
    on: site_id
    value: "{{ .name }}"
`, 2)
	next, _ := m.onFleetRead(fleetReadMsg{path: "/sdwan/v2.2/api/securityzones", tsg: "1",
		err: "403 forbidden"})
	m = next.(tuiModel)

	if !strings.Contains(m.status, "403") {
		t.Errorf("status = %q, want the reason the call was refused", m.status)
	}
	if got := plain(columnCell(t, &m, 0, "zone")); got != "-" {
		t.Errorf("row 0 = %q, want a dash once the call has failed", got)
	}
	if cmd := m.readFleets(); cmd != nil {
		t.Error("it asked again after a refusal")
	}
}
