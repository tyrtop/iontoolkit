package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tyrtop.com/iontk/internal/app"

	"tyrtop.com/iontk/internal/ui"
)

func listModelFrom(t *testing.T, yaml string, n int) tuiModel {
	t.Helper()
	path := filepath.Join(t.TempDir(), "columns.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	app.ColumnsFile = path
	t.Cleanup(func() { app.ColumnsFile = "" })
	return listModel(t, n)
}

func columnNames(m *tuiModel) []string {
	out := make([]string, 0, len(m.listCols))
	for _, c := range m.listCols {
		out = append(out, c.name)
	}
	return out
}

func TestAFileNamesTheColumnsAndTheirOrder(t *testing.T) {
	m := listModelFrom(t, "columns:\n  - state\n  - name\n  - tenant\n", 4)

	want := []string{colCaret, colState, colName, colTenant}
	if got := columnNames(&m); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("columns = %v, want %v", got, want)
	}
	if got := m.listCols[1].title; got != "State" {
		t.Errorf("the state column is headed %q, want the built-in's title", got)
	}
	if m.columnsErr != "" {
		t.Errorf("columnsErr = %q, want the file to have loaded", m.columnsErr)
	}
}

func TestAFileGetsTheColumnItAsksForWhateverTheFleetLooksLike(t *testing.T) {
	m := listModelFrom(t, "columns:\n  - name\n  - tenant\n", 2)
	if tenantCountOf(m) > 1 {
		t.Fatal("this fleet is one tenant, so the built-in set would hide the column")
	}
	if got := columnNames(&m); !strings.Contains(strings.Join(got, " "), colTenant) {
		t.Errorf("columns = %v, want the tenant column the file named", got)
	}
}

func TestAColumnFromAFileReadsTheRecordByItsPortalName(t *testing.T) {
	m := listModelFrom(t, `columns:
  - name
  - name: version
    header: Software
    value: "{{ .software_version }}"
  - name: where
    header: Where
    from: site
    value: "{{ .name }}"
`, 2)
	rows := append([]elementRow(nil), m.rows()...)
	rows[0].Software = "6.3.4-b2"
	m.runtime().MergeSites(map[string]string{"1/0": "Store 0000"})
	m.setElements(rows)

	if got := plain(columnCell(t, &m, 0, "version")); got != "6.3.4-b2" {
		t.Errorf("the version column = %q, want the field off the element record", got)
	}
	if got := plain(columnCell(t, &m, 0, "where")); got != "Store 0000" {
		t.Errorf("the site column = %q, want the site's own name", got)
	}
	if got := m.listCols[2].title; got != "Software" {
		t.Errorf("header = %q, want the one the file gave", got)
	}
}

func TestAStyleRuleFromAFileColoursTheCell(t *testing.T) {
	m := listModelFrom(t, `columns:
  - name
  - name: link
    header: Link
    value: "{{ if .connected }}up{{ else }}down{{ end }}"
    style:
      - when: "{{ not .connected }}"
        role: err
      - role: ok
`, 3)
	m.moveCursorTo(2)

	if got := columnCell(t, &m, 1, "link"); got != ui.StyleErr.Render("down") {
		t.Errorf("an offline row = %q, want it in the error colour", got)
	}
	if got := columnCell(t, &m, 0, "link"); got != ui.StyleOK.Render("up") {
		t.Errorf("a connected row = %q, want the rule that always matches", got)
	}
	if got := columnCell(t, &m, 2, "link"); got != plain(got) {
		t.Errorf("the row under the cursor = %q, want it plain", got)
	}
}

func TestTheMarkAndTheCaretSurviveWhateverAFileNames(t *testing.T) {
	m := listModelFrom(t, "columns:\n  - site\n  - model\n", 3)

	if m.listCols[0].name != colCaret {
		t.Fatalf("columns = %v, want the caret in front of them", columnNames(&m))
	}
	if !m.listCols[1].mark {
		t.Fatalf("the mark is on %v, want it on the first column with a value", columnNames(&m))
	}

	before := plain(columnCell(t, &m, 1, colSite))
	selectOnly(&m, "1/1")
	m.rebuildRows()
	after := plain(columnCell(t, &m, 1, colSite))

	if !strings.HasPrefix(after, selectedMark) {
		t.Errorf("a ticked row reads %q, want the bar against it", after)
	}
	if ui.Width(before) != ui.Width(after) {
		t.Errorf("the mark moved the value: %q then %q", before, after)
	}
}

func TestAColumnIontkDoesNotDrawLeavesTheListAsItWas(t *testing.T) {
	m := listModelFrom(t, "columns:\n  - name\n  - uptime\n", 2)

	if m.columnsErr == "" {
		t.Fatal("a column iontk does not draw loaded without a word about it")
	}
	for _, want := range []string{"uptime", colSerial} {
		if !strings.Contains(m.columnsErr, want) {
			t.Errorf("columnsErr = %q, want it to name %q", m.columnsErr, want)
		}
	}
	if m.listSpec != nil {
		t.Error("the refused file was taken anyway")
	}
	if got := columnNames(&m); !strings.Contains(strings.Join(got, " "), colStatus) {
		t.Errorf("columns = %v, want the set iontk ships", got)
	}

	if got := plain(m.screensLine()); !strings.Contains(got, "uptime") {
		t.Errorf("settings says %q, want the reason the file was refused", got)
	}
}

func TestAFileThatNamesTheNameColumnKeepsTheMarkOnIt(t *testing.T) {
	m := listModelFrom(t, "columns:\n  - site\n  - name\n", 3)
	for _, c := range m.listCols {
		if c.mark && c.name != colName {
			t.Errorf("the mark is on %q, want it on the name the list is read by", c.name)
		}
	}
}
