package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type fakePortal struct {
	mu      sync.Mutex
	gets    []string
	queries [][]string
	bulkErr error
	getErr  map[string]error
	row     func(id string) map[string]any
	fleet   func(path string) ([]byte, error)
}

func (f *fakePortal) GetJSON(_ context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	f.gets = append(f.gets, path)
	f.mu.Unlock()
	if err := f.getErr[path]; err != nil {
		return nil, err
	}
	if f.fleet != nil {
		return f.fleet(path)
	}
	id := path[strings.LastIndex(strings.TrimSuffix(path, "/status"), "/")+1:]
	id = strings.TrimSuffix(id, "/status")
	return json.Marshal(f.row(id))
}

func (f *fakePortal) QueryJSON(_ context.Context, _, _ string, ids []string, _, _ int) ([]byte, error) {
	f.mu.Lock()
	f.queries = append(f.queries, ids)
	f.mu.Unlock()
	if f.bulkErr != nil {
		return nil, f.bulkErr
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, f.row(id))
	}
	return json.Marshal(map[string]any{"count": len(items), "items": items})
}

func uptimeRow(id string) map[string]any {
	return map[string]any{"element_id": id, "uptime": "up " + id}
}

const uptimeColumn = `kind: list
endpoints:
  - name: status
    path: /sdwan/v2.6/api/elements/{element_id}/status
  - name: query
    method: POST
    path: /sdwan/v2.6/api/elements/status/query
columns:
  - name
  - name: uptime
    header: Uptime
    fetch: status
    bulk: query
    value: "{{ .uptime }}"
`

func fetchModel(t *testing.T, n int) (tuiModel, *fakePortal) {
	t.Helper()
	m := listModelFrom(t, uptimeColumn, n)
	if m.columnsErr != "" {
		t.Fatalf("the file was refused: %s", m.columnsErr)
	}
	fetches := false
	for _, c := range m.listCols {
		fetches = fetches || (c.name == "uptime" && c.fetch != nil)
	}
	if !fetches {
		t.Fatalf("columns = %v, want one that fetches", columnNames(&m))
	}
	return m, &fakePortal{row: uptimeRow}
}

func runFetch(t *testing.T, m tuiModel, p *fakePortal, ids ...string) tuiModel {
	t.Helper()
	rt := m.runtime()
	rt.ApplyCells(rt.FetchCellsWith(t.Context(), p, runtime.CellJob{Col: "uptime", TSG: "1", IDs: ids}))
	return settle(t, m)
}

func TestAFetchedColumnFillsFromOneQuery(t *testing.T) {
	m, portal := fetchModel(t, 3)
	if got := plain(columnCell(t, &m, 0, "uptime")); got != "loading" && got != "-" {
		t.Errorf("before the sweep the cell reads %q", got)
	}

	m = runFetch(t, m, portal, "0", "1", "2")

	if len(portal.queries) != 1 || len(portal.queries[0]) != 3 {
		t.Fatalf("queries = %v, want the three elements in one", portal.queries)
	}
	if len(portal.gets) != 0 {
		t.Errorf("it asked per element as well: %v", portal.gets)
	}
	for row := range 3 {
		if got := plain(columnCell(t, &m, row, "uptime")); got != "up "+fmt.Sprint(row) {
			t.Errorf("row %d = %q, want the value keyed back by element_id", row, got)
		}
	}
}

func TestAQueryTheTenantRefusesFallsBackToOnePerElement(t *testing.T) {
	m, portal := fetchModel(t, 3)
	portal.bulkErr = fmt.Errorf("403 forbidden")

	m = runFetch(t, m, portal, "0", "1", "2")

	if len(portal.gets) != 3 {
		t.Fatalf("gets = %v, want one per element", portal.gets)
	}
	if got := portal.gets[0]; !strings.HasPrefix(got, "/sdwan/v2.6/api/elements/") {
		t.Errorf("path = %q, want the ids filled in", got)
	}
	if got := plain(columnCell(t, &m, 1, "uptime")); got != "up 1" {
		t.Errorf("row 1 = %q, want the value the fallback read", got)
	}
	if m.runtime().CellsBulk("uptime") == "" || m.runtime().CellsBulk("uptime") == "used" {
		t.Errorf("fetchBulk = %q, want the reason the query was refused", m.runtime().CellsBulk("uptime"))
	}
}

func TestAnElementTheAnswerSkippedReadsAsNothing(t *testing.T) {
	m, portal := fetchModel(t, 3)
	portal.row = func(id string) map[string]any {
		if id == "1" {
			return map[string]any{}
		}
		return uptimeRow(id)
	}

	m = runFetch(t, m, portal, "0", "1", "2")

	if got := plain(columnCell(t, &m, 1, "uptime")); got != "-" {
		t.Errorf("row 1 = %q, want a dash: the query answered and said nothing about it", got)
	}
	if got := plain(columnCell(t, &m, 0, "uptime")); got != "up 0" {
		t.Errorf("row 0 = %q, want its own value", got)
	}
}

func TestAnElementTheCallRefusedReadsAsAQuestion(t *testing.T) {
	m, portal := fetchModel(t, 2)
	portal.bulkErr = fmt.Errorf("the query is not for this tenant")
	portal.getErr = map[string]error{
		"/sdwan/v2.6/api/elements/1/status": fmt.Errorf("404 no such element"),
	}

	m = runFetch(t, m, portal, "0", "1")

	if got := plain(columnCell(t, &m, 1, "uptime")); got != "?" {
		t.Errorf("row 1 = %q, want a question: that element's call failed", got)
	}
	if got := columnCell(t, &m, 1, "uptime"); got != ui.StyleWarn.Render("?") {
		t.Errorf("the question is %q, want it warn-coloured", got)
	}
	if !strings.Contains(m.runtime().CellsErr("uptime"), "404") {
		t.Errorf("fetchErr = %q, want the reason kept", m.runtime().CellsErr("uptime"))
	}
}

func TestLastRunsValuesShowWithAnAsteriskUntilTheyAreRead(t *testing.T) {
	m, portal := fetchModel(t, 2)
	m.runtime().SetCachedCells("1", map[string]map[string]string{"uptime": {"0": "up 0 earlier"}})
	m.forgetRows()
	m.rebuildRows()
	m.moveCursorTo(1)

	if got := plain(columnCell(t, &m, 0, "uptime")); got != "up 0 earlier*" {
		t.Errorf("row 0 = %q, want last run's value with the asterisk", got)
	}
	if got := columnCell(t, &m, 0, "uptime"); !strings.Contains(got, ui.StyleDim.Render("*")) {
		t.Errorf("row 0 = %q, want a dim asterisk", got)
	}

	m = runFetch(t, m, portal, "0")
	if got := plain(columnCell(t, &m, 0, "uptime")); got != "up 0" {
		t.Errorf("row 0 = %q, want the value this run read", got)
	}
}

func TestTheRestOfTheFleetWaitsForATick(t *testing.T) {
	m, portal := fetchModel(t, 260)
	rt := m.runtime()

	ids := make([]string, 0, 260)
	for i := range 260 {
		ids = append(ids, fmt.Sprint(i))
	}
	res := rt.FetchCellsWith(t.Context(), portal, runtime.CellJob{Col: "uptime", TSG: "1", IDs: ids})
	if len(res.Cells) != fetchChunkSize {
		t.Fatalf("%d cells in the first chunk, want %d", len(res.Cells), fetchChunkSize)
	}
	if len(res.Rest) != 60 {
		t.Fatalf("%d left over, want 60", len(res.Rest))
	}

	next := rt.ApplyCells(res)
	if len(rt.CellsQueued("uptime", "1")) != 60 {
		t.Error("the rest of the fleet was not queued")
	}
	if !next.Tick {
		t.Fatal("nothing was scheduled for the rest")
	}
	if !m.fetchWorking() {
		t.Error("fetchWorking() is false with a queue outstanding")
	}
}

func TestTheRowsOnScreenAreAskedForFirst(t *testing.T) {
	m, _ := fetchModel(t, 400)
	m.moveCursorTo(300)

	ids := make([]string, 0, 400)
	for i := range 400 {
		ids = append(ids, fmt.Sprint(i))
	}
	first := m.runtime().NearestElements("1", ids)

	at := -1
	for i, id := range first[:40] {
		if id == "300" {
			at = i
		}
	}
	if at < 0 {
		t.Errorf("the row under the cursor is not in the first 40 asked for: %v", first[:40])
	}
}

func TestTheNextChunkStartsAtTheCursor(t *testing.T) {
	m, portal := fetchModel(t, 260)
	rt := m.runtime()
	ids := make([]string, 0, 260)
	for i := range 260 {
		ids = append(ids, fmt.Sprint(i))
	}
	rt.ApplyCells(rt.FetchCellsWith(t.Context(), portal, runtime.CellJob{Col: "uptime", TSG: "1", IDs: ids}))
	m.moveCursorTo(250)

	job, ok := rt.NextCells("uptime", "1")
	if !ok || job.IDs[0] != "250" {
		t.Errorf("next chunk = %v, want it to start at the row under the cursor", job.IDs)
	}
	if !rt.CellsOut("uptime", "1") {
		t.Error("the column is not marked as in flight, so it could be asked twice")
	}
}

func TestAFetchedColumnIsRefusedWhenItCannotBeDrawn(t *testing.T) {
	for _, tt := range []struct{ name, yaml, want string }{
		{"a path off the api", "kind: list\nendpoints:\n  - name: x\n    path: /api/x\ncolumns:\n  - name\n  - name: x\n    fetch: x\n    value: \"{{ .x }}\"\n", "/sdwan/"},
		{"no value", "kind: list\nendpoints:\n  - name: x\n    path: /sdwan/v2.0/api/x\ncolumns:\n  - name\n  - name: x\n    fetch: x\n", "says no value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := listModelFrom(t, tt.yaml, 2)
			if m.columnsErr == "" {
				t.Fatalf("%q loaded", tt.yaml)
			}
			if !strings.Contains(m.columnsErr, tt.want) {
				t.Errorf("columnsErr = %q, want it to name %q", m.columnsErr, tt.want)
			}
		})
	}
}

func TestTheSweepSaysWhereItIsWhileItRuns(t *testing.T) {
	m, _ := fetchModel(t, 300)
	m.runtime().SetCellsProgress("uptime", "1", 300, 200)
	m.runtime().MarkCellsOut("uptime", "1")

	note := plain(m.cellsNote())
	if !strings.Contains(note, "uptime") || !strings.Contains(note, "200/300") {
		t.Errorf("the note reads %q, want the column and how far it has got", note)
	}
	if lines := strings.Count(plain(m.refreshNote()), "\n"); lines != 1 {
		t.Errorf("the refresh note is %d lines, want the frame height to hold", lines+1)
	}
}

func TestADroppedTenantLeavesTheColumnAlone(t *testing.T) {
	m, portal := fetchModel(t, 2)
	rt := m.runtime()
	rt.ApplyCells(rt.FetchCellsWith(t.Context(), nil, runtime.CellJob{Col: "uptime", TSG: "1", IDs: []string{"0"}}))
	m = settle(t, m)

	if got := plain(columnCell(t, &m, 0, "uptime")); got != "?" {
		t.Errorf("row 0 = %q, want a question when there is no client", got)
	}
	if len(portal.gets)+len(portal.queries) != 0 {
		t.Error("it called the portal anyway")
	}
	if _, ok := column.Builtin(); ok != nil {
		t.Fatal("the shipped columns stopped loading")
	}
}

var _ = tea.Batch

func TestTheListSaysWhyAColumnIsSlow(t *testing.T) {
	m, portal := fetchModel(t, 3)
	portal.bulkErr = fmt.Errorf("403 not for this tenant")
	m.runtime().SetCellsProgress("uptime", "1", 3, 0)

	m = runFetch(t, m, portal, "0", "1", "2")

	if !strings.Contains(m.status, "one call per element") || !strings.Contains(m.status, "403") {
		t.Errorf("status = %q, want the fallback and the reason", m.status)
	}
}
