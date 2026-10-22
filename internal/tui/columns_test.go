package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

func listModel(t *testing.T, n int) tuiModel {
	t.Helper()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "Acme Co", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}},
	}}}
	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true

	els := make([]api.Element, 0, n)
	for i := range n {
		els = append(els, api.Element{
			ID:        fmt.Sprint(i),
			Name:      fmt.Sprintf("store-%04d-rtr-01", i),
			Model:     "ion 1200",
			Serial:    fmt.Sprintf("ION1200%06d", i),
			SiteID:    fmt.Sprint(i),
			Connected: i%2 == 0,
			SpokeHA:   &api.SpokeHA{ClusterID: fmt.Sprint(i), Enable: true},
		})
	}
	setTestElements(&m, els)
	m.phase = phasePicking
	m.width, m.height = 160, 24
	m.rebuildRows()
	return m
}

func columnCell(t *testing.T, m *tuiModel, row int, name string) string {
	t.Helper()
	for i, c := range m.listCols {
		if c.name == name {
			return m.table.Rows()[row][i]
		}
	}
	t.Fatalf("no %q column in %v", name, m.listCols)
	return ""
}

func TestTheHAColumnKeepsItsColour(t *testing.T) {
	m := listModel(t, 4)
	m.ha = map[string]string{"1/1": haActive, "1/2": haStandby}
	m.haCached = map[string]string{"1/3": haActive}
	m.forgetRows()
	m.rebuildRows()

	want := map[int]string{
		1: ui.StyleOK.Bold(true).Render(haActive),
		2: ui.StyleElement.Render(haStandby),
		3: ui.StyleOK.Bold(true).Render(haActive) + ui.StyleDim.Render("*"),
	}
	for row, w := range want {
		if got := columnCell(t, &m, row, colHA); got != w {
			t.Errorf("row %d ha = %q, want %q", row, got, w)
		}
	}
	if got := columnCell(t, &m, 0, colHA); got != plain(got) {
		t.Errorf("the row under the cursor keeps its own colour: %q", got)
	}
}

func TestTheStatusGlyphKeepsItsColour(t *testing.T) {
	m := listModel(t, 4)
	m.dialing = map[string]dialState{"1": {failed: true}}
	m.forgetRows()
	m.rebuildRows()

	got := columnCell(t, &m, 1, colStatus)
	if plain(got) != plain(ui.MarkCross.String()) {
		t.Fatalf("a failed dial reads %q, want the cross", plain(got))
	}
	if !strings.Contains(got, ui.StyleErr.Render(plain(got))) {
		t.Errorf("the cross is %q, want it in the error colour", got)
	}
}

func TestACachedRowFollowsTheStateBehindIt(t *testing.T) {
	m, set := fixtureTUIWith(t, haFixtureYAML)
	m.phase = phasePicking
	m.rebuildRows()

	first := set.Tenants[0].Elements[0]
	at := -1
	for i, n := range m.matches {
		if m.all[n].ID == first.ID {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no row for %s", first.ID)
	}
	if got := plain(columnCell(t, &m, at, colHA)); got != "loading" {
		t.Fatalf("ha before the sweep = %q, want loading", got)
	}

	cmd := loadHA(t.Context(), m.pool.get(m.tenant.TSGID), m.tenant.TSGID, haClusters(elementsOf(m)).pairs)
	msg, ok := cmd().(haStatesMsg)
	if !ok {
		t.Fatalf("message = %T, want the HA states", msg)
	}
	next, _ := m.Update(msg)
	m = next.(tuiModel)

	if got := plain(columnCell(t, &m, at, colHA)); got != haActive && got != haStandby {
		t.Errorf("ha after the sweep = %q, want the state the sweep read", got)
	}
}

func TestACachedRowFollowsTheSiteName(t *testing.T) {
	m := listModel(t, 2)
	if got := plain(columnCell(t, &m, 0, colSite)); got != "0" {
		t.Fatalf("site without a name = %q, want the raw id", got)
	}

	m.sites = map[string]string{"1/0": "Store 0000", "1/1": "Store 0001"}
	m.setElements(m.all)

	if got := plain(columnCell(t, &m, 0, colSite)); got != "Store 0000" {
		t.Errorf("site = %q, want the name that has since landed", got)
	}
}

func TestEveryColumnSaysWhatItDraws(t *testing.T) {
	cols, err := defaultColumns()
	if err != nil {
		t.Fatalf("the shipped elements.yaml does not load: %v", err)
	}
	if len(cols) != 9 {
		t.Fatalf("%d columns in the shipped file, want nine", len(cols))
	}

	seen := map[string]bool{}
	for _, c := range cols {
		if seen[c.name] {
			t.Errorf("two columns named %q", c.name)
		}
		seen[c.name] = true
		if c.name == "" {
			t.Error("a column with no name cannot be picked by a file")
		}
		if c.value == nil && c.paint == nil {
			t.Errorf("%q draws nothing", c.name)
		}
		if c.min > c.max {
			t.Errorf("%q has a minimum wider than its maximum", c.name)
		}
	}
}

func TestANarrowTerminalKeepsEveryColumnInside(t *testing.T) {
	m := listModel(t, 40)
	for _, w := range []int{80, 100, 160} {
		m.width = w
		m.rebuildRows()
		total := 0
		for _, c := range m.listWidths {
			total += c + colGap
		}
		if total > w {
			t.Errorf("columns total %d on a %d-column terminal", total, w)
		}
		for _, row := range m.table.Rows() {
			for i, c := range row {
				if got := ui.Width(plain(c)); got > m.listWidths[i] {
					t.Errorf("%s cell %q is %d wide, want %d",
						m.listCols[i].name, plain(c), got, m.listWidths[i])
				}
			}
		}
	}
}

func TestTheMarkOnATickedRowStaysTwoColumnsWide(t *testing.T) {
	m := listModel(t, 4)
	before := plain(columnCell(t, &m, 1, colName))
	m.selected = map[string]bool{"1/1": true}
	m.rebuildRows()
	after := plain(columnCell(t, &m, 1, colName))

	if !strings.HasPrefix(after, selectedMark) {
		t.Errorf("a ticked name is %q, want the bar against it", after)
	}
	if ui.Width(before) != ui.Width(after) {
		t.Errorf("the mark moved the name: %q then %q", before, after)
	}
}
