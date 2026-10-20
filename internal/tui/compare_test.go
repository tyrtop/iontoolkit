package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
)

const compareFixtureYAML = `
tenants:
  - name: Acme Co
    tsg_id: "000000000"
    users:
      - name: admin
        password: ion
    elements:
      - name: store-0003-rtr-01
        model: ion 1200
        site: Store 0003
        connected: true
        responses:
          "dump device status": |
            Name	: store-0003-rtr-01
            Model	: ion 9999
`

const compareViewYAML = `
views:
  - name: Overview
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
        - label: Name
          portal: "{{ .el.name }}"
          device: "{{ kv .cli.dev \"Name\" }}"
        - label: Model
          portal: "{{ .el.model_name }}"
          device: "{{ kv .cli.dev \"Model\" }}"
        - label: Serial
          portal: "{{ .el.serial_number }}"
          device: "{{ kv .cli.dev \"Serial\" }}"
`

func TestCompareAsksTheDeviceAndMarksEachRow(t *testing.T) {
	useViews(t, compareViewYAML)
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	if got := plain(m.dataPage(m.data[0]).String()); !strings.Contains(got, "c asks the device") {
		t.Errorf("the page does not offer the comparison:\n%s", got)
	}

	next, cmd = m.onDataKey("c")
	m = next.(tuiModel)
	if !m.data[0].cmp.loading {
		t.Fatal("c did not start a comparison")
	}
	m = runCompare(t, m, cmd)

	if m.data[0].cmp.err != "" {
		t.Fatalf("comparison failed: %s", m.data[0].cmp.err)
	}
	page := plain(m.dataPage(m.data[0]).String())
	for _, want := range []string{
		"CLI check", "dump device status",
		"CHECK", "PORTAL", "DEVICE",
		"✓ Name",
		"✗ Model",
		"· Serial",
		"ion 9999",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the comparison is missing %q:\n%s", want, page)
		}
	}
	if got := m.status; !strings.Contains(got, "1 of 3") {
		t.Errorf("status = %q, want the number of disagreements", got)
	}
}

func TestCompareWithoutADeviceLoginAsksForOne(t *testing.T) {
	useViews(t, compareViewYAML)
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	if err := m.store.Secrets.Delete(config.IONPassKey(m.tenant.TSGID, "admin")); err != nil {
		t.Fatal(err)
	}

	next, cmd = m.onDataKey("c")
	m = next.(tuiModel)
	if cmd != nil {
		t.Error("c dialled the element with no login saved")
	}
	if m.data[0].cmp.loading {
		t.Error("the tab is waiting for a comparison that was never started")
	}
	if !strings.Contains(m.status, "no ION login") {
		t.Errorf("status = %q, want it to name what is missing", m.status)
	}
}

func TestComparisonsTheElementCannotAnswerAreNotOffered(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	clustered := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: clustered.ID, name: clustered.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)
	if got := strings.Count(plain(m.dataPage(m.data[0]).String()), "CLI check"); got != 2 {
		t.Errorf("%d comparisons offered on a clustered element, want both built-ins", got)
	}

	m2, set2 := fixtureTUIWith(t, fixtureYAML)
	m2.phase = phasePicking
	m2.mode = modeData
	m2.rebuildRows()
	spoke := set2.Tenants[0].Elements[0]
	next, cmd = m2.openDataTabs([]sessionTarget{{eid: spoke.ID, name: spoke.Name, tsg: m2.tenant.TSGID}})
	m2 = runDataLoads(t, next.(tuiModel), cmd)

	page := plain(m2.dataPage(m2.data[0]).String())
	if got := strings.Count(page, "CLI check"); got != 1 {
		t.Errorf("%d comparisons offered on an unclustered element, want only the one that applies:\n%s", got, page)
	}
	applies := dataview.CompareViews(m2.views.Views, m2.data[0].vars())
	if len(dataview.CompareCommands(applies)) != 1 {
		t.Error("the HA command would still be run on an element with no cluster")
	}
}

func TestAFailedComparisonLeavesThePageReadable(t *testing.T) {
	useViews(t, compareViewYAML)
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	next, _ = m.Update(compareDoneMsg{eid: el.ID, err: "dial: expected handshake response status 101 but got 404"})
	m = next.(tuiModel)

	page := plain(m.dataPage(m.data[0]).String())
	for _, want := range []string{"store-0003-rtr-01", "CLI check", "got 404"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}
}

func useViews(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "views.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	Views = path
	t.Cleanup(func() { Views = "" })
}

func runCompare(t *testing.T, m tuiModel, cmd tea.Cmd) tuiModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	cmds := []tea.Cmd{cmd}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		cmds = batch
	}
	for _, c := range cmds {
		done, ok := c().(compareDoneMsg)
		if !ok {
			continue
		}
		next, _ := m.Update(done)
		return next.(tuiModel)
	}
	t.Fatal("nothing the keypress queued compared anything")
	return m
}
