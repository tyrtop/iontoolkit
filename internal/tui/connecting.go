package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

type dialState struct {
	attempt int
	of      int
	lastErr string
	failed  bool
}

type dialProgressMsg struct {
	eid     string
	attempt int
	of      int
	err     string
}

func newDialReports() chan dialProgressMsg { return make(chan dialProgressMsg, 32) }

func waitForDial(ch <-chan dialProgressMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func reportDialsTo(c *api.Client, ch chan dialProgressMsg) {
	if c == nil {
		return
	}
	c.SetDialReporter(func(a api.DialAttempt) {
		msg := dialProgressMsg{eid: a.ElementID, attempt: a.Attempt, of: a.Of}
		if a.Err != nil {
			msg.err = a.Err.Error()
		}
		select {
		case ch <- msg:
		default:
		}
	})
}

func (m *tuiModel) noteDial(msg dialProgressMsg) {
	m.setDial(msg.eid, dialState{attempt: msg.attempt, of: msg.of, lastErr: msg.err})
}

func (m *tuiModel) markDialing(eid string) {
	m.setDial(eid, dialState{attempt: 1, of: 1})
}

func (m *tuiModel) setDial(eid string, st dialState) {
	if m.dialing == nil {
		m.dialing = map[string]dialState{}
	}
	m.dialing[eid] = st
}

func (m *tuiModel) dialDone(eid string) {
	delete(m.dialing, eid)
}

func (m *tuiModel) dialFailed(eid, reason string) {
	st := m.dialing[eid]
	st.failed = true
	st.lastErr = reason
	m.setDial(eid, st)
	if m.redialing == eid {
		m.redialing = ""
	}
}

func (m tuiModel) connectingAny() bool {
	for _, st := range m.dialing {
		if !st.failed {
			return true
		}
	}
	return m.connecting != ""
}

func (m tuiModel) retryPrompt() string {
	name := m.askRetry.name
	if name == "" {
		name = m.askRetry.eid
	}

	reason := ""
	if st, ok := m.dialing[m.askRetry.eid]; ok && st.lastErr != "" {
		reason = ui.Truncate(shortDialErr(st.lastErr), 44)
	}

	lines := []string{
		ui.StyleErr.Render("unable to connect to ") + ui.StyleElement.Render(name),
	}
	if reason != "" {
		lines = append(lines, ui.StyleDim.Render(reason))
	}
	lines = append(lines,
		m.ionLabel(m.askRetry.eid)+ui.StyleDim.Render(" in the portal"),
		"",
		"try again",
		m.retryChoices(),
	)
	return ui.Modal(lines, m.width)
}

func (m tuiModel) retryChoices() string {
	yes, no := " yes ", " no "
	if m.askRetryNo {
		return ui.StyleDim.Render(yes) + "  " + ui.StyleErr.Reverse(true).Bold(true).Render(no)
	}
	return ui.StyleOK.Reverse(true).Bold(true).Render(yes) + "  " + ui.StyleDim.Render(no)
}

func (m tuiModel) overlayRetry(content string) string {
	prompt := m.retryPrompt()
	keep := max(0, m.height-ui.ModalHeight(len(strings.Split(prompt, "\n")))-1)

	lines := strings.Split(content, "\n")
	if len(lines) > keep {
		lines = lines[:keep]
	}
	return strings.Join(lines, "\n") + "\n" + prompt
}

func (m tuiModel) onRetryKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.askRetryNo = !m.askRetryNo
		return m, nil
	}

	target := m.askRetry
	retry := false
	switch key {
	case "y", "Y":
		retry = true
	case "enter":
		retry = !m.askRetryNo
	}

	m.askRetry = sessionTarget{}
	m.askRetryNo = false
	m.dialDone(target.eid)

	if retry {
		m.status = ""
		return m.openSessions([]sessionTarget{target})
	}
	m.rebuildRows()
	if m.sessions.Count() == 0 && m.phase == phaseSession {
		m.phase = phasePicking
	}
	return m, nil
}

func (m *tuiModel) rowStatus(el elementRow) string {
	glyph, style := m.rowStatusOf(el)
	return style.Render(glyph)
}

func (m *tuiModel) rowStatusOf(el elementRow) (string, lipgloss.Style) {
	if st, ok := m.dialing[el.ID]; ok {
		if st.failed {
			return ui.MarkCross.String(), ui.StyleErr
		}
		return m.spinner.View(), lipgloss.NewStyle()
	}
	s := m.sessions.Find(el.ID)
	if s == nil {
		for _, d := range m.sessions.Detached() {
			if d.ElementID == el.ID {
				return "○", ui.StyleDim
			}
		}
		return " ", lipgloss.NewStyle()
	}
	switch {
	case s.Err() != nil:
		return ui.MarkCross.String(), ui.StyleErr
	case s.Hung():
		return "~", ui.StyleWarn
	}
	return "●", ui.StyleOK
}

func (m tuiModel) connectBar() string {
	s := m.sessions.Active()
	if s == nil {
		if m.connecting == "" {
			return ui.StyleDim.Render("  no session")
		}
		return "  " + m.spinner.View() + " connecting to " + m.connecting
	}

	var parts []string

	if st, ok := m.dialing[s.ElementID]; ok && !st.failed {
		what := "dial"
		if m.redialing == s.ElementID {
			what = "redial"
		}
		parts = append(parts, m.spinner.View()+" "+
			ui.StyleWarn.Render(fmt.Sprintf("%s %d/%d", what, st.attempt, st.of)))
		if st.lastErr != "" {
			parts = append(parts, ui.StyleDim.Render("last: "+ui.Truncate(shortDialErr(st.lastErr), 40)))
		}
	} else {
		switch {
		case s.Err() != nil:
			parts = append(parts, ui.StyleErr.Render(ui.MarkCross.String()+" failed"))
		case s.Hung():
			parts = append(parts, ui.StyleWarn.Render("~ not answering"))
		case !s.Answered():
			parts = append(parts, m.spinner.View()+" "+ui.StyleWarn.Render("waiting for the ion"))
		default:
			parts = append(parts, ui.StyleOK.Render(ui.MarkCheck.String()+" successfully dialed"))
		}
	}

	parts = append(parts, m.ionLabel(s.ElementID))

	if (s.Hung() || s.Err() != nil) && m.redialing != s.ElementID {
		parts = append(parts, ui.StyleDim.Render("alt+r retry"))
	}
	return "  " + strings.Join(parts, ui.StyleDim.Render(" · "))
}

func (m tuiModel) awaitingDevice() bool {
	s := m.sessions.Active()
	return s != nil && s.Err() == nil && !s.Hung() && !s.Ended() && !s.Answered()
}

func (m tuiModel) ionLabel(eid string) string {
	for _, r := range m.all {
		if r.ID != eid {
			continue
		}
		if r.Connected {
			return ui.StyleOK.Render("ion online")
		}
		return ui.StyleWarn.Render("ion offline")
	}
	return ui.StyleDim.Render("ion unknown")
}

func shortDialErr(s string) string {
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		return s[i+2:]
	}
	return s
}
