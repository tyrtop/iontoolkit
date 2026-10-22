package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/logging"
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

func subjectOf(f fleetSpec) string {
	switch {
	case strings.HasSuffix(f.path, "/"+column.ElementsSrc):
		return column.FromElement
	case strings.HasSuffix(f.path, "/"+column.SitesSrc):
		return column.FromSite
	}
	return ""
}

func (m *tuiModel) joined(f fleetSpec, el elementRow) (map[string]any, bool) {
	rows := m.fleet[fleetKey(f.path, el.TSG)]
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
	ctx, path, key := m.ctx, f.path, f.key
	return func() tea.Msg {
		body, err := r.GetJSON(ctx, path)
		if err != nil {
			return fleetReadMsg{path: path, tsg: tsg, err: shortTenantErr(err)}
		}
		rows, err := rowsByKey(body, key)
		if err != nil {
			return fleetReadMsg{path: path, tsg: tsg, err: err.Error()}
		}
		logging.L().Info("column read for the fleet", "path", path, "tsg", tsg, "rows", len(rows))
		return fleetReadMsg{path: path, tsg: tsg, rows: rows}
	}
}

func rowsByKey(body []byte, key string) (map[string]map[string]any, error) {
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

func (m tuiModel) onFleetRead(msg fleetReadMsg) (tea.Model, tea.Cmd) {
	at := fleetKey(msg.path, msg.tsg)
	delete(m.fleetOut, at)
	if msg.err != "" {
		if m.fleetErr == nil {
			m.fleetErr = map[string]string{}
		}
		m.fleetErr[at] = msg.err
		if m.status == "" {
			m.status = msg.path + ": " + msg.err
		}
		return m, nil
	}
	if m.fleet == nil {
		m.fleet = map[string]map[string]map[string]any{}
	}
	m.fleet[at] = msg.rows
	m.forgetRows()
	m.rebuildRows()
	return m, nil
}

func (m tuiModel) fleetWorking(path string) bool {
	for at, out := range m.fleetOut {
		if out && strings.HasPrefix(at, path+"\x00") {
			return true
		}
	}
	return false
}

func (m *tuiModel) readFleets() tea.Cmd {
	var cmds []tea.Cmd
	for _, f := range m.fleetSpecs() {
		if subjectOf(f) != "" {
			continue
		}
		for tsg := range m.tenantsOnTheList() {
			at := fleetKey(f.path, tsg)
			if m.fleetOut[at] || m.fleet[at] != nil || m.fleetErr[at] != "" {
				continue
			}
			if m.fleetOut == nil {
				m.fleetOut = map[string]bool{}
			}
			m.fleetOut[at] = true
			if cmd := m.readFleet(m.readerFor(tsg), f, tsg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) fleetSpecs() []fleetSpec {
	var out []fleetSpec
	for _, spec := range m.listSpecs {
		if spec.Fleet() {
			out = append(out, fleetSpec{name: spec.Name, path: spec.Fetch,
				key: spec.Key, on: spec.On, value: spec.Template})
		}
	}
	return out
}
