package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/script"
)

type scriptDoneMsg struct {
	name    string
	effects []script.Effect
	err     string
}

func (m tuiModel) startScript(name string) (tea.Model, tea.Cmd) {
	if m.scriptOn != "" {
		m.status = fmt.Sprintf("%s is still running", m.scriptOn)
		return m, nil
	}
	m.scriptOn = name
	m.status = name + " running"

	run := script.Run{
		Name:   name,
		State:  m.scriptState(),
		Export: m.scriptExport(),
	}
	if c := m.pool.get(m.tenant.TSGID); c != nil {
		run.Get = m.scriptGet(c)
	}

	set, ctx := m.scripts, m.ctx
	logging.L().Info("lua action started", "action", name)
	return m, func() tea.Msg {
		effects, err := set.Run(ctx, run)
		msg := scriptDoneMsg{name: name, effects: effects}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

func (m tuiModel) onScriptDone(msg scriptDoneMsg) (tea.Model, tea.Cmd) {
	m.scriptOn = ""
	var cmds []tea.Cmd

	for _, e := range msg.effects {
		switch e.Kind {
		case script.EffectStatus:
			m.status = e.Value
		case script.EffectExport:
			m.status = "exported to " + e.Value
		case script.EffectGoto:
			place, ok := tutorialPlaces[e.Value]
			if !ok {
				m.status = fmt.Sprintf("%s: %q is not a place", msg.name, e.Value)
				continue
			}
			next, cmd := place(m)
			m, cmds = next, append(cmds, cmd)
		case script.EffectSet:
			next, cmd, err := m.setField(e.Field, e.Value)
			if err != nil {
				m.status = fmt.Sprintf("%s: %s", msg.name, err)
				continue
			}
			m, cmds = next, append(cmds, cmd)
		}
	}
	if msg.err != "" {
		m.status = msg.err
		logging.L().Warn("lua action failed", "action", msg.name, "err", msg.err)
	} else {
		logging.L().Info("lua action done", "action", msg.name, "effects", len(msg.effects))
	}
	return m, tea.Batch(cmds...)
}

func (m tuiModel) scriptState() map[string]any {
	state := map[string]any{
		"tenant":    m.tenant.Name,
		"tsg":       m.tenant.TSGID,
		"mode":      m.mode.String(),
		"selection": len(m.selected),
		"screen":    m.screen(),
	}
	if el, ok := m.current(); ok {
		if b, err := json.Marshal(el.Element); err == nil {
			var rec map[string]any
			if json.Unmarshal(b, &rec) == nil {
				state["element"] = rec
			}
		}
		state["element_id"] = el.ID
		state["site_id"] = el.SiteID
		state["cluster_id"] = el.ClusterID()
		state["element_name"] = el.label()
	}
	return state
}

func (m tuiModel) scriptGet(g dataview.Getter) func(string, map[string]string) (any, error) {
	vars := m.scriptVars()
	return func(path string, extra map[string]string) (any, error) {
		full := fill(path, vars, extra)
		if err := checkScriptPath(full); err != nil {
			return nil, err
		}
		b, err := g.GetJSON(m.ctx, full)
		if err != nil {
			return nil, err
		}
		var body any
		if err := json.Unmarshal(b, &body); err != nil {
			return nil, fmt.Errorf("decode %s: %w", full, err)
		}
		return body, nil
	}
}

func (m tuiModel) scriptExport() func(string, string) (string, error) {
	return func(text, slug string) (string, error) {
		return m.writeExport(slug, "txt", text)
	}
}

func (m tuiModel) scriptVars() map[string]string {
	vars := map[string]string{"tenant_id": m.tenant.TSGID}
	if el, ok := m.current(); ok {
		vars["element_id"] = el.ID
		vars["site_id"] = el.SiteID
		vars["cluster_id"] = el.ClusterID()
		vars["spokecluster_id"] = el.ClusterID()
		vars["el"] = el.ID
		vars["site"] = el.SiteID
	}
	return vars
}

func fill(path string, vars, extra map[string]string) string {
	for _, from := range []map[string]string{vars, extra} {
		for k, v := range from {
			path = strings.ReplaceAll(path, "{"+k+"}", v)
		}
	}
	return path
}

func checkScriptPath(path string) error {
	if i := strings.Index(path, "{"); i >= 0 {
		return fmt.Errorf("%s: nothing fills %s", path, path[i:min(i+20, len(path))])
	}
	return dataview.CheckPath(path)
}
