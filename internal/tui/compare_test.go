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

// A device that answers one question the way the portal does and one it does
// not, which is the whole point of the comparison.
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

// The view the test compares with. It takes the name of a built-in, which
// replaces it: the shipped Overview comparison asks about fields the fixture
// derives from an element's name, and this one asks about the two the fixture
// file above states outright.
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

// The explicit keypress end to end: one session to the playback device, the
// portal read again beside it, and a mark per row.
func TestCompareAsksTheDeviceAndMarksEachRow(t *testing.T) {
	useViews(t, compareViewYAML)
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant.TSGID}})
	m = runDataLoads(t, next.(tuiModel), cmd)

	// Nothing has been asked of the device yet: the portal half of the mode
	// is credential-free, and the page says what the key is for.
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
		"✓ Name",   // both sides say store-0003-rtr-01
		"✗ Model",  // the portal says ion 1200, the device ion 9999
		"· Serial", // the device prints no serial, which is not a disagreement
		"ion 9999",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the comparison is missing %q:\n%s", want, page)
		}
	}
	// The marks may be below the window, so the footer carries the count.
	if got := m.status; !strings.Contains(got, "1 of 3") {
		t.Errorf("status = %q, want the number of disagreements", got)
	}
}

// A comparison logs in to the appliance, so it is the one action in this mode
// that needs a device credential, and it says so rather than dialling.
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

// The gate is checked before the dial: an element that is not clustered is
// never asked about HA, and the heading does not appear on its page either.
func TestComparisonsTheElementCannotAnswerAreNotOffered(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.mode = modeData
	m.rebuildRows()

	// The HA element of the pair, then a plain spoke in another tenant-less
	// site: the fixture file marks only the first two as clustered.
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

// A dial that fails is reported on the page rather than taking it down: the
// portal half is still good.
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

// useViews points --views at a file for the length of one test, which is also
// how a --fixtures run gets views of its own.
func useViews(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "views.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	Views = path
	t.Cleanup(func() { Views = "" })
}

// runCompare runs the comparison the keypress queued and feeds the answer
// back in, the way the runtime would when the session finishes.
func runCompare(t *testing.T, m tuiModel, cmd tea.Cmd) tuiModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	// Run them one at a time and stop at the comparison: the batch also holds
	// the spinner tick, which would sit out a frame for nothing.
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
