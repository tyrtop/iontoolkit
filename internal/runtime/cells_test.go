package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

type reader struct {
	mu      sync.Mutex
	bulkErr error
	gets    []string
	queries int
}

func (r *reader) GetJSON(_ context.Context, path string) ([]byte, error) {
	r.mu.Lock()
	r.gets = append(r.gets, path)
	r.mu.Unlock()
	if strings.HasSuffix(path, "/zones") {
		return []byte(`{"items":[{"id":"z1","name":"inside"},{"id":"z2","name":"outside"}]}`), nil
	}
	parts := strings.Split(path, "/")
	id := parts[len(parts)-2]
	if id == "bad" {
		return nil, errors.New("404 gone")
	}
	return []byte(fmt.Sprintf(`{"uptime":"up %s"}`, id)), nil
}

func (r *reader) QueryJSON(_ context.Context, _, field string, ids []string, _, _ int) ([]byte, error) {
	r.queries++
	if r.bulkErr != nil {
		return nil, r.bulkErr
	}
	var items []string
	for _, id := range ids {
		items = append(items, fmt.Sprintf(`{%q:%q,"uptime":"bulk %s"}`, field, id, id))
	}
	return []byte(`{"items":[` + strings.Join(items, ",") + `]}`), nil
}

func cellsRuntime(t *testing.T, n int, bulk bool, ids ...string) *Runtime {
	t.Helper()
	spec := `
endpoints:
  - name: uptime
    path: /sdwan/v2.0/api/elements/{element_id}/uptime
  - name: query
    method: POST
    path: /sdwan/v2.0/api/elements/query
  - name: zones
    path: /sdwan/v2.0/api/zones
columns:
  - name: uptime
    header: Up
    fetch: uptime
    value: "{{ .uptime }}"
`
	if bulk {
		spec += "    bulk: query\n    key: element_id\n"
	}
	spec += `  - name: zone
    header: Zone
    fetch: zones
    key: id
    on: id
    value: "{{ .name }}"
`
	set, err := column.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := withConfig(t)
	var rows []Element
	for i := 0; i < n; i++ {
		id := fmt.Sprint(i)
		if i < len(ids) {
			id = ids[i]
		}
		rows = append(rows, Element{Element: api.Element{ID: id, Name: fmt.Sprintf("el-%03d", i)}, TSG: "1"})
	}
	rt.SetElements(rows)
	rt.SetColumns(set.Columns)
	return rt
}

func TestABulkColumnReadsAChunkAndQueuesTheRest(t *testing.T) {
	rt := cellsRuntime(t, CellChunk+5, true)
	jobs := rt.StartCells("1")
	if len(jobs) != 1 || jobs[0].Col != "uptime" || len(jobs[0].IDs) != CellChunk+5 || !rt.CellsWorking() {
		t.Fatalf("jobs = %d, working %v", len(jobs), rt.CellsWorking())
	}
	r := &reader{}
	res := rt.FetchCellsWith(context.Background(), r, jobs[0])
	if res.Bulk != "used" || len(res.Cells) != CellChunk || len(res.Rest) != 5 || r.queries != 1 {
		t.Fatalf("first read: bulk %q, %d cells, %d left, %d queries", res.Bulk, len(res.Cells), len(res.Rest), r.queries)
	}
	next := rt.ApplyCells(res)
	if !next.Tick || len(rt.CellsQueued("uptime", "1")) != 5 {
		t.Fatalf("after: %+v queued %d", next, len(rt.CellsQueued("uptime", "1")))
	}
	job, ok := rt.NextCells("uptime", "1")
	if !ok || len(job.IDs) != 5 {
		t.Fatalf("next = %+v %v", job, ok)
	}
	rt.ApplyCells(rt.FetchCellsWith(context.Background(), r, job))
	if done, total := rt.CellsProgress("uptime"); done != total || total != CellChunk+5 || rt.CellsWorking() {
		t.Errorf("progress %d/%d, working %v", done, total, rt.CellsWorking())
	}
	if c, ok := rt.Cell("uptime", "1/3"); !ok || c.Text != "bulk 3" {
		t.Errorf("cell = %+v %v", c, ok)
	}
}

func TestARefusedBulkQueryFallsBackOneByOne(t *testing.T) {
	rt := cellsRuntime(t, 3, true, "a", "bad", "c")
	r := &reader{bulkErr: errors.New("403 not for this tenant")}
	res := rt.FetchCellsWith(context.Background(), r, rt.StartCells("1")[0])
	rt.ApplyCells(res)
	if !strings.Contains(rt.CellsBulk("uptime"), "403") || len(r.gets) != 3 {
		t.Errorf("bulk %q, gets %v", rt.CellsBulk("uptime"), r.gets)
	}
	if c, _ := rt.Cell("uptime", "1/bad"); !c.Failed || !strings.Contains(rt.CellsErr("uptime"), "404") {
		t.Errorf("bad = %+v, err %q", c, rt.CellsErr("uptime"))
	}
	if c, _ := rt.Cell("uptime", "1/a"); c.Text != "up a" {
		t.Errorf("a = %+v", c)
	}
}

func TestOnlyACellReadThisSessionIsLeftAlone(t *testing.T) {
	rt := cellsRuntime(t, 3, false)
	rt.SetCachedCells("1", map[string]map[string]string{"uptime": {"0": "up 0 earlier"}})
	jobs := rt.StartCells("1")
	if len(jobs) != 1 || strings.Join(jobs[0].IDs, ",") != "0,1,2" {
		t.Errorf("jobs = %+v, want the cached cell read again too", jobs)
	}
	if c, _ := rt.Cell("uptime", "1/0"); !c.Cached || c.Text != "up 0 earlier" {
		t.Errorf("cached cell = %+v, want it shown while it is read", c)
	}
	if again := rt.StartCells("1"); len(again) != 0 {
		t.Errorf("a second start while the first reads asked again: %+v", again)
	}
	rt.ApplyCells(CellResult{Col: "uptime", TSG: "1", Cells: map[string]Cell{"1/0": {Text: "up 0"}, "1/1": {Text: "up 1"}, "1/2": {Failed: true}}})
	if again := rt.StartCells("1"); len(again) != 1 || strings.Join(again[0].IDs, ",") != "2" {
		t.Errorf("the next load asked for %+v, want only the failed cell", again)
	}

	if err := rt.Store().SaveCache(&config.ElementCache{TSGID: "1", FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rt.SaveCells("1", "uptime", map[string]Cell{"1/1": {Text: "up 1"}, "1/2": {Failed: true}})
	c, err := rt.Store().Cache("1")
	if err != nil || c.Cells["uptime"]["1"] != "up 1" || c.Cells["uptime"]["2"] != "" {
		t.Errorf("saved cells = %+v %v", c, err)
	}
}

func TestATableColumnIsOneReadPerTenantJoinedByKey(t *testing.T) {
	rt := cellsRuntime(t, 2, false)
	jobs := rt.TablesWanted("1")
	if len(jobs) != 1 || jobs[0].Path != "/sdwan/v2.0/api/zones" || !rt.TableWorking("zones") {
		t.Fatalf("jobs = %+v", jobs)
	}
	if again := rt.TablesWanted("1"); len(again) != 0 {
		t.Error("a table already being read was asked for again")
	}
	rt.ApplyTable(rt.ReadTableWith(context.Background(), &reader{}, jobs[0]))
	rows := rt.Table("zones", "1")
	if rows["z2"]["name"] != "outside" || rt.TableWorking("zones") {
		t.Errorf("rows = %v", rows)
	}
}

func TestARowsTextComesFromTheColumnSpecs(t *testing.T) {
	rt := New()
	list, err := column.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	rt.SetColumns(list.Columns)
	el := Element{Element: api.Element{ID: "1", Name: "s0001-rtr-01", Model: "ion 1200", SiteID: "9",
		Connected: true, Serial: "SN1"}, TSG: "111", Tenant: "Acme Co"}
	rt.SetElements([]Element{el})
	rt.MergeSites(map[string]string{"111/9": "Store 0001"})

	got := map[string]string{}
	texts := rt.RowText(list.Columns, el)
	for i, c := range list.Columns {
		got[c.Name] = texts[i]
	}
	for name, want := range map[string]string{"name": "s0001-rtr-01", "site": "Store 0001", "model": "ion 1200",
		"sn": "SN1", "state": "connected", "tenant": "Acme Co"} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	if w := rt.ListWhen(); w["serials"] != true || w["clustered"] != false || w["tenants"] != 1 {
		t.Errorf("ListWhen() = %v", w)
	}
}
