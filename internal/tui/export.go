package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/runtime"
)

const (
	exportKey     = "alt+y"
	exportKeyHint = exportKey + " export"
)

func (m tuiModel) exportText(what, slug, ext, text string) (tuiModel, tea.Cmd) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		m.status = "nothing to export here"
		return m, nil
	}

	where, err := m.runtime().Export(slug, ext, text)
	if err != nil {
		m.status = "export failed: " + err.Error()
		logging.L().Warn("export failed", "what", what, "err", err)
		return m, nil
	}

	m.status = fmt.Sprintf("exported %s, %s to %s", what, size(len(text)), where)
	logging.L().Info("exported", "what", what, "bytes", len(text), "path", where)
	return m, nil
}

func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1024)
}

func (m tuiModel) exportSession() (tea.Model, tea.Cmd) { return m.exportCommand("session.export", nil) }

func (m tuiModel) exportDataPage() (tea.Model, tea.Cmd) { return m.exportCommand("data.export", nil) }

func (m tuiModel) exportCommand(name string, args map[string]string) (tea.Model, tea.Cmd) {
	var said []string
	if err := m.runtime().Exec(&runtime.Call{Args: args, Out: func(l string) { said = append(said, l) }}, name); err != nil {
		m.status = "nothing to export here: " + err.Error()
		return m, nil
	}
	m.status = strings.Join(said, " ")
	return m, nil
}

func (m tuiModel) exportResults() (tea.Model, tea.Cmd) {
	idx, ok := m.currentResult()
	if ok && m.resOpen {
		return m.exportCommand("batch.export", map[string]string{"element": m.batch().Results[idx].ElementID})
	}
	return m.exportRunJSON()
}

func (m tuiModel) exportRunJSON() (tea.Model, tea.Cmd) {
	where, err := m.runtime().SaveBatch("", m.runtime().ExportsDir(), output.FormatJSON, "")
	if err != nil {
		m.status = "export failed: " + err.Error()
		return m, nil
	}
	m.status = fmt.Sprintf("exported %d results as json to %s", m.completedCount(), where)
	return m, nil
}

func pasteCommands(text string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (m tuiModel) onDebugKey(key string) (tea.Model, tea.Cmd) {
	if key != "enter" {
		return m, nil
	}
	if m.setCursor == logsRow {
		return m.exportLog()
	}
	return m.onTestingKey(key)
}

func (m tuiModel) exportLog() (tea.Model, tea.Cmd) {
	path := logging.Path()
	if !logging.On() || path == "" {
		m.status = "no log this run"
		return m, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.exportText("the log", "log", "jsonl", string(b))
}
