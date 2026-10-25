package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

func newMacroFilter() textinput.Model {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "filter"
	in.SetWidth(26)
	return in
}

func (m tuiModel) openMacros() (tea.Model, tea.Cmd) {
	m.macroFilter = newMacroFilter()
	m.macroFilterOn = false
	m.status = ""
	m.phase = phaseMacros
	return m.resetMacroCursor(), nil
}

func (m tuiModel) macroRows() []config.Macro {
	return m.runtime().MacrosMatching(m.macroFilter.Value())
}

func (m tuiModel) macroAt(cursor int) (config.Macro, bool) {
	rows := m.macroRows()
	if cursor < 1 || cursor > len(rows) {
		return config.Macro{}, false
	}
	return rows[cursor-1], true
}

func (m tuiModel) resetMacroCursor() tuiModel {
	m.macroCursor = 0
	if len(m.macroRows()) > 0 {
		m.macroCursor = 1
	}
	return m
}

func (m tuiModel) onMacroKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	armed := m.macroDel
	if key != "x" {
		m.macroDel = ""
	}

	if m.macroFilterOn {
		switch key {
		case "esc":
			m.macroFilterOn = false
			m.macroFilter.SetValue("")
			m.macroFilter.Blur()
			return m.resetMacroCursor(), nil
		case "enter":
			m.macroFilterOn = false
			m.macroFilter.Blur()
			return m, nil
		}
		if key == "backspace" && m.macroFilter.Value() == "" {
			m.macroFilterOn = false
			m.macroFilter.Blur()
			return m.resetMacroCursor(), nil
		}
		var cmd tea.Cmd
		m.macroFilter, cmd = m.macroFilter.Update(msg)
		return m.resetMacroCursor(), cmd
	}

	if key == "x" {
		return m.armOrDeleteMacro(armed)
	}
	if next, cmd, ran := m.bindingOn(scrMacros, key); ran {
		return next, cmd
	}
	switch key {
	case "up", "k":
		m.macroCursor = max(0, m.macroCursor-1)
	case "down", "j":
		m.macroCursor = min(len(m.macroRows()), m.macroCursor+1)
	}
	return m, nil
}

func (m tuiModel) macrosBack() (tea.Model, tea.Cmd) {
	m.phase = phaseMenu
	m.status = ""
	return m, nil
}

func (m tuiModel) openMacroFilter() (tea.Model, tea.Cmd) {
	m.macroFilter.SetValue("")
	m.macroCursor = 0
	m.macroFilterOn = true
	return m, m.macroFilter.Focus()
}

func (m tuiModel) editMacro() (tea.Model, tea.Cmd) {
	if mac, ok := m.macroAt(m.macroCursor); ok {
		return m.openMacroForm(mac)
	}
	return m, nil
}

func (m tuiModel) pickMacro() (tea.Model, tea.Cmd) {
	if mac, ok := m.macroAt(m.macroCursor); ok {
		return m.armMacro(mac)
	}
	return m.openMacroForm(config.Macro{})
}

func (m tuiModel) armOrDeleteMacro(armed string) (tea.Model, tea.Cmd) {
	mac, ok := m.macroAt(m.macroCursor)
	if !ok {
		return m, nil
	}
	if !strings.EqualFold(armed, mac.Name) {
		m.macroDel = mac.Name
		m.status = "x again to delete " + mac.Name
		return m, nil
	}

	m.runtime().RemoveMacro(mac.Name)
	m.macroCursor = max(0, min(m.macroCursor, len(m.macroRows())))
	m.status = "deleted " + mac.Name + " · u puts it back"
	return m, m.saveCommands()
}

func (m tuiModel) undoMacroDelete() (tea.Model, tea.Cmd) {
	mac, n, err := m.runtime().UndoMacro()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.status = mac.Name + " is back"
	if n > 0 {
		m.status += fmt.Sprintf(" · %d more to undo", n)
	}
	return m, m.saveCommands()
}

func (m tuiModel) armMacro(mac config.Macro) (tea.Model, tea.Cmd) {
	m.runtime().ArmMacro(mac.Name)
	m.status = mac.Name + ": select elements, then enter"

	if len(m.rows()) == 0 {
		return m.startContinue()
	}
	m.phase = phasePicking
	m.rebuildRows()
	return m, nil
}

func (m tuiModel) runArmedMacro(rows []elementRow) (tea.Model, tea.Cmd) {
	mac, ok := m.runtime().Macro(m.armed())
	if !ok {
		m.runtime().Disarm()
		m.status = "that macro is gone — pick another from the menu"
		return m, nil
	}
	return m.runMacro(mac, rows)
}

func (m tuiModel) runMacro(mac config.Macro, rows []elementRow) (tea.Model, tea.Cmd) {
	return m.startMacro(mac.Name, rows, false)
}

func (m tuiModel) startMacro(name string, rows []elementRow, anyway bool) (tea.Model, tea.Cmd) {
	m.status = "starting the run"
	rt, ctx := m.runtime(), m.ctx
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		events, err := rt.StartMacro(ctx, name, rows, anyway)
		return batchStartedMsg{events: events, err: err, macro: name, rows: rows}
	})
}

func (m tuiModel) macroWarnPrompt() string {
	mac, _ := m.runtime().Macro(m.askMacro)

	lines := []string{
		ui.StyleWarn.Render(ui.Truncate(mac.Name, 30)) + ui.StyleWarn.Render(" was built on other hardware"),
		ui.StyleDim.Render(ui.Truncate(m.askMacroWhy, 52)),
		ui.StyleDim.Render("command syntax differs by model and version"),
		"",
		"run it anyway",
		m.retryChoices(),
		"",
		ui.StyleDim.Render("answered once, then never asked again"),
	}
	return ui.Modal(lines, m.width)
}

func (m tuiModel) overlayMacroWarn(content string) string {
	prompt := m.macroWarnPrompt()
	keep := max(0, m.height-ui.ModalHeight(len(strings.Split(prompt, "\n")))-1)
	lines := strings.Split(content, "\n")
	if len(lines) > keep {
		lines = lines[:keep]
	}
	return strings.Join(lines, "\n") + "\n" + prompt
}

func (m tuiModel) onMacroWarnKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.askRetryNo = !m.askRetryNo
		return m, nil
	}

	name, rows := m.askMacro, m.askMacroOn
	m.askMacro, m.askMacroOn, m.askMacroWhy = "", nil, ""
	yes := key == "y" || key == "Y" || (key == "enter" && !m.askRetryNo)
	m.askRetryNo = false
	if !yes {
		m.status = "not run"
		return m, nil
	}
	return m.startMacro(name, rows, true)
}

func macroCommandLabel(i int) string { return fmt.Sprintf("command %d", i+1) }

func macroPlaceholder(i int) string {
	if i == 0 {
		return "dump device status"
	}
	return ""
}

func newMacroCommand(i int) formField {
	return newField(macroCommandLabel(i), macroPlaceholder(i), false)
}

func (m tuiModel) openMacroForm(mac config.Macro) (tea.Model, tea.Cmd) {
	name := newField("name", "wan check", false)
	name.input.SetValue(mac.Name)
	fields := []formField{name}
	for i, c := range mac.Commands {
		f := newMacroCommand(i)
		f.input.SetValue(c)
		fields = append(fields, f)
	}

	fields = append(fields, newMacroCommand(len(mac.Commands)))

	m.formMacroName = mac.Name
	m.cmdPick = newSuggest(nil, "")
	title := "New macro"
	if mac.Name != "" {
		title = "Edit " + mac.Name
	}
	return m.openForm(formMacro, title, phaseAddUser, fields)
}

func (m tuiModel) addMacroCommand() (tea.Model, tea.Cmd) {
	m.form[m.formCursor].input.Blur()
	m.form = append(m.form, newMacroCommand(len(m.form)-1))
	m.formCursor = len(m.form) - 1
	return m.syncMacroSuggestions(), m.form[m.formCursor].input.Focus()
}

func (m tuiModel) removeMacroCommand() (tea.Model, tea.Cmd) {
	m.form[m.formCursor].input.Blur()
	m.form = append(m.form[:m.formCursor], m.form[m.formCursor+1:]...)
	for i := 1; i < len(m.form); i++ {
		m.form[i].label = macroCommandLabel(i - 1)
		m.form[i].input.Placeholder = macroPlaceholder(i - 1)
	}

	m.formCursor = max(1, min(m.formCursor, len(m.form)-1))
	in := &m.form[m.formCursor].input
	in.SetCursor(len(in.Value()))
	return m.syncMacroSuggestions(), in.Focus()
}

func (m tuiModel) macroFormCommands() []string {
	var out []string
	for i := 1; i < len(m.form); i++ {
		if v := m.formValue(i); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (m tuiModel) submitMacro() (tea.Model, tea.Cmd) {
	var said []string
	c := &runtime.Call{Out: func(l string) { said = append(said, l) }, Rest: m.macroFormCommands(),
		Args: map[string]string{"name": m.formValue(0), "was": m.formMacroName}}
	if err := m.runtime().Exec(c, "macro.set"); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.form, m.formKind = nil, formNone
	m.phase = m.formReturn
	m.status = strings.Join(said, " ")
	return m, nil
}

func (m tuiModel) viewMacros() string {
	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render("macros"))

	rows := m.runtime().Selected()
	b.WriteString(ui.StyleDim.Render(fmt.Sprintf("   %d element(s) selected", len(rows))))
	if m.macroFilterOn || m.macroFilter.Value() != "" {
		b.WriteString("   " + m.macroFilter.View())
	}
	b.WriteString("\n\n")

	width := max(20, m.width-2)
	line := func(here bool, plain, styled string) string {
		if here {
			return ui.StyleCmd.Render("> ") + ui.StyleRow.Render(ui.Truncate(plain, width)) + "\n"
		}
		return "  " + styled + "\n"
	}

	const newRow = "+ New Macro"
	b.WriteString(line(m.macroCursor == 0, newRow, ui.StyleGhost.Render(newRow)))

	list := m.macroRows()
	if len(m.runtime().Macros()) > 0 && len(list) == 0 {
		b.WriteString(ui.StyleDim.Render("  nothing matches the filter") + "\n")
	}

	for i, mac := range list {
		name := fmt.Sprintf("%-22s", ui.Truncate(mac.Name, 22))
		count := fmt.Sprintf("%-14s", fmt.Sprintf("%d command(s)", len(mac.Commands)))
		cmds := ui.Truncate(strings.Join(mac.Commands, " · "), max(10, m.width-58))

		model, tag := "", ""
		if mac.Model != "" {
			model = mac.Model + "  "
			style := ui.StyleDim
			if sel := m.runtime().SelectionModel(); sel != "" && sel != mac.Model {
				style = ui.StyleWarn
			}
			tag = style.Render(model)
		}

		b.WriteString(line(i+1 == m.macroCursor,
			name+count+model+cmds,
			ui.StyleElement.Render(name)+ui.StyleDim.Render(count)+tag+ui.StyleDim.Render(cmds)))
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status,
		"↑↓ move · e edit · x delete · u undo · / filter · esc back"))
	return b.String()
}

func (m tuiModel) modeLabel() string {
	label := ui.StyleElement.Render(ui.Title(m.mode()) + " Mode")
	if m.mode() == modeMacro && m.armed() != "" {
		label += ui.StyleCmd.Render("  " + m.armed())
	}
	return label
}

func (m tuiModel) pasteMacroCommands(cmds []string) (tea.Model, tea.Cmd) {
	m.form[m.formCursor].input.Blur()

	at := m.formCursor
	for i, c := range cmds {
		if at+i >= len(m.form) {
			m.form = append(m.form, newMacroCommand(len(m.form)-1))
		}
		m.form[at+i].input.SetValue(c)
	}
	m.form = append(m.form, newMacroCommand(len(m.form)-1))

	m.formCursor = len(m.form) - 1
	in := &m.form[m.formCursor].input
	in.SetCursor(len(in.Value()))
	return m.syncMacroSuggestions(), in.Focus()
}

func (m tuiModel) listReturn() tuiPhase {
	if m.mode() == modeMacro && m.armed() != "" {
		return phaseMacros
	}
	return phaseMenu
}
