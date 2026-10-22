package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

const (
	fetchChunkSize = 200
	fetchPace      = 400 * time.Millisecond
	fetchWorkers   = 8
	fetchBatch     = 16
)

type fetchedCell struct {
	text   string
	cached bool
	failed bool
}

type fetchSpec struct {
	name  string
	path  string
	bulk  string
	key   string
	value *tmpl.T
}

type cellReader interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
	QueryJSON(ctx context.Context, path, field string, ids []string, limit, page int) ([]byte, error)
}

type fetchedMsg struct {
	col   string
	tsg   string
	cells map[string]fetchedCell
	bulk  string
	err   string
	rest  []string
}

func fetchKey(col, tsg string) string { return col + "\x00" + tsg }

func (m tuiModel) fetchChunk(r cellReader, col fetchSpec, tsg string, ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	if r == nil {
		return func() tea.Msg {
			return fetchedMsg{col: col.name, tsg: tsg, err: "no client for this tenant",
				cells: failedCells(tsg, ids)}
		}
	}

	ask, rest := ids, []string(nil)
	if len(ask) > fetchChunkSize {
		ask, rest = ids[:fetchChunkSize], ids[fetchChunkSize:]
	}
	byID := m.elementsByID(tsg, ask)
	ctx := m.ctx

	return func() tea.Msg {
		if col.bulk != "" {
			cells, err := bulkCells(ctx, r, col, tsg, ask)
			if err == nil {
				return fetchedMsg{col: col.name, tsg: tsg, cells: cells, bulk: "used", rest: rest}
			}
			logging.L().Warn("column falling back to one call per element",
				"column", col.name, "tsg", tsg, "asked", len(ask), "err", err)
			cells, first := oneByOne(ctx, r, col, tsg, ask, byID)
			return fetchedMsg{col: col.name, tsg: tsg, cells: cells,
				bulk: shortTenantErr(err), err: first, rest: rest}
		}

		take, later := ask, rest
		if len(take) > fetchBatch {
			take, later = take[:fetchBatch], append(take[fetchBatch:], rest...)
		}
		cells, first := oneByOne(ctx, r, col, tsg, take, byID)
		return fetchedMsg{col: col.name, tsg: tsg, cells: cells, err: first, rest: later}
	}
}

func bulkCells(ctx context.Context, r cellReader, col fetchSpec, tsg string, ids []string) (map[string]fetchedCell, error) {
	body, err := r.QueryJSON(ctx, col.bulk, col.key, ids, fetchChunkSize, 1)
	if err != nil {
		return nil, err
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode %s: %w", col.bulk, err)
	}

	cells := make(map[string]fetchedCell, len(ids))
	for _, id := range ids {
		cells[tsg+"/"+id] = fetchedCell{}
	}
	named := 0
	for _, row := range env.Items {
		id, _ := row[col.key].(string)
		if id == "" {
			continue
		}
		named++
		cells[tsg+"/"+id] = fetchedCell{text: col.value.Render(row)}
	}
	if named == 0 {
		return nil, fmt.Errorf("%s answered %d row(s), none of them with a %s",
			col.bulk, len(env.Items), col.key)
	}
	return cells, nil
}

func oneByOne(ctx context.Context, r cellReader, col fetchSpec, tsg string, ids []string, byID map[string]elementRow) (map[string]fetchedCell, string) {
	var mu sync.Mutex
	cells := make(map[string]fetchedCell, len(ids))
	first := ""

	var wg sync.WaitGroup
	work := make(chan string)
	for range min(fetchWorkers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				text, err := oneCell(ctx, r, col, byID[id])
				mu.Lock()
				if err != nil {
					cells[tsg+"/"+id] = fetchedCell{failed: true}
					if first == "" {
						first = err.Error()
					}
				} else {
					cells[tsg+"/"+id] = fetchedCell{text: text}
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

func oneCell(ctx context.Context, r cellReader, col fetchSpec, el elementRow) (string, error) {
	path := fill(col.path, elementVars(el), nil)
	if i := strings.Index(path, "{"); i >= 0 {
		return "", fmt.Errorf("%s: nothing fills %s", col.name, path[i:])
	}
	body, err := r.GetJSON(ctx, path)
	if err != nil {
		return "", fmt.Errorf("%s: %s", col.name, shortTenantErr(err))
	}
	var answer any
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("%s: decode %s: %s", col.name, path, err)
	}
	row, ok := answer.(map[string]any)
	if !ok {
		row = map[string]any{"answer": answer}
	}
	return col.value.Render(row), nil
}

func failedCells(tsg string, ids []string) map[string]fetchedCell {
	out := make(map[string]fetchedCell, len(ids))
	for _, id := range ids {
		out[tsg+"/"+id] = fetchedCell{failed: true}
	}
	return out
}

func elementVars(el elementRow) map[string]string {
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

func (m tuiModel) elementsByID(tsg string, ids []string) map[string]elementRow {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[string]elementRow, len(ids))
	for _, el := range m.all {
		if el.TSG == tsg && want[el.ID] {
			out[el.ID] = el
		}
	}
	return out
}

func (m tuiModel) onFetchedCells(msg fetchedMsg) (tea.Model, tea.Cmd) {
	if m.fetched == nil {
		m.fetched = map[string]map[string]fetchedCell{}
	}
	if m.fetched[msg.col] == nil {
		m.fetched[msg.col] = map[string]fetchedCell{}
	}
	for key, cell := range msg.cells {
		m.fetched[msg.col][key] = cell
		m.forgetRow(key)
	}

	at := fetchKey(msg.col, msg.tsg)
	delete(m.fetchOut, at)
	if m.fetchDone == nil {
		m.fetchDone = map[string]int{}
	}
	m.fetchDone[at] += len(msg.cells)
	if msg.bulk != "" {
		if m.fetchBulk == nil {
			m.fetchBulk = map[string]string{}
		}
		m.fetchBulk[msg.col] = msg.bulk
	}
	if msg.err != "" {
		if m.fetchErr == nil {
			m.fetchErr = map[string]string{}
		}
		if m.fetchErr[msg.col] == "" {
			m.fetchErr[msg.col] = msg.err
		}
		if m.status == "" {
			m.status = msg.col + ": " + msg.err
		}
	}
	if msg.bulk != "" && msg.bulk != "used" && m.status == "" {
		m.status = fmt.Sprintf("%s one call per element (%s): the query was refused — %s",
			msg.col, sweepETA(m.fetchTotal[at]-m.fetchDone[at]), msg.bulk)
	}
	m.rebuildRows()

	if len(msg.rest) == 0 {
		delete(m.fetchQueue, at)
		return m, saveCells(m.store, msg.tsg, msg.col, msg.cells)
	}
	if m.fetchQueue == nil {
		m.fetchQueue = map[string][]string{}
	}
	m.fetchQueue[at] = msg.rest
	return m, tea.Batch(fetchTick(msg.col, msg.tsg), saveCells(m.store, msg.tsg, msg.col, msg.cells))
}

type fetchTickMsg struct{ col, tsg string }

func fetchTick(col, tsg string) tea.Cmd {
	return tea.Tick(fetchPace, func(time.Time) tea.Msg { return fetchTickMsg{col: col, tsg: tsg} })
}

func (m tuiModel) onFetchTick(msg fetchTickMsg) (tea.Model, tea.Cmd) {
	at := fetchKey(msg.col, msg.tsg)
	left := m.fetchQueue[at]
	if len(left) == 0 {
		return m, nil
	}
	col, ok := m.fetchSpecOf(msg.col)
	if !ok {
		return m, nil
	}
	m.markFetchOut(at)
	return m, m.fetchChunk(m.readerFor(msg.tsg), col, msg.tsg, m.visibleFirst(msg.tsg, left))
}

func (m tuiModel) readerFor(tsg string) cellReader {
	c := m.pool.get(tsg)
	if c == nil {
		return nil
	}
	return c
}

func (m *tuiModel) markFetchOut(at string) {
	if m.fetchOut == nil {
		m.fetchOut = map[string]bool{}
	}
	if m.fetchDone == nil {
		m.fetchDone = map[string]int{}
	}
	m.fetchOut[at] = true
}

func (m tuiModel) fetchSpecOf(name string) (fetchSpec, bool) {
	for _, c := range m.listCols {
		if c.name == name && c.fetch != nil {
			return *c.fetch, true
		}
	}
	return fetchSpec{}, false
}

func (m tuiModel) fetchWorking() bool {
	for _, out := range m.fetchOut {
		if out {
			return true
		}
	}
	for _, left := range m.fetchQueue {
		if len(left) > 0 {
			return true
		}
	}
	return false
}

func (m *tuiModel) askVisibleCells() tea.Cmd {
	var cmds []tea.Cmd
	for _, col := range m.listCols {
		if col.fetch == nil {
			continue
		}
		for tsg, ids := range m.cellsWanted(*col.fetch) {
			at := fetchKey(col.name, tsg)
			if m.fetchOut[at] {
				continue
			}
			m.markFetchOut(at)
			if cmd := m.fetchChunk(m.readerFor(tsg), *col.fetch, tsg, ids); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) cellsWanted(col fetchSpec) map[string][]string {
	out := map[string][]string{}
	for _, pos := range m.visibleMatches() {
		el := m.all[pos]
		if _, have := m.fetched[col.name][el.key()]; have {
			continue
		}
		out[el.TSG] = append(out[el.TSG], el.ID)
	}
	return out
}

func (m *tuiModel) startCells() tea.Cmd {
	var cmds []tea.Cmd
	for _, col := range m.listCols {
		if col.fetch == nil {
			continue
		}
		queued := map[string][]string{}
		for _, el := range m.all {
			if _, have := m.fetched[col.name][el.key()]; have {
				continue
			}
			queued[el.TSG] = append(queued[el.TSG], el.ID)
		}
		for tsg, ids := range queued {
			at := fetchKey(col.name, tsg)
			if m.fetchOut[at] || len(m.fetchQueue[at]) > 0 {
				continue
			}
			if m.fetchTotal == nil {
				m.fetchTotal = map[string]int{}
			}

			m.fetchTotal[at] = len(ids)
			m.markFetchOut(at)
			if cmd := m.fetchChunk(m.readerFor(tsg), *col.fetch, tsg, m.visibleFirst(tsg, ids)); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func saveCells(st *config.Store, tsg, col string, cells map[string]fetchedCell) tea.Cmd {
	if st == nil || len(cells) == 0 {
		return nil
	}
	bare := make(map[string]string, len(cells))
	for key, cell := range cells {
		if cell.text == "" || cell.failed {
			continue
		}
		bare[strings.TrimPrefix(key, tsg+"/")] = cell.text
	}
	if len(bare) == 0 {
		return nil
	}
	return func() tea.Msg {
		_ = st.SaveCells(tsg, col, bare)
		return nil
	}
}

func (m *tuiModel) loadCachedCells(tsg string, saved map[string]map[string]string) {
	if len(saved) == 0 {
		return
	}
	if m.fetched == nil {
		m.fetched = map[string]map[string]fetchedCell{}
	}
	for col, cells := range saved {
		if m.fetched[col] == nil {
			m.fetched[col] = map[string]fetchedCell{}
		}
		for eid, text := range cells {
			key := tsg + "/" + eid
			if _, have := m.fetched[col][key]; have {
				continue
			}
			m.fetched[col][key] = fetchedCell{text: text, cached: true}
		}
	}
}

func fetchedValue(name string) func(*tuiModel, elementRow) string {
	return func(m *tuiModel, el elementRow) string {
		cell, ok := m.fetched[name][el.key()]
		switch {
		case ok && cell.failed:
			return "?"
		case ok && cell.text != "":
			if cell.cached {
				return cell.text + "*"
			}
			return cell.text
		case ok:
			return "-"
		case m.fetchWorking():
			return "loading"
		}
		return "-"
	}
}

func fetchedPaint(name string) func(*tuiModel, cell) string {
	return func(m *tuiModel, c cell) string {
		if c.here {
			return ui.Plain(c.text)
		}
		got, ok := m.fetched[name][c.el.key()]
		switch {
		case ok && got.failed:
			return ui.StyleWarn.Render(c.text)
		case !ok:
			return ui.StyleDim.Render(c.text)
		case got.cached && strings.HasSuffix(c.text, "*"):
			return c.row(strings.TrimSuffix(c.text, "*")) + ui.StyleDim.Render("*")
		}
		return c.row(c.text)
	}
}

func specOf(c column.Spec) *fetchSpec {
	if !c.Fetches() {
		return nil
	}
	return &fetchSpec{name: c.Name, path: c.Fetch, bulk: c.Bulk, key: c.Key, value: c.Template}
}

var _ cellReader = (*api.Client)(nil)

func (m tuiModel) cellsNote() string {
	var notes []string
	for _, col := range m.listCols {
		if col.fetch == nil {
			continue
		}
		done, total := 0, 0
		for tsg := range m.tenantsOnTheList() {
			at := fetchKey(col.name, tsg)
			done += m.fetchDone[at]
			total += m.fetchTotal[at]
		}
		if total == 0 || done >= total {
			continue
		}
		notes = append(notes, ui.StyleWarn.Render(col.name)+
			ui.StyleDim.Render(fmt.Sprintf(" %d/%d", min(done, total), total)))
	}
	if len(notes) == 0 {
		return ""
	}
	if len(notes) > 2 {
		notes = notes[:2]
	}
	return m.spinner.View() + " " + strings.Join(notes, ui.StyleDim.Render(" · "))
}

func (m tuiModel) tenantsOnTheList() map[string]bool {
	out := map[string]bool{}
	for _, el := range m.all {
		out[el.TSG] = true
	}
	return out
}
