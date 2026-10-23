package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/ui"
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

	where, err := m.writeExport(slug, ext, text)
	if err != nil {
		m.status = "export failed: " + err.Error()
		logging.L().Warn("export failed", "what", what, "err", err)
		return m, nil
	}

	m.status = fmt.Sprintf("exported %s, %s to %s", what, size(len(text)), where)
	logging.L().Info("exported", "what", what, "bytes", len(text), "path", where)
	return m, nil
}

func (m tuiModel) writeExport(slug, ext, text string) (string, error) {
	if m.store == nil {
		return "", fmt.Errorf("no config directory to write to")
	}

	path := filepath.Join(m.store.ExportsDir(), output.ExportName(slug, ext, time.Now()))
	f, where, err := output.Open(path, "", "", time.Now())
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(f, text+"\n"); err != nil {
		f.Close()
		return "", err
	}
	return where, f.Close()
}

func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1024)
}

func (m tuiModel) exportSession() (tea.Model, tea.Cmd) {
	s := m.sessions.Active()
	if s == nil {
		m.status = "nothing to export here"
		return m, nil
	}
	return m.exportText(s.Name+" scrollback", s.Name, "txt", s.Buffer())
}

func (m tuiModel) exportDataPage() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok {
		m.status = "nothing to export here"
		return m, nil
	}
	page := m.visiblePage(tab)
	lines := make([]string, 0, len(page.lines)+1)
	lines = append(lines, tab.name)
	for _, ln := range page.lines {
		lines = append(lines, strings.TrimRight(ui.Plain(ln), " "))
	}
	return m.exportText(tab.name+" data", tab.name, "txt", strings.Join(lines, "\n"))
}

func (m tuiModel) exportResults() (tea.Model, tea.Cmd) {
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
		return m.exportText(name+" output", name, "txt", strings.Join(lines, "\n"))
	}
	return m.exportRunJSON()
}

func (m tuiModel) exportRunJSON() (tea.Model, tea.Cmd) {
	var b strings.Builder
	sink := output.NewSink(&b, output.FormatJSON, output.WithOrientation(m.batchOrient()))
	for i, r := range m.results {
		if r.ElementID == "" {
			continue
		}
		if err := sink.Add(i, r); err != nil {
			m.status = "export failed: " + err.Error()
			return m, nil
		}
	}
	if err := sink.Close(); err != nil {
		m.status = "export failed: " + err.Error()
		return m, nil
	}
	return m.exportText(fmt.Sprintf("%d results as json", m.completedCount()), "run", "json", b.String())
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
