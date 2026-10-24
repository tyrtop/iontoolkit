package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

const (
	fetchChunkSize = runtime.CellChunk
	fetchPace      = 400 * time.Millisecond
)

type fetchedCell = runtime.Cell

type fetchSpec struct {
	name  string
	path  string
	bulk  string
	key   string
	value *tmpl.T
}

type cellReader = runtime.CellReader

type fetchedMsg struct {
	col   string
	tsg   string
	cells map[string]fetchedCell
	bulk  string
	err   string
	rest  []string
}

func cellsMsg(r runtime.CellResult) fetchedMsg {
	return fetchedMsg{col: r.Col, tsg: r.TSG, cells: r.Cells, bulk: r.Bulk, err: r.Err, rest: r.Rest}
}

func (msg fetchedMsg) result() runtime.CellResult {
	return runtime.CellResult{Col: msg.col, TSG: msg.tsg, Cells: msg.cells, Bulk: msg.bulk, Err: msg.err, Rest: msg.rest}
}

func fetchKey(col, tsg string) string { return col + "\x00" + tsg }

func (m tuiModel) fetchChunk(r cellReader, col fetchSpec, tsg string, ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	rt, ctx, job := m.runtime(), m.ctx, runtime.CellJob{Col: col.name, TSG: tsg, IDs: ids}
	return func() tea.Msg { return cellsMsg(rt.FetchCellsWith(ctx, r, job)) }
}

func (m tuiModel) onFetchedCells(msg fetchedMsg) (tea.Model, tea.Cmd) {
	next := m.runtime().ApplyCells(msg.result())
	for key := range msg.cells {
		m.forgetRow(key)
	}
	if msg.err != "" && m.status == "" {
		m.status = msg.col + ": " + msg.err
	}
	if msg.bulk != "" && msg.bulk != "used" && m.status == "" {
		m.status = fmt.Sprintf("%s one call per element (%s): the query was refused — %s",
			msg.col, sweepETA(m.runtime().CellsLeft(msg.col, msg.tsg)), msg.bulk)
	}
	m.rebuildRows()
	save := saveCells(m.runtime(), msg.tsg, msg.col, next.Save)
	if !next.Tick {
		return m, save
	}
	return m, tea.Batch(fetchTick(msg.col, msg.tsg), save)
}

type fetchTickMsg struct{ col, tsg string }

func fetchTick(col, tsg string) tea.Cmd {
	return tea.Tick(fetchPace, func(time.Time) tea.Msg { return fetchTickMsg{col: col, tsg: tsg} })
}

func (m tuiModel) onFetchTick(msg fetchTickMsg) (tea.Model, tea.Cmd) {
	job, ok := m.runtime().NextCells(msg.col, msg.tsg)
	if !ok {
		return m, nil
	}
	return m, m.fetchJob(job)
}

func (m tuiModel) fetchJob(job runtime.CellJob) tea.Cmd {
	col, ok := m.fetchSpecOf(job.Col)
	if !ok {
		return nil
	}
	return m.fetchChunk(m.readerFor(job.TSG), col, job.TSG, job.IDs)
}

func (m tuiModel) readerFor(tsg string) cellReader {
	c := m.pool().Get(tsg)
	if c == nil {
		return nil
	}
	return c
}

func (m tuiModel) fetchSpecOf(name string) (fetchSpec, bool) {
	for _, c := range m.listCols {
		if c.name == name && c.fetch != nil {
			return *c.fetch, true
		}
	}
	return fetchSpec{}, false
}

func (m tuiModel) fetchWorking() bool { return m.rt != nil && m.rt.CellsWorking() }

func (m *tuiModel) askVisibleCells() tea.Cmd {
	m.runtime().SetViewport(m.listHeight())
	var cmds []tea.Cmd
	for _, job := range m.runtime().CellsWanted() {
		if cmd := m.fetchJob(job); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *tuiModel) startCells() tea.Cmd {
	var cmds []tea.Cmd
	for _, job := range m.runtime().StartCells() {
		if cmd := m.fetchJob(job); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func saveCells(rt *runtime.Runtime, tsg, col string, cells map[string]fetchedCell) tea.Cmd {
	if len(cells) == 0 {
		return nil
	}
	return func() tea.Msg {
		rt.SaveCells(tsg, col, cells)
		return nil
	}
}

func (m *tuiModel) loadCachedCells(tsg string, saved map[string]map[string]string) {
	m.runtime().SetCachedCells(tsg, saved)
}

func fetchedValue(name string) func(*tuiModel, elementRow) string {
	return func(m *tuiModel, el elementRow) string {
		cell, ok := m.runtime().Cell(name, el.Key())
		switch {
		case ok && cell.Failed:
			return "?"
		case ok && cell.Text != "":
			if cell.Cached {
				return cell.Text + "*"
			}
			return cell.Text
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
		got, ok := m.runtime().Cell(name, c.el.Key())
		switch {
		case ok && got.Failed:
			return ui.StyleWarn.Render(c.text)
		case !ok:
			return ui.StyleDim.Render(c.text)
		case got.Cached && strings.HasSuffix(c.text, "*"):
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
		done, total := m.runtime().CellsProgress(col.name)
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
	for _, el := range m.rows() {
		out[el.TSG] = true
	}
	return out
}

func elementVars(el elementRow) map[string]string { return runtime.ElementVars(el) }
