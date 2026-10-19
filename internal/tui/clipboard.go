package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/ui"
)

// terminals cap OSC 52 and truncate in silence; cutting here can say so
const clipLimit = 100 << 10

const copyKeyHint = "alt+y copy"

// OSC 52 never answers, so the footer has to admit that silence means
// the terminal refused it.
func (m tuiModel) copyText(what, text string) (tuiModel, tea.Cmd) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		m.status = "nothing to copy here"
		return m, nil
	}

	sent, dropped := clipTail(text)
	if dropped > 0 {
		m.status = fmt.Sprintf("copied the last %s of %s — %s would not fit",
			size(len(sent)), what, size(dropped))
	} else {
		m.status = fmt.Sprintf("copied %s, %s", what, size(len(sent)))
	}
	m.status += " · nothing arrived? your terminal refused OSC 52"
	return m, tea.SetClipboard(sent)
}

// the newest output is the part being copied, and half a line is noise
func clipTail(text string) (string, int) {
	if len(text) <= clipLimit {
		return text, 0
	}
	cut := text[len(text)-clipLimit:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	// a byte cut can land inside a rune
	cut = strings.ToValidUTF8(cut, "")
	return cut, len(text) - len(cut)
}

func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1024)
}

// ids are what gets pasted into --element; a ticked selection wins
func (m tuiModel) copyElements() (tea.Model, tea.Cmd) {
	rows, ids := m.selectedRows()
	if len(ids) > 0 {
		what := fmt.Sprintf("%d element ids", len(rows))
		// one per line, because that is what -f reads
		return m.copyText(what, strings.Join(ids, "\n"))
	}
	el, ok := m.current()
	if !ok {
		m.status = "nothing to copy here"
		return m, nil
	}
	return m.copyText(el.label()+"'s id", el.ID)
}

// the whole buffer, not the window; a dead tab copies too
func (m tuiModel) copySession() (tea.Model, tea.Cmd) {
	s := m.sessions.Active()
	if s == nil {
		m.status = "nothing to copy here"
		return m, nil
	}
	return m.copyText(s.Name+" scrollback", s.Buffer())
}

// the page as shown: the pane has a scroll offset and no cursor
func (m tuiModel) copyDataPage() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok {
		m.status = "nothing to copy here"
		return m, nil
	}
	// what a filter has narrowed it to, not the whole page
	page := m.visiblePage(tab)
	lines := make([]string, 0, len(page.lines)+1)
	lines = append(lines, tab.name)
	for _, ln := range page.lines {
		lines = append(lines, strings.TrimRight(ui.Plain(ln), " "))
	}
	return m.copyText(tab.name+" data", strings.Join(lines, "\n"))
}

// the open result, or the whole run when nothing is open
func (m tuiModel) copyResults() (tea.Model, tea.Cmd) {
	idx, ok := m.currentResult()
	if ok && m.resOpen {
		r := m.results[idx]
		name := r.Name
		if name == "" {
			name = r.ElementID
		}
		lines := make([]string, 0, len(m.resultDetail(idx)))
		for _, ln := range m.resultDetail(idx) {
			lines = append(lines, strings.TrimRight(ui.Plain(ln), " "))
		}
		return m.copyText(name+" output", strings.Join(lines, "\n"))
	}
	return m.copyRunJSON()
}

// half a JSON document parses as nothing, so an oversize run is refused
func (m tuiModel) copyRunJSON() (tea.Model, tea.Cmd) {
	var b strings.Builder
	sink := output.NewSink(&b, output.FormatJSON, output.WithOrientation(m.batchOrient()))
	for i, r := range m.results {
		if r.ElementID == "" {
			continue
		}
		if err := sink.Add(i, r); err != nil {
			m.status = "copy failed: " + err.Error()
			return m, nil
		}
	}
	if err := sink.Close(); err != nil {
		m.status = "copy failed: " + err.Error()
		return m, nil
	}
	if b.Len() > clipLimit {
		m.status = fmt.Sprintf("the run is %s, too big for a clipboard — s saves it to a file", size(b.Len()))
		return m, nil
	}
	return m.copyText(fmt.Sprintf("%d results as json", m.completedCount()), b.String())
}

// one per line, so one macro can be pasted into another
func (m tuiModel) copyMacro() (tea.Model, tea.Cmd) {
	mac, ok := m.macroAt(m.macroCursor)
	if !ok {
		m.status = "nothing to copy here"
		return m, nil
	}
	return m.copyText(mac.Name, strings.Join(mac.Commands, "\n"))
}

// a pasted block is a list, like app.SplitCommands with commas
func pasteCommands(text string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
