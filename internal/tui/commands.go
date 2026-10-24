package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
)

type commandsSavedMsg struct{ err string }

func (m *tuiModel) recordUse(command, model string) tea.Cmd {
	m.runtime().RecordUse(model, command)
	return m.saveCommands()
}

func (m tuiModel) saveCommands() tea.Cmd {
	rt := m.runtime()
	if rt.Store() == nil {
		return nil
	}
	return func() tea.Msg {
		if err := rt.SaveCommands(); err != nil {
			return commandsSavedMsg{err: err.Error()}
		}
		return commandsSavedMsg{}
	}
}

func (m tuiModel) onCommandsSaved(msg commandsSavedMsg) (tea.Model, tea.Cmd) {
	if msg.err != "" && m.status == "" {
		m.status = "could not save command history: " + msg.err
	}
	return m, nil
}

func (m tuiModel) modelOf(tsg, eid string) string { return m.runtime().ModelOf(tsg, eid) }

func (m *tuiModel) recordSessionLine() tea.Cmd {
	s := m.sessions().Active()
	if s == nil {
		return nil
	}
	line := s.Line()
	if line == "" {
		return nil
	}
	return m.recordUse(line, m.modelOf(m.tsgForElement(s.ElementID), s.ElementID))
}

func (m *tuiModel) recordRun(cmds []string, rows []elementRow) tea.Cmd {
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Model] {
			continue
		}
		seen[r.Model] = true
		m.runtime().RecordUse(r.Model, cmds...)
	}
	return m.saveCommands()
}

func (m tuiModel) activeModel() string { return m.runtime().SessionModel() }

func (m tuiModel) setFreq(q string) tuiModel {
	model := m.activeModel()
	ranked := m.runtime().Ranked(model, q)
	m.freq = ranked
	m.slashItems = make([]string, 0, len(ranked))
	for _, u := range ranked {
		m.slashItems = append(m.slashItems, freqRow(u, model))
	}
	if m.slashCursor >= len(m.slashItems) {
		m.slashCursor = max(0, len(m.slashItems)-1)
	}
	return m
}

func freqRow(u config.CommandUse, model string) string {
	row := u.Command + "  \u00d7" + strconv.Itoa(u.Uses)
	if model != "" && u.Model != "" && u.Model != model {
		row += " \u00b7 " + u.Model
	}
	return row
}

func (m tuiModel) freqAt(i int) (config.CommandUse, bool) {
	if i < 0 || i >= len(m.freq) {
		return config.CommandUse{}, false
	}
	return m.freq[i], true
}

func (m tuiModel) forgetFreq() (tea.Model, tea.Cmd) {
	use, ok := m.freqAt(m.slashCursor)
	if !ok {
		return m, nil
	}
	if !m.runtime().ForgetUse(use.Command, use.Model) {
		return m, nil
	}
	save := m.saveCommands()
	m = m.setFreq(m.slashInput.Value())
	m.status = "forgot " + use.Command
	return m, save
}
