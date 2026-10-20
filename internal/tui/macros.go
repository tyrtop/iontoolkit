package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
)

const warnMacroModel = "macro/model"

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
	all := m.cmds.Macros
	q := strings.ToLower(strings.TrimSpace(m.macroFilter.Value()))
	if q == "" {
		return all
	}
	var out []config.Macro
	for _, mac := range all {
		hay := strings.ToLower(mac.Name + " " + strings.Join(mac.Commands, " "))
		if strings.Contains(hay, q) {
			out = append(out, mac)
		}
	}
	return out
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

	switch key {
	case "esc", "q":
		m.phase = phaseMenu
		m.status = ""
		return m, nil
	case "/":
		m.macroFilter.SetValue("")
		m.macroCursor = 0
		m.macroFilterOn = true
		return m, m.macroFilter.Focus()
	case "up", "k":
		m.macroCursor = max(0, m.macroCursor-1)
		return m, nil
	case "down", "j":
		m.macroCursor = min(len(m.macroRows()), m.macroCursor+1)
		return m, nil
	case "e":
		if mac, ok := m.macroAt(m.macroCursor); ok {
			return m.openMacroForm(mac)
		}
		return m, nil
	case "x":
		return m.armOrDeleteMacro(armed)
	case "u":
		return m.undoMacroDelete()
	case "enter":
		if mac, ok := m.macroAt(m.macroCursor); ok {
			return m.armMacro(mac)
		}
		return m.openMacroForm(config.Macro{})
	}
	return m, nil
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

	m.macroUndo = append(slices.Clone(m.macroUndo), mac)
	m.cmds.RemoveMacro(mac.Name)
	m.macroCursor = max(0, min(m.macroCursor, len(m.macroRows())))
	m.status = "deleted " + mac.Name + " · u puts it back"
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) undoMacroDelete() (tea.Model, tea.Cmd) {
	if len(m.macroUndo) == 0 {
		m.status = "nothing to undo"
		return m, nil
	}
	last := len(m.macroUndo) - 1
	mac := m.macroUndo[last]
	m.macroUndo = slices.Clone(m.macroUndo[:last])

	if err := m.cmds.SetMacro(mac); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.status = mac.Name + " is back"
	if n := len(m.macroUndo); n > 0 {
		m.status += fmt.Sprintf(" · %d more to undo", n)
	}
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) armMacro(mac config.Macro) (tea.Model, tea.Cmd) {
	m.armedMacro = mac.Name
	m.mode = modeMacro
	m.status = mac.Name + ": select elements, then enter"

	if len(m.all) == 0 {
		return m.startContinue()
	}
	m.phase = phasePicking
	m.rebuildRows()
	return m, nil
}

func (m tuiModel) runArmedMacro(rows []elementRow) (tea.Model, tea.Cmd) {
	mac, ok := m.cmds.Macro(m.armedMacro)
	if !ok {
		m.mode = modeShell
		m.armedMacro = ""
		m.status = "that macro is gone — pick another from the menu"
		return m, nil
	}
	return m.runMacro(mac, rows)
}

func (m tuiModel) runMacro(mac config.Macro, rows []elementRow) (tea.Model, tea.Cmd) {
	if !app.Defaults.AllowWrites && !m.cfg.AllowWrites {
		if err := ioncli.CheckReadOnly(mac.Commands); err != nil {
			m.status = mac.Name + ": " + err.Error()
			return m, nil
		}
	}

	if len(rows) == 0 {
		m.status = "nothing selected — pick elements in the list, then run the macro"
		return m, nil
	}

	if macroMismatch(mac, rows) != "" && !m.cmds.Acked(warnMacroModel) {
		m.askMacro = mac.Name
		m.askMacroOn = rows
		return m, nil
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return m.startRunOn(mac.Commands, rows, ids)
}

func macroMismatch(mac config.Macro, rows []elementRow) string {
	if mac.Model == "" {
		return ""
	}
	for _, r := range rows {
		if r.Model != "" && r.Model != mac.Model {
			return fmt.Sprintf("built on %s, selection has %s", mac.Model, r.Model)
		}
	}
	return ""
}

func (m tuiModel) macroWarnPrompt() string {
	mac, _ := m.cmds.Macro(m.askMacro)
	rows := m.askMacroOn

	lines := []string{
		ui.StyleWarn.Render(ui.Truncate(mac.Name, 30)) + ui.StyleWarn.Render(" was built on other hardware"),
		ui.StyleDim.Render(ui.Truncate(macroMismatch(mac, rows), 52)),
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
	m.askMacro, m.askMacroOn = "", nil
	yes := key == "y" || key == "Y" || (key == "enter" && !m.askRetryNo)
	m.askRetryNo = false
	if !yes {
		m.status = "not run"
		return m, nil
	}

	m.cmds.Ack(warnMacroModel)
	save := saveCommands(m.store, m.cmds)
	mac, ok := m.cmds.Macro(name)
	if !ok {
		return m, save
	}
	next, cmd := m.runMacro(mac, rows)
	return next, tea.Batch(save, cmd)
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
	return m, m.form[m.formCursor].input.Focus()
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
	return m, in.Focus()
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
	name := m.formValue(0)
	mac := config.Macro{Name: name, Commands: m.macroFormCommands(), Model: m.selectionModel()}

	if m.formMacroName != "" && !strings.EqualFold(m.formMacroName, name) {
		m.cmds.RemoveMacro(m.formMacroName)
	}
	if err := m.cmds.SetMacro(mac); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.form, m.formKind = nil, formNone
	m.phase = m.formReturn
	m.status = fmt.Sprintf("saved %s, %d command(s)", mac.Name, len(mac.Commands))
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) selectionModel() string {
	rows, _ := m.selectedRows()
	model := ""
	for _, r := range rows {
		switch {
		case r.Model == "":
			continue
		case model == "":
			model = r.Model
		case model != r.Model:
			return ""
		}
	}
	return model
}

func (m tuiModel) viewMacros() string {
	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render("macros"))

	rows, _ := m.selectedRows()
	b.WriteString(ui.StyleDim.Render(fmt.Sprintf("   %d element(s) selected", len(rows))))
	if m.macroFilterOn || m.macroFilter.Value() != "" {
		b.WriteString("   " + m.macroFilter.View())
	}
	b.WriteString("\n\n")

	width := max(20, m.width-2)
	line := func(here bool, plain, styled string) string {
		if here {
			return ui.StyleRow.Render(fmt.Sprintf("%-*s", width, ui.Truncate(plain, width))) + "\n"
		}
		return "  " + styled + "\n"
	}

	const newRow = "+ New Macro"
	b.WriteString(line(m.macroCursor == 0, newRow, ui.StyleGhost.Render(newRow)))

	list := m.macroRows()
	if len(m.cmds.Macros) > 0 && len(list) == 0 {
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
			if sel := m.selectionModel(); sel != "" && sel != mac.Model {
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
		"↑↓ move · e edit · x delete · u undo · / filter · esc menu"))
	return b.String()
}

func (m tuiModel) modeLabel() string {
	label := ui.StyleElement.Render(ui.Title(m.mode.String()) + " Mode")
	if m.mode == modeMacro && m.armedMacro != "" {
		label += ui.StyleCmd.Render("  " + m.armedMacro)
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
	return m, in.Focus()
}

func (m tuiModel) listReturn() tuiPhase {
	if m.mode == modeMacro && m.armedMacro != "" {
		return phaseMacros
	}
	return phaseMenu
}
