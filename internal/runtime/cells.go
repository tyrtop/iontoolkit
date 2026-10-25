package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/logging"
)

const (
	CellChunk   = 200
	cellWorkers = 8
	CellBatch   = 16

	AreaCells = "cells"
)

type Cell struct {
	Text   string
	Cached bool
	Failed bool
}

type CellResult struct {
	Col   string
	TSG   string
	Cells map[string]Cell
	Bulk  string
	Err   string
	Rest  []string
}

type CellJob struct {
	Col string
	TSG string
	IDs []string
}

type cellsState struct {
	specs  []column.Spec
	cells  map[string]map[string]Cell
	queue  map[string][]string
	out    map[string]bool
	done   map[string]int
	total  map[string]int
	err    map[string]string
	bulk   map[string]string
	tables map[string]map[string]map[string]any
	tOut   map[string]bool
	tErr   map[string]string
}

func cellKey(col, tsg string) string { return col + "\x00" + tsg }

func (rt *Runtime) editCells(edit func(s *cellsState)) {
	rt.mu.Lock()
	s := &rt.cells
	if s.cells == nil {
		s.cells, s.queue, s.out = map[string]map[string]Cell{}, map[string][]string{}, map[string]bool{}
		s.done, s.total, s.err, s.bulk = map[string]int{}, map[string]int{}, map[string]string{}, map[string]string{}
		s.tables, s.tOut, s.tErr = map[string]map[string]map[string]any{}, map[string]bool{}, map[string]string{}
	}
	edit(s)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaCells})
}

func (rt *Runtime) SetColumns(specs []column.Spec) {
	rt.editCells(func(s *cellsState) { s.specs = specs })
	rt.changed(Event{Area: AreaCells, Rows: true})
}

func (rt *Runtime) Columns() []column.Spec {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return append([]column.Spec(nil), rt.cells.specs...)
}

func (rt *Runtime) fetchColumn(name string) (column.Spec, bool) {
	for _, c := range rt.Columns() {
		if c.Name == name && c.PerElement() {
			return c, true
		}
	}
	return column.Spec{}, false
}

func (rt *Runtime) Cell(col, key string) (Cell, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	c, ok := rt.cells.cells[col][key]
	return c, ok
}

func (rt *Runtime) SetCells(col string, cells map[string]Cell) {
	rt.editCells(func(s *cellsState) {
		if s.cells[col] == nil {
			s.cells[col] = map[string]Cell{}
		}
		for k, c := range cells {
			s.cells[col][k] = c
		}
	})
	rt.changed(Event{Area: AreaCells, Rows: true})
}

func (rt *Runtime) SetCachedCells(tsg string, saved map[string]map[string]string) {
	if len(saved) == 0 {
		return
	}
	rt.editCells(func(s *cellsState) {
		for col, cells := range saved {
			if s.cells[col] == nil {
				s.cells[col] = map[string]Cell{}
			}
			for eid, text := range cells {
				key := tsg + "/" + eid
				if _, have := s.cells[col][key]; !have {
					s.cells[col][key] = Cell{Text: text, Cached: true}
				}
			}
		}
	})
	rt.changed(Event{Area: AreaCells, Rows: true})
}

func (rt *Runtime) CellsWorking() bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, out := range rt.cells.out {
		if out {
			return true
		}
	}
	for _, left := range rt.cells.queue {
		if len(left) > 0 {
			return true
		}
	}
	return false
}

func (rt *Runtime) CellsProgress(col string) (done, total int) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for at, n := range rt.cells.total {
		if strings.HasPrefix(at, col+"\x00") {
			total += n
			done += rt.cells.done[at]
		}
	}
	return done, total
}

func (rt *Runtime) CellsQueued(col, tsg string) []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return append([]string(nil), rt.cells.queue[cellKey(col, tsg)]...)
}

func (rt *Runtime) CellsOut(col, tsg string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cells.out[cellKey(col, tsg)]
}

func (rt *Runtime) MarkCellsOut(col, tsg string) {
	rt.editCells(func(s *cellsState) { s.out[cellKey(col, tsg)] = true })
}

func (rt *Runtime) StartCells(tsg string) []CellJob {
	rows := rt.Elements()
	var jobs []CellJob
	for _, col := range rt.Columns() {
		if !col.PerElement() {
			continue
		}
		var ids []string
		for _, el := range rows {
			if el.TSG != tsg {
				continue
			}
			if c, have := rt.Cell(col.Name, el.Key()); have && !c.Cached && !c.Failed {
				continue
			}
			ids = append(ids, el.ID)
		}
		if len(ids) == 0 {
			continue
		}
		at, start := cellKey(col.Name, tsg), false
		rt.editCells(func(s *cellsState) {
			if s.out[at] || len(s.queue[at]) > 0 {
				return
			}
			s.total[at], s.done[at], s.out[at], start = len(ids), 0, true, true
		})
		if start {
			jobs = append(jobs, CellJob{Col: col.Name, TSG: tsg, IDs: rt.NearestElements(tsg, ids)})
		}
	}
	return jobs
}

func (rt *Runtime) NextCells(col, tsg string) (CellJob, bool) {
	left := rt.CellsQueued(col, tsg)
	if len(left) == 0 {
		return CellJob{}, false
	}
	if _, ok := rt.fetchColumn(col); !ok {
		return CellJob{}, false
	}
	rt.MarkCellsOut(col, tsg)
	return CellJob{Col: col, TSG: tsg, IDs: rt.NearestElements(tsg, left)}, true
}

func (rt *Runtime) FetchCells(ctx context.Context, job CellJob) CellResult {
	var r CellReader
	if c := rt.Clients().Get(job.TSG); c != nil {
		r = c
	}
	return rt.FetchCellsWith(ctx, r, job)
}

func (rt *Runtime) FetchCellsWith(ctx context.Context, c CellReader, job CellJob) CellResult {
	res := CellResult{Col: job.Col, TSG: job.TSG}
	col, ok := rt.fetchColumn(job.Col)
	if !ok || len(job.IDs) == 0 {
		return res
	}
	if c == nil {
		res.Err, res.Cells = "no client for this tenant", map[string]Cell{}
		for _, id := range job.IDs {
			res.Cells[job.TSG+"/"+id] = Cell{Failed: true}
		}
		return res
	}
	ask, rest := job.IDs, []string(nil)
	if len(ask) > CellChunk {
		ask, rest = job.IDs[:CellChunk], job.IDs[CellChunk:]
	}
	byID := rt.elementsByID(job.TSG, ask)

	if col.Bulk != "" {
		cells, err := bulkCells(ctx, c, col, job.TSG, ask)
		if err == nil {
			res.Cells, res.Bulk, res.Rest = cells, "used", rest
			return res
		}
		logging.L().Warn("column falling back to one call per element",
			"column", col.Name, "tsg", job.TSG, "asked", len(ask), "err", err)
		res.Cells, res.Err = rt.oneByOne(ctx, c, col, job.TSG, ask, byID)
		res.Bulk, res.Rest = rt.explain(err), rest
		return res
	}
	take, later := ask, rest
	if len(take) > CellBatch {
		take, later = take[:CellBatch], append(take[CellBatch:], rest...)
	}
	res.Cells, res.Err = rt.oneByOne(ctx, c, col, job.TSG, take, byID)
	res.Rest = later
	return res
}

type CellReader interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
	QueryJSON(ctx context.Context, path, field string, ids []string, limit, page int) ([]byte, error)
}

func bulkCells(ctx context.Context, r CellReader, col column.Spec, tsg string, ids []string) (map[string]Cell, error) {
	body, err := r.QueryJSON(ctx, col.BulkEndpoint.Path, col.Key, ids, CellChunk, 1)
	if err != nil {
		return nil, err
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode %s: %w", col.Bulk, err)
	}
	cells := make(map[string]Cell, len(ids))
	for _, id := range ids {
		cells[tsg+"/"+id] = Cell{}
	}
	named := 0
	for _, row := range env.Items {
		id, _ := row[col.Key].(string)
		if id == "" {
			continue
		}
		named++
		cells[tsg+"/"+id] = Cell{Text: col.Template.Render(row)}
	}
	if named == 0 {
		return nil, fmt.Errorf("%s answered %d row(s), none of them with a %s", col.Bulk, len(env.Items), col.Key)
	}
	return cells, nil
}

func (rt *Runtime) oneByOne(ctx context.Context, r CellReader, col column.Spec, tsg string, ids []string, byID map[string]Element) (map[string]Cell, string) {
	var mu sync.Mutex
	cells := make(map[string]Cell, len(ids))
	first := ""
	var wg sync.WaitGroup
	work := make(chan string)
	for range min(cellWorkers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				text, err := rt.oneCell(ctx, r, col, byID[id])
				mu.Lock()
				if err != nil {
					cells[tsg+"/"+id] = Cell{Failed: true}
					if first == "" {
						first = err.Error()
					}
				} else {
					cells[tsg+"/"+id] = Cell{Text: text}
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		work <- id
	}
	close(work)
	wg.Wait()
	return cells, first
}

func (rt *Runtime) oneCell(ctx context.Context, r CellReader, col column.Spec, el Element) (string, error) {
	path, err := col.Endpoint.Fill(ElementVars(el))
	if err != nil {
		return "", err
	}
	body, err := r.GetJSON(ctx, path)
	if err != nil {
		return "", fmt.Errorf("%s: %s", col.Name, rt.explain(err))
	}
	var answer any
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("%s: decode %s: %s", col.Name, path, err)
	}
	row, ok := answer.(map[string]any)
	if !ok {
		row = map[string]any{"answer": answer}
	}
	return col.Template.Render(row), nil
}

func ElementVars(el Element) map[string]string {
	return map[string]string{
		"element_id":      el.ID,
		"site_id":         el.SiteID,
		"cluster_id":      el.ClusterID(),
		"spokecluster_id": el.ClusterID(),
		"tenant_id":       el.TSG,
		"el":              el.ID,
		"site":            el.SiteID,
	}
}

func (rt *Runtime) elementsByID(tsg string, ids []string) map[string]Element {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[string]Element, len(ids))
	for _, el := range rt.Elements() {
		if el.TSG == tsg && want[el.ID] {
			out[el.ID] = el
		}
	}
	return out
}

type CellsNext struct {
	Tick bool
	Save map[string]Cell
}

func (rt *Runtime) ApplyCells(res CellResult) CellsNext {
	at := cellKey(res.Col, res.TSG)
	rt.editCells(func(s *cellsState) {
		if s.cells[res.Col] == nil {
			s.cells[res.Col] = map[string]Cell{}
		}
		for k, c := range res.Cells {
			s.cells[res.Col][k] = c
		}
		delete(s.out, at)
		s.done[at] += len(res.Cells)
		if res.Bulk != "" {
			s.bulk[res.Col] = res.Bulk
		}
		if res.Err != "" && s.err[res.Col] == "" {
			s.err[res.Col] = res.Err
		}
		if len(res.Rest) == 0 {
			delete(s.queue, at)
		} else {
			s.queue[at] = res.Rest
		}
	})
	keys := make([]string, 0, len(res.Cells))
	for k := range res.Cells {
		keys = append(keys, k)
	}
	rt.changed(Event{Area: AreaCells, Rows: true, Keys: keys})
	if res.Err != "" {
		rt.note(res.Col + ": " + res.Err)
	}
	if res.Bulk != "" && res.Bulk != "used" {
		rt.note(fmt.Sprintf("%s one call per element (%s): the query was refused — %s",
			res.Col, rt.SweepETA(res.TSG, rt.CellsLeft(res.Col, res.TSG)), res.Bulk))
	}
	return CellsNext{Tick: len(res.Rest) > 0, Save: res.Cells}
}

func (rt *Runtime) CellsLeft(col, tsg string) int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	at := cellKey(col, tsg)
	return rt.cells.total[at] - rt.cells.done[at]
}

func (rt *Runtime) SaveCells(tsg, col string, cells map[string]Cell) {
	st := rt.Store()
	if st == nil || len(cells) == 0 {
		return
	}
	bare := make(map[string]string, len(cells))
	for key, c := range cells {
		if c.Text == "" || c.Failed {
			continue
		}
		bare[strings.TrimPrefix(key, tsg+"/")] = c.Text
	}
	if len(bare) > 0 {
		_ = st.SaveCells(tsg, col, bare)
	}
}

func (rt *Runtime) ResetCells() {
	rt.mu.Lock()
	specs := rt.cells.specs
	rt.cells = cellsState{specs: specs}
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaCells, Rows: true})
}

type TableJob struct {
	Name string
	Path string
	Key  string
	TSG  string
}

type TableResult struct {
	Name string
	TSG  string
	Rows map[string]map[string]any
	Err  string
}

func (rt *Runtime) TablesWanted(tsg string) []TableJob {
	var jobs []TableJob
	for _, spec := range rt.Columns() {
		if !spec.Fleet() || TableSubject(spec.Fetch) != "" {
			continue
		}
		at, start := cellKey(spec.Fetch, tsg), false
		rt.editCells(func(s *cellsState) {
			if s.tOut[at] || s.tables[at] != nil {
				return
			}
			delete(s.tErr, at)
			s.tOut[at], start = true, true
		})
		if start {
			jobs = append(jobs, TableJob{Name: spec.Fetch, Path: spec.Endpoint.Path, Key: spec.Key, TSG: tsg})
		}
	}
	return jobs
}

func TableSubject(uses string) string {
	switch uses {
	case column.ElementsSrc:
		return column.FromElement
	case column.SitesSrc:
		return column.FromSite
	}
	return ""
}

func (rt *Runtime) ReadTable(ctx context.Context, job TableJob) TableResult {
	var r CellReader
	if c := rt.Clients().Get(job.TSG); c != nil {
		r = c
	}
	return rt.ReadTableWith(ctx, r, job)
}

func (rt *Runtime) ReadTableWith(ctx context.Context, c CellReader, job TableJob) TableResult {
	res := TableResult{Name: job.Name, TSG: job.TSG}
	if c == nil {
		res.Err = "no client for this tenant"
		return res
	}
	body, err := c.GetJSON(ctx, job.Path)
	if err != nil {
		res.Err = rt.explain(err)
		return res
	}
	rows, err := RowsByKey(body, job.Key)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	logging.L().Info("column read for the fleet", "path", job.Path, "tsg", job.TSG, "rows", len(rows))
	res.Rows = rows
	return res
}

func RowsByKey(body []byte, key string) (map[string]map[string]any, error) {
	var env struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Items == nil {
		var bare []map[string]any
		if err := json.Unmarshal(body, &bare); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		env.Items = bare
	}
	out := make(map[string]map[string]any, len(env.Items))
	for _, row := range env.Items {
		if id := fmt.Sprint(row[key]); id != "" && id != "<nil>" {
			out[id] = row
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%d row(s), none of them with a %s", len(env.Items), key)
	}
	return out, nil
}

func (rt *Runtime) ApplyTable(res TableResult) {
	at := cellKey(res.Name, res.TSG)
	rt.editCells(func(s *cellsState) {
		delete(s.tOut, at)
		if res.Err != "" {
			s.tErr[at] = res.Err
			return
		}
		s.tables[at] = res.Rows
	})
	if res.Err != "" {
		rt.note(res.Name + ": " + res.Err)
	}
	rt.changed(Event{Area: AreaCells, Rows: true})
}

func (rt *Runtime) Table(name, tsg string) map[string]map[string]any {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cells.tables[cellKey(name, tsg)]
}

func (rt *Runtime) TableWorking(name string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for at, out := range rt.cells.tOut {
		if out && strings.HasPrefix(at, name+"\x00") {
			return true
		}
	}
	return false
}

func (rt *Runtime) CellsBulk(col string) string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cells.bulk[col]
}

func (rt *Runtime) CellsErr(col string) string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cells.err[col]
}

func (rt *Runtime) SetCellsProgress(col, tsg string, total, done int) {
	rt.editCells(func(s *cellsState) {
		at := cellKey(col, tsg)
		s.total[at], s.done[at] = total, done
	})
}

func (rt *Runtime) MarkTableOut(path, tsg string) {
	rt.editCells(func(s *cellsState) { s.tOut[cellKey(path, tsg)] = true })
}
