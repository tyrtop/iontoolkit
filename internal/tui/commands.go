package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ioncmd"
)

type commandsSavedMsg struct{ err string }

func (m *tuiModel) countUse(command, model string) {
	if m.cmds == nil {
		m.cmds = &config.CommandStore{}
	}
	m.cmds.Record(command, model, time.Now())
}

func (m *tuiModel) recordUse(command, model string) tea.Cmd {
	m.countUse(command, model)
	return saveCommands(m.store, m.cmds)
}

func saveCommands(st *config.Store, c *config.CommandStore) tea.Cmd {
	if st == nil || c == nil {
		return nil
	}
	snapshot := *c
	return func() tea.Msg {
		if err := st.SaveCommands(&snapshot); err != nil {
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

func (m tuiModel) modelOf(tsg, eid string) string {
	if el, ok := m.elementByKey(tsg + "/" + eid); ok {
		return el.Model
	}
	return ""
}

func (m *tuiModel) recordSessionLine() tea.Cmd {
	s := m.sessions.Active()
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
		for _, c := range cmds {
			m.countUse(c, r.Model)
		}
	}
	return saveCommands(m.store, m.cmds)
}

func (m tuiModel) activeModel() string {
	s := m.sessions.Active()
	if s == nil {
		return ""
	}
	return m.modelOf(m.tsgForElement(s.ElementID), s.ElementID)
}

func (m tuiModel) setFreq(q string) tuiModel {
	model := m.activeModel()
	ranked := m.cmds.Ranked(model)

	if q = strings.TrimSpace(q); q != "" {
		cmds := make([]string, 0, len(ranked))
		for _, u := range ranked {
			cmds = append(cmds, u.Command)
		}
		keep := map[string]bool{}
		for _, hit := range ioncmd.Fuzzy(cmds, q) {
			keep[hit] = true
		}
		kept := ranked[:0:0]
		for _, u := range ranked {
			if keep[u.Command] {
				kept = append(kept, u)
			}
		}
		ranked = kept
	}

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
	if !m.cmds.Forget(use.Command, use.Model) {
		return m, nil
	}
	save := saveCommands(m.store, m.cmds)
	m = m.setFreq(m.slashInput.Value())
	m.status = "forgot " + use.Command
	return m, save
}
