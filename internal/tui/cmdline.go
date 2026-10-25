package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

func newCmdline() textinput.Model {
	in := textinput.New()
	in.Prompt = ":"
	in.Placeholder = "command  (tab completes)"
	in.SetWidth(60)
	return in
}

func (m tuiModel) openCmdline() (tea.Model, tea.Cmd) {
	m.cmdline = newCmdline()
	m.cmdlineOn = true
	m.status = ""
	return m, m.cmdline.Focus()
}

func (m tuiModel) closeCmdline() tuiModel {
	m.cmdlineOn = false
	m.cmdline.Blur()
	return m
}

func (m tuiModel) onCmdlineKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		return m.closeCmdline(), nil
	case "backspace":
		if m.cmdline.Value() == "" {
			return m.closeCmdline(), nil
		}
	case "tab":
		m.cmdline.SetValue(m.completeLine(m.cmdline.Value()))
		m.cmdline.CursorEnd()
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.cmdline.Value())
		m = m.closeCmdline()
		if line == "" {
			return m, nil
		}
		return m.execLine(line)
	}
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(msg)
	return m, cmd
}

func (m tuiModel) completeLine(line string) string {
	if strings.ContainsAny(line, " \t") {
		return line
	}
	names := m.runtime().Complete(line)
	if len(names) == 0 {
		return line
	}
	common := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	if len(names) == 1 {
		return common + " "
	}
	return common
}

func (m tuiModel) cmdlineHint() string {
	line := m.cmdline.Value()
	name, _, typed := strings.Cut(line, " ")
	if !typed {
		names := m.runtime().Complete(name)
		if len(names) > 8 {
			names = append(names[:8], "…")
		}
		return strings.Join(names, "  ")
	}
	cmd, ok := m.runtime().Command(name)
	if !ok {
		return "no command is called " + name
	}
	parts := []string{cmd.Help}
	for _, a := range cmd.Args {
		parts = append(parts, a.Name+"=")
	}
	return strings.Join(parts, "  ")
}

func (m tuiModel) overlayCmdline(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	width := max(10, m.width-1)
	box := []string{
		ui.StyleDim.Render(ui.Truncate(m.cmdlineHint(), width)),
		m.cmdline.View(),
	}
	keep := max(0, len(lines)-len(box))
	return strings.Join(append(lines[:keep], box...), "\n")
}

func (m *tuiModel) runsCommand(line string) bool {
	_, _, _, err := m.runtime().Parse(line)
	return err == nil
}

func (m tuiModel) execLine(line string) (tea.Model, tea.Cmd) {
	cmd, args, rest, err := m.runtime().Parse(line)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	return m.execCommand(cmd, args, rest)
}

func (m tuiModel) execCommand(cmd runtime.Command, args map[string]string, rest []string) (tea.Model, tea.Cmd) {
	m.seenSessions, m.seenData = m.sessions().Count(), len(m.dataTabs())
	missing := m.runtime().CheckArgs(cmd.Name, args) != nil
	if cmd.Slow || missing || cmd.Writes {
		m = m.openCommand(cmd, args, rest)
		if missing {
			return m, nil
		}
		return m.startWorkflow(false)
	}

	var lines []string
	c := &runtime.Call{Ctx: m.ctx, Args: args, Rest: rest, Out: func(l string) { lines = append(lines, l) }}
	err := m.runtime().Exec(c, cmd.Name)
	if l, ok := c.Result.(app.List); ok {
		m.drawRuntime(l)
	}
	m.syncFromRuntime()
	switch {
	case err != nil:
		m.status = cmd.Name + ": " + err.Error()
	case len(lines) > 1:
		m = m.openCommand(cmd, args, rest)
		m.wf.lines, m.wf.step = lines, wfDone
	case len(lines) == 1:
		m.status = lines[0]
	default:
		m.status = m.runtime().Status()
	}
	return m, nil
}

func (m tuiModel) openCommand(cmd runtime.Command, args map[string]string, rest []string) tuiModel {
	back := m.phase
	m.wf = wfState{title: "Command", values: args, rest: rest, direct: true, back: back}
	m.phase = phaseWorkflows
	return m.openFormFor(cmd)
}

func (m tuiModel) leaveCommand() tuiModel {
	m.phase = m.wf.back
	m.syncFromRuntime()
	return m
}

func (m *tuiModel) syncFromRuntime() {
	if q := m.runtime().Query(); q != m.filter.Value() {
		m.filter.SetValue(q)
	}
	m.forgetRows()
	m.rebuildRows()
	switch {
	case m.sessions().Count() > m.seenSessions:
		m.phase = phaseSession
	case len(m.dataTabs()) > m.seenData:
		m.phase = phaseData
	}
	m.seenSessions, m.seenData = m.sessions().Count(), len(m.dataTabs())
}
