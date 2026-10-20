package tui

import (
	"strings"

	"tyrtop.com/iontk/internal/ui"
)

func lastSegment(s string) string {
	if i := strings.LastIndex(s, ","); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}

func replaceLastSegment(s, with string) string {
	if i := strings.LastIndex(s, ","); i >= 0 {
		return strings.TrimRight(s[:i+1], " ") + " " + with
	}
	return with
}

func (m tuiModel) refilterCommands(query string) tuiModel {
	m.cmdPick = m.cmdPick.refilter(m.catalog(), query)
	return m
}

func (m tuiModel) cmdSuggestBlock() string {
	var b strings.Builder
	b.WriteString(m.cmdPick.view(m.width, suggestRows))
	line := ui.StyleDim.Render("ctrl+n/ctrl+p pick a command · tab completes it") +
		"   " + m.cmdPick.count("commands")
	if pick, ok := m.cmdPick.current(); ok {
		line = ui.StyleDim.Render("tab inserts ") + ui.StyleElement.Render(pick)
	}
	b.WriteString(line + "\n")
	return b.String()
}

func (m tuiModel) insertBatchCommand(pick string) tuiModel {
	v := replaceLastSegment(m.cmdInput.Value(), pick)
	m.cmdInput.SetValue(v)
	m.cmdInput.SetCursor(len(v))
	return m.refilterCommands(lastSegment(v))
}

func (m tuiModel) syncMacroSuggestions() tuiModel {
	if m.formKind != formMacro || m.formCursor < 1 {
		m.cmdPick = newSuggest(nil, "")
		return m
	}
	return m.refilterCommands(m.formValue(m.formCursor))
}

func (m tuiModel) insertMacroCommand(pick string) tuiModel {
	in := &m.form[m.formCursor].input
	in.SetValue(pick)
	in.SetCursor(len(pick))
	return m.syncMacroSuggestions()
}
