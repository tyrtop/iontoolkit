package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

// dialState is what is known about one element's connection while it is being
// made. It exists because dialling retries on its own: without this the list
// sat still for up to three attempts with nothing to show for it.
type dialState struct {
	attempt int
	of      int
	// lastErr is what the previous attempt failed with, kept so a retry can say
	// why it is retrying.
	lastErr string
	// failed is set once the attempts ran out.
	failed bool
}

// dialProgressMsg is one reported attempt, forwarded from whichever client
// happens to be dialling.
type dialProgressMsg struct {
	eid     string
	attempt int
	of      int
	err     string
}

// dialReports is the bridge from the clients, which dial on their own
// goroutines, to Update. Buffered and dropped rather than blocked: a dial must
// never wait on the UI.
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

// reportDialsTo points a client's dial reporter at the model's channel. Every
// client that might open a session needs this, and setting it twice is
// harmless.
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

// markDialing shows a row as connecting before the first attempt reports, so
// the spinner is there from the first frame rather than after a failure.
func (m *tuiModel) markDialing(eid string) {
	m.setDial(eid, dialState{attempt: 1, of: 1})
}

func (m *tuiModel) setDial(eid string, st dialState) {
	if m.dialing == nil {
		m.dialing = map[string]dialState{}
	}
	m.dialing[eid] = st
}

// dialDone clears an element's dial state, whatever its outcome.
func (m *tuiModel) dialDone(eid string) {
	delete(m.dialing, eid)
}

func (m *tuiModel) dialFailed(eid, reason string) {
	st := m.dialing[eid]
	st.failed = true
	st.lastErr = reason
	m.setDial(eid, st)
	// A redial that failed leaves the tab it was asked from exactly where it
	// was, with its own error back in the bar.
	if m.redialing == eid {
		m.redialing = ""
	}
}

// connectingAny reports whether anything is mid-dial, which is what gates the
// spinner: a list that repaints twelve times a second for nothing is worse than
// no spinner at all.
func (m tuiModel) connectingAny() bool {
	for _, st := range m.dialing {
		if !st.failed {
			return true
		}
	}
	return m.connecting != ""
}

// retryPrompt is the question asked when a connection could not be made. It is
// a modal because the answer decides whether anything happens at all, and the
// status line was being missed.
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

// retryChoices draws the two answers with the chosen one highlighted. Both are
// padded before styling: padding a styled string counts its escape bytes
// towards the width and the two buttons drift apart.
func (m tuiModel) retryChoices() string {
	yes, no := " yes ", " no "
	if m.askRetryNo {
		return ui.StyleDim.Render(yes) + "  " + ui.StyleErr.Reverse(true).Bold(true).Render(no)
	}
	return ui.StyleOK.Reverse(true).Bold(true).Render(yes) + "  " + ui.StyleDim.Render(no)
}

// overlayRetry puts the prompt where the content was. The rows above it are
// kept so the list or the tab bar is still visible behind the question.
func (m tuiModel) overlayRetry(content string) string {
	prompt := m.retryPrompt()
	keep := max(0, m.height-ui.ModalHeight(len(strings.Split(prompt, "\n")))-1)

	lines := strings.Split(content, "\n")
	if len(lines) > keep {
		lines = lines[:keep]
	}
	return strings.Join(lines, "\n") + "\n" + prompt
}

// onRetryKey answers the retry prompt. Navigation moves between the answers;
// anything else that is not yes dismisses it, because an unexplained modal that
// will not go away is worse than a missed retry.
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
		// Only enter confirms. Space selects rows on the list behind this, and
		// muscle memory should not redial an ION.
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

// rowStatus is the no-header column beside each element: what this element is
// doing, and whether it already has a tab.
func (m tuiModel) rowStatus(el elementRow) string {
	if st, ok := m.dialing[el.ID]; ok {
		if st.failed {
			return ui.StyleErr.Render(ui.MarkCross.String())
		}
		return m.spinner.View()
	}
	s := m.sessions.Find(el.ID)
	if s == nil {
		for _, d := range m.sessions.Detached() {
			if d.ElementID == el.ID {
				return ui.StyleDim.Render("○")
			}
		}
		return " "
	}
	switch {
	case s.Err() != nil:
		return ui.StyleErr.Render(ui.MarkCross.String())
	case s.Hung():
		return ui.StyleWarn.Render("~")
	}
	return ui.StyleOK.Render("●")
}

// connectBar is the line under the tab bar. It keeps its row whatever the
// state, so the pane below it does not change height as a session connects.
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
		// Said on the dead tab itself, which is the one being looked at while
		// its replacement is dialled.
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
			// The socket is up and the appliance has said nothing. That is
			// what dialling an element that is not there looks like, and it
			// used to read as "successfully dialed" until the handshake
			// deadline failed it twenty seconds later.
			parts = append(parts, m.spinner.View()+" "+ui.StyleWarn.Render("waiting for the ion"))
		default:
			// What this half is about is the dial, not the appliance: the two
			// read as a contradiction while the session half said "connected",
			// and naming the socket it runs over only moved the confusion.
			// "successfully dialed · ion offline" is a state that happens and
			// now says so.
			parts = append(parts, ui.StyleOK.Render(ui.MarkCheck.String()+" successfully dialed"))
		}
	}

	parts = append(parts, m.ionLabel(s.ElementID))

	// Not while one is already in flight: the bar says "redial n/m" there, and
	// offering the key again reads as though nothing is happening.
	if (s.Hung() || s.Err() != nil) && m.redialing != s.ElementID {
		parts = append(parts, ui.StyleDim.Render("alt+r retry"))
	}
	return "  " + strings.Join(parts, ui.StyleDim.Render(" · "))
}

// awaitingDevice is a live session that has not heard from its appliance yet,
// which is the state the spinner has to keep ticking for.
func (m tuiModel) awaitingDevice() bool {
	s := m.sessions.Active()
	return s != nil && s.Err() == nil && !s.Hung() && !s.Ended() && !s.Answered()
}

// ionLabel is what the portal last said about the appliance, said as such.
// Next to a dial it is the only evidence there is -- an offline ION will not
// come up however many times it is tried -- and next to a live session it is
// the disagreement that matters: a stale cached list, or a box that has lost
// its controller channel while its CLI is perfectly well.
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

// shortDialErr trims the layers a failed handshake arrives wrapped in.
func shortDialErr(s string) string {
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		return s[i+2:]
	}
	return s
}
