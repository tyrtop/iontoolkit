package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ioncmd"
)

// commandsSavedMsg is a save that failed. A successful one says nothing: the
// store is written for the next run, and nothing on screen depends on it.
type commandsSavedMsg struct{ err string }

// countUse is one use, in memory only, for a caller recording several before
// it saves.
func (m *tuiModel) countUse(command, model string) {
	if m.cmds == nil {
		m.cmds = &config.CommandStore{}
	}
	m.cmds.Record(command, model, time.Now())
}

// recordUse counts a command against the model it was run on and hands back
// the save. The save is a command rather than a call because it touches the
// disk and this runs on a keypress.
//
// Named apart from m.record, which builds a search.Record for the matcher.
func (m *tuiModel) recordUse(command, model string) tea.Cmd {
	m.countUse(command, model)
	return saveCommands(m.store, m.cmds)
}

func saveCommands(st *config.Store, c *config.CommandStore) tea.Cmd {
	if st == nil || c == nil {
		return nil
	}
	// Copied: the model goes on being edited while this runs.
	snapshot := *c
	return func() tea.Msg {
		if err := st.SaveCommands(&snapshot); err != nil {
			return commandsSavedMsg{err: err.Error()}
		}
		return commandsSavedMsg{}
	}
}

func (m tuiModel) onCommandsSaved(msg commandsSavedMsg) (tea.Model, tea.Cmd) {
	// Said once, in the footer, and not repeated: a history that cannot be
	// written is worth knowing about but must not interrupt anything.
	if msg.err != "" && m.status == "" {
		m.status = "could not save command history: " + msg.err
	}
	return m, nil
}

// modelOf is the hardware model of an element, which is what makes history
// and favourites model-aware. Empty for an element the list has not got --
// a session reattached after the cache was dropped, say.
func (m tuiModel) modelOf(tsg, eid string) string {
	if el, ok := m.elementByKey(tsg + "/" + eid); ok {
		return el.Model
	}
	return ""
}

// recordSessionLine counts what a shell session is about to send. It is taken
// before the key is forwarded, because the device echoes the line away.
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

// recordRun counts a batch run's commands once per model in it. Per model
// rather than per element: ten 1200s running the same command is one fact
// about the 1200, not ten.
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

// activeModel is the model of the element the session in front of the user is
// on, which is what /fc ranks for.
func (m tuiModel) activeModel() string {
	s := m.sessions.Active()
	if s == nil {
		return ""
	}
	return m.modelOf(m.tsgForElement(s.ElementID), s.ElementID)
}

// setFreq fills /fc from the history. The query decides membership and the
// ranking decides order: most-used first is the point of the list, so a fuzzy
// score must not reorder it.
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

// freqRow is one line of the list: the command, how often it has been run,
// and the model when it was not this one -- a 1200 command offered on a 3200
// is what the stored model is there to show. With no model to compare
// against, every row would carry a tag that says nothing, so none do.
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

// forgetFreq drops the highlighted entry. A history nobody can prune fills
// with typos and stops being a shortlist.
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
