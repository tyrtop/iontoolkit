package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type wfStep int

const (
	wfList wfStep = iota
	wfForm
	wfRunning
	wfDone
)

type wfState struct {
	step   wfStep
	prefix string
	title  string
	cursor int
	cmd    runtime.Command
	inputs []textinput.Model
	focus  int
	values map[string]string
	rest   []string
	doit   bool
	lines  []string
	err    string
	stop   context.CancelFunc
	events chan tea.Msg
	scroll int

	direct bool
	back   tuiPhase
}

type wfLineMsg string

type wfDoneMsg struct{ err string }

func (m tuiModel) openWorkflows() (tea.Model, tea.Cmd) {
	return m.openCommands(app.WorkflowPrefix, "Workflows"), nil
}

func (m tuiModel) openCommands(prefix, title string) tuiModel {
	m.wf = wfState{prefix: prefix, title: title}
	m.phase = phaseWorkflows
	m.status = strings.Join(m.runtime().Warnings(), "; ")
	return m
}

func (m tuiModel) listedCommands() []runtime.Command {
	return m.runtime().CommandsNamed(m.wf.prefix)
}

func (m tuiModel) openWorkflowForm() tuiModel {
	return m.openFormFor(m.listedCommands()[m.wf.cursor])
}

func (m tuiModel) openFormFor(cmd runtime.Command) tuiModel {
	m.wf.cmd, m.wf.err, m.wf.focus = cmd, "", 0
	m.wf.inputs = make([]textinput.Model, len(cmd.Args))
	for i, a := range cmd.Args {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = a.Default
		ti.SetWidth(40)
		if v, ok := m.wf.values[a.Name]; ok {
			ti.SetValue(v)
		}
		m.wf.inputs[i] = ti
	}
	if len(m.wf.inputs) > 0 {
		m.wf.inputs[0].Focus()
	}
	m.wf.step = wfForm
	return m
}

func (m tuiModel) formValues() map[string]string {
	out := map[string]string{}
	for i, a := range m.wf.cmd.Args {
		if v := strings.TrimSpace(m.wf.inputs[i].Value()); v != "" {
			out[a.Name] = v
		}
	}
	return out
}

func (m tuiModel) wfDoitBlocked() string {
	return ""
}

func (m tuiModel) bindPortal(tsg string) {
	name := m.tenantName(tsg)
	m.runtime().UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		c := m.tenantClient(tsg)
		if c == nil {
			return nil, runtime.Tenant{}, fmt.Errorf("could not reach %s", name)
		}
		return c, runtime.Tenant{TSG: tsg, Name: name}, nil
	})
}

func (m tuiModel) startWorkflow(doit bool) (tea.Model, tea.Cmd) {
	values := m.formValues()
	if err := m.runtime().CheckArgs(m.wf.cmd.Name, values); err != nil {
		m.wf.err = err.Error()
		return m, nil
	}
	m.bindPortal(m.tenant().TSGID)

	ctx, stop := context.WithCancel(m.ctx)
	events := make(chan tea.Msg, 64)
	m.wf.values, m.wf.doit, m.wf.lines, m.wf.err, m.wf.scroll = values, doit, nil, "", 0
	m.wf.stop, m.wf.events, m.wf.step = stop, events, wfRunning

	rt, name, rest := m.runtime(), m.wf.cmd.Name, m.wf.rest
	logging.L().Info("command started", "command", name, "doit", doit)
	go func() {
		defer stop()
		err := rt.Exec(&runtime.Call{Ctx: ctx, Args: values, Rest: rest, DoIt: doit,
			Out: func(line string) { events <- wfLineMsg(line) }}, name)
		done := wfDoneMsg{}
		if err != nil {
			done.err = err.Error()
		}
		events <- done
		close(events)
	}()
	return m, tea.Batch(m.spinner.Tick, waitForWorkflow(events))
}

func waitForWorkflow(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m tuiModel) onWorkflowLine(msg wfLineMsg) (tea.Model, tea.Cmd) {
	m.wf.lines = append(m.wf.lines, string(msg))
	return m, waitForWorkflow(m.wf.events)
}

func (m tuiModel) onWorkflowDone(msg wfDoneMsg) (tea.Model, tea.Cmd) {
	m.wf.err, m.wf.step, m.wf.stop = msg.err, wfDone, nil
	if m.wf.direct {
		m.syncFromRuntime()
	}
	return m, nil
}

func (m tuiModel) onWorkflowKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch m.wf.step {
	case wfList:
		switch key {
		case "esc", "q":
			m.phase = phaseMenu
			if m.wf.back != 0 {
				m.phase = m.wf.back
			}
		case "up", "k":
			m.wf.cursor = max(0, m.wf.cursor-1)
		case "down", "j":
			m.wf.cursor = min(len(m.listedCommands())-1, m.wf.cursor+1)
		case "enter":
			if len(m.listedCommands()) > 0 {
				m.wf.values = nil
				m = m.openWorkflowForm()
			}
		}

	case wfForm:
		switch key {
		case "esc":
			if m.wf.direct {
				return m.leaveCommand(), nil
			}
			m.wf.step, m.wf.err = wfList, ""
		case "tab", "down":
			m = m.moveWorkflowFocus(1)
		case "shift+tab", "up":
			m = m.moveWorkflowFocus(-1)
		case "enter":
			return m.startWorkflow(false)
		default:
			if len(m.wf.inputs) == 0 {
				return m, nil
			}
			var cmd tea.Cmd
			m.wf.inputs[m.wf.focus], cmd = m.wf.inputs[m.wf.focus].Update(msg)
			return m, cmd
		}

	case wfRunning:
		if key == "esc" && m.wf.stop != nil {
			m.wf.stop()
		}

	case wfDone:
		switch key {
		case "esc", "q":
			if m.wf.direct {
				return m.leaveCommand(), nil
			}
			m.wf.step = wfForm
		case "r":
			return m.startWorkflow(false)
		case "d":
			if m.wf.doit || m.wf.err != "" || !m.wf.cmd.Writes {
				return m, nil
			}
			if why := m.wfDoitBlocked(); why != "" {
				m.wf.err = why
				return m, nil
			}
			return m.startWorkflow(true)
		case "down", "j":
			m.wf.scroll = min(m.wf.scroll+1, max(0, len(m.wf.lines)-1))
		case "up", "k":
			m.wf.scroll = max(0, m.wf.scroll-1)
		}
	}
	return m, nil
}

func (m tuiModel) moveWorkflowFocus(by int) tuiModel {
	if len(m.wf.inputs) == 0 {
		return m
	}
	m.wf.inputs[m.wf.focus].Blur()
	m.wf.focus = (m.wf.focus + by + len(m.wf.inputs)) % len(m.wf.inputs)
	m.wf.inputs[m.wf.focus].Focus()
	return m
}

func (m tuiModel) viewWorkflows() string {
	var b strings.Builder
	width := max(20, m.width-1)
	title := m.wf.title
	if m.wf.step != wfList {
		title += " › " + m.wf.cmd.Help
	}
	b.WriteString(ui.StyleCmd.Render(title) + "\n\n")

	hint := "esc back"
	switch m.wf.step {
	case wfList:
		cmds := m.listedCommands()
		if len(cmds) == 0 {
			b.WriteString(ui.StyleDim.Render("nothing here yet — add a folder under workflows/ in the config directory") + "\n")
		}
		for i, c := range cmds {
			b.WriteString(pickLine(i == m.wf.cursor, c.Help, width) + "\n")
			if i == m.wf.cursor && c.Long != "" {
				b.WriteString("    " + ui.StyleDim.Render(ui.Truncate(c.Long, width-4)) + "\n")
			}
		}
		hint = "↑↓ move · enter open · esc menu"

	case wfForm:
		if d := m.wf.cmd.Long; d != "" {
			b.WriteString(ui.StyleDim.Render(ui.Truncate(d, width)) + "\n\n")
		}
		for i, a := range m.wf.cmd.Args {
			mark := "  "
			if i == m.wf.focus {
				mark = ui.StyleCmd.Render("> ")
			}
			req := ""
			if a.Required {
				req = ui.StyleDim.Render("  required")
			}
			label := a.Help
			if label == "" {
				label = a.Name
			}
			b.WriteString(mark + ui.Label(label) + m.wf.inputs[i].View() + req + "\n")
		}
		if len(m.wf.cmd.Args) == 0 {
			b.WriteString(ui.StyleDim.Render("this asks for nothing") + "\n")
		}
		hint = "tab/↑↓ field · enter dry run · esc back"
		if !m.wf.cmd.Writes {
			hint = "tab/↑↓ field · enter run · esc back"
		}

	case wfRunning, wfDone:
		mode := ui.StyleOK.Render("dry run")
		if m.wf.doit {
			mode = ui.StyleWarn.Render("doing it")
		}
		if !m.wf.cmd.Writes {
			mode = ui.StyleCmd.Render(m.wf.cmd.Name)
		}
		b.WriteString(mode + ui.StyleDim.Render("  "+inputLine(m.wf.values)) + "\n\n")
		rows := max(3, m.height-8)
		start := min(m.wf.scroll, max(0, len(m.wf.lines)-1))
		for i := start; i < len(m.wf.lines) && i < start+rows; i++ {
			b.WriteString("  " + ui.Truncate(m.wf.lines[i], width-2) + "\n")
		}
		if more := len(m.wf.lines) - start - rows; more > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more, ↓", more)) + "\n")
		}
		if m.wf.step == wfRunning {
			b.WriteString("  " + ui.StyleDim.Render(m.spinner.View()+" running") + "\n")
			hint = "esc stop"
			break
		}
		switch {
		case m.wf.err != "":
		case m.wf.doit || !m.wf.cmd.Writes:
			hint = "r run again · esc back"
		default:
			hint = "d do it · r run again · esc back"
		}
	}
	if m.wf.err != "" {
		b.WriteString("\n" + ui.StyleErr.Render(ui.Truncate(m.wf.err, width)) + "\n")
	}
	return b.String() + "\n" + ui.Footer(ui.Truncate(m.status, width), ui.Truncate(hint, width))
}

func inputLine(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	return strings.Join(parts, " ")
}
