package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/runtime"
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

	rt, ctx, screen := m.runtime(), m.ctx, m.screen()
	logging.L().Info("lua action started", "action", name)
	return m, func() tea.Msg {
		c := &runtime.Call{Ctx: ctx, Args: map[string]string{"screen": screen}, DoIt: true}
		err := rt.Exec(c, app.ActionPrefix+name)
		effects, _ := c.Result.([]script.Effect)
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
			next, cmd := m.syncField(e.Field)
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
