package tui

import (
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/tmpl"
)

type fleetSpec struct {
	name  string
	path  string
	key   string
	on    string
	value *tmpl.T
}

func fleetValue(spec column.Spec) func(*tuiModel, elementRow) string {
	f := fleetSpec{name: spec.Name, path: spec.Fetch, key: spec.Key, on: spec.On, value: spec.Template}
	if read, ok := fieldReader(f.value.Text(), subjectOf(f)); ok && subjectOf(f) != "" {
		return read
	}
	flat := flatEnough(f.value.Text(), subjectOf(f))
	return func(m *tuiModel, el elementRow) string {
		if subject := subjectOf(f); subject != "" {
			return f.value.Render(m.rowContext(el, subject, flat))
		}
		row, ok := m.joined(f, el)
		if !ok {
			if m.fleetWorking(f.path) {
				return "loading"
			}
			return "-"
		}
		return f.value.Render(row)
	}
}

func subjectOf(f fleetSpec) string { return runtime.TableSubject(f.path) }

func (m *tuiModel) joined(f fleetSpec, el elementRow) (map[string]any, bool) {
	rows := m.runtime().Table(f.path, el.TSG)
	if rows == nil {
		return nil, false
	}
	read, ok := elementValues[f.on]
	if !ok {
		return nil, false
	}
	row, ok := rows[asText(read(m, el))]
	return row, ok
}

func fleetKey(path, tsg string) string { return path + "\x00" + tsg }

type fleetReadMsg struct {
	path string
	tsg  string
	rows map[string]map[string]any
	err  string
}

func (m tuiModel) readFleet(r cellReader, f fleetSpec, tsg string) tea.Cmd {
	if r == nil {
		return nil
	}
	rt, ctx, job := m.runtime(), m.ctx, runtime.TableJob{Path: f.path, Key: f.key, TSG: tsg}
	return func() tea.Msg {
		res := rt.ReadTableWith(ctx, r, job)
		return fleetReadMsg{path: res.Path, tsg: res.TSG, rows: res.Rows, err: res.Err}
	}
}

func rowsByKey(body []byte, key string) (map[string]map[string]any, error) {
	return runtime.RowsByKey(body, key)
}

func (m tuiModel) onFleetRead(msg fleetReadMsg) (tea.Model, tea.Cmd) {
	m.runtime().ApplyTable(runtime.TableResult{Path: msg.path, TSG: msg.tsg, Rows: msg.rows, Err: msg.err})
	if msg.err != "" {
		if m.status == "" {
			m.status = msg.path + ": " + msg.err
		}
		return m, nil
	}
	m.forgetRows()
	m.rebuildRows()
	return m, nil
}

func (m tuiModel) fleetWorking(path string) bool { return m.rt != nil && m.rt.TableWorking(path) }

func (m *tuiModel) readFleets() tea.Cmd {
	var cmds []tea.Cmd
	for _, job := range m.runtime().TablesWanted() {
		if cmd := m.readFleet(m.readerFor(job.TSG), fleetSpec{path: job.Path, key: job.Key}, job.TSG); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) fleetSpecs() []fleetSpec {
	var out []fleetSpec
	for _, spec := range m.runtime().Columns() {
		if spec.Fleet() {
			out = append(out, fleetSpec{name: spec.Name, path: spec.Fetch,
				key: spec.Key, on: spec.On, value: spec.Template})
		}
	}
	return out
}
