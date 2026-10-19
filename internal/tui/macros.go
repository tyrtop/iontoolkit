package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
)

// warnMacroModel is the ID of the cross-model warning. Warnings are keyed by
// ID rather than given a field apiece, so the next one costs a constant.
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

// macroRows is the list as filtered. Name and commands both match: a macro is
// remembered by what it does as often as by what it was called.
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

// macroAt maps the cursor to a macro. Cursor 0 is the "+ new macro" row, an
// action rather than a macro, so it answers nothing.
func (m tuiModel) macroAt(cursor int) (config.Macro, bool) {
	rows := m.macroRows()
	if cursor < 1 || cursor > len(rows) {
		return config.Macro{}, false
	}
	return rows[cursor-1], true
}

// resetMacroCursor puts the cursor on the first macro, or on the + row when
// there is none: filtering is done to find a macro, not to make one.
func (m tuiModel) resetMacroCursor() tuiModel {
	m.macroCursor = 0
	if len(m.macroRows()) > 0 {
		m.macroCursor = 1
	}
	return m
}

func (m tuiModel) onMacroKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
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
		// Backspace on an already-empty query leaves, the same as on the
		// element list: the last press deletes the "/".
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
	case "alt+y":
		return m.copyMacro()
	case "ctrl+d", "x":
		return m.deleteMacro()
	case "enter":
		// The top row makes one; everything below it is picked up and taken
		// to the element list.
		if mac, ok := m.macroAt(m.macroCursor); ok {
			return m.armMacro(mac)
		}
		return m.openMacroForm(config.Macro{})
	}
	return m, nil
}

func (m tuiModel) deleteMacro() (tea.Model, tea.Cmd) {
	mac, ok := m.macroAt(m.macroCursor)
	if !ok {
		return m, nil
	}
	m.cmds.RemoveMacro(mac.Name)
	m.macroCursor = max(0, min(m.macroCursor, len(m.macroRows())))
	m.status = "deleted " + mac.Name
	return m, saveCommands(m.store, m.cmds)
}

// armMacro picks a macro up and goes to the element list to choose what to
// run it on, which is batch mode's order of doing things: the list is where
// elements are found, filtered and selected, and a macro page could only ever
// be a worse copy of it.
func (m tuiModel) armMacro(mac config.Macro) (tea.Model, tea.Cmd) {
	m.armedMacro = mac.Name
	m.mode = modeMacro
	m.status = mac.Name + ": select elements, then enter"

	// Reached from the menu, the list may never have been loaded.
	if len(m.all) == 0 {
		return m.startContinue()
	}
	m.phase = phasePicking
	m.rebuildRows()
	return m, nil
}

// runArmedMacro is enter in macro mode.
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

// runMacro runs the sequence against the rows enter acted on, through the
// same batch engine a typed command goes through.
func (m tuiModel) runMacro(mac config.Macro, rows []elementRow) (tea.Model, tea.Cmd) {
	// As a unit, before anything is dialled. Command by command would leave a
	// macro half run, with the write it was refused for sitting after
	// whatever already took effect.
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

	// Across models, once. Afterwards it runs without asking: the standing
	// signal is the model tag on the macro page, which a status line could
	// not be -- a run clears the status as it starts.
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

// macroMismatch names the first selected model the macro was not built
// against. Empty for a macro with no model recorded: that is unknown, not
// wrong. Kept short enough to fit a modal line, since that is where it is
// read.
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

// macroWarnPrompt asks once. Command syntax differs by model and version, and
// a sequence written for one appliance can fail halfway through another.
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

// onMacroWarnKey answers it. Yes acknowledges for good, as the warning says:
// the alternative is a modal between the user and every cross-model run.
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

// macroCommandLabel numbers the command lines from one, which is the order
// they run in.
func macroCommandLabel(i int) string { return fmt.Sprintf("command %d", i+1) }

// macroPlaceholder puts an example on the first line only. On the ones below
// it, a placeholder reads like a command that is already there.
func macroPlaceholder(i int) string {
	if i == 0 {
		return "dump device status"
	}
	return ""
}

func newMacroCommand(i int) formField {
	return newField(macroCommandLabel(i), macroPlaceholder(i), false)
}

// openMacroForm is one field per command rather than one comma-separated
// line: a command is not a list item, and a sequence read down the page is
// the thing being built.
func (m tuiModel) openMacroForm(mac config.Macro) (tea.Model, tea.Cmd) {
	name := newField("name", "wan check", false)
	name.input.SetValue(mac.Name)
	fields := []formField{name}
	for i, c := range mac.Commands {
		f := newMacroCommand(i)
		f.input.SetValue(c)
		fields = append(fields, f)
	}

	// Always an empty line to type into, which is what the ghost row under
	// the form is pointing at.
	fields = append(fields, newMacroCommand(len(mac.Commands)))

	m.formMacroName = mac.Name
	title := "New macro"
	if mac.Name != "" {
		title = "Edit " + mac.Name
	}
	return m.openForm(formMacro, title, phaseAddUser, fields)
}

// addMacroCommand grows the form by a line and moves to it.
func (m tuiModel) addMacroCommand() (tea.Model, tea.Cmd) {
	m.form[m.formCursor].input.Blur()
	m.form = append(m.form, newMacroCommand(len(m.form)-1))
	m.formCursor = len(m.form) - 1
	return m, m.form[m.formCursor].input.Focus()
}

// removeMacroCommand takes an empty command line out and lands on the one
// above it, the way an editor does. The form is the whole editor, so without
// this a line in the middle could only be removed by retyping everything
// after it.
func (m tuiModel) removeMacroCommand() (tea.Model, tea.Cmd) {
	m.form[m.formCursor].input.Blur()
	m.form = append(m.form[:m.formCursor], m.form[m.formCursor+1:]...)
	// The labels are the run order, so they renumber.
	for i := 1; i < len(m.form); i++ {
		m.form[i].label = macroCommandLabel(i - 1)
		m.form[i].input.Placeholder = macroPlaceholder(i - 1)
	}

	m.formCursor = max(1, min(m.formCursor, len(m.form)-1))
	in := &m.form[m.formCursor].input
	in.SetCursor(len(in.Value()))
	return m, in.Focus()
}

// macroFormCommands is the command lines, in order, with the empty ones
// dropped: a line left blank in the middle is a line not filled in, not a
// command.
func (m tuiModel) macroFormCommands() []string {
	var out []string
	for i := 1; i < len(m.form); i++ {
		if v := m.formValue(i); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// submitMacro saves the form. The model is the selection's, when the whole
// selection is one: that is what the sequence was written against, and it is
// what the cross-model warning compares.
func (m tuiModel) submitMacro() (tea.Model, tea.Cmd) {
	name := m.formValue(0)
	mac := config.Macro{Name: name, Commands: m.macroFormCommands(), Model: m.selectionModel()}

	// Renaming replaces rather than leaving the old name behind.
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

// selectionModel is the model of the selection when it is all one, and empty
// when it is mixed or empty: a macro that claims a model it was not built for
// would warn about the wrong thing.
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

	// The line the cursor is on is highlighted whole, the way a row of the
	// element list is, and built plain to do it: a highlight wrapped around
	// styled text ends at the first reset inside it, which is why a marker
	// was all this page used to manage.
	width := max(20, m.width-2)
	line := func(here bool, plain, styled string) string {
		if here {
			return ui.StyleRow.Render(fmt.Sprintf("%-*s", width, ui.Truncate(plain, width))) + "\n"
		}
		return "  " + styled + "\n"
	}

	// An action rather than a row, so it stays at the top whatever the filter
	// says and is where the cursor rests when there is nothing to run.
	const newRow = "+ new macro"
	b.WriteString(line(m.macroCursor == 0, newRow, ui.StyleGhost.Render(newRow)))

	list := m.macroRows()
	if len(m.cmds.Macros) > 0 && len(list) == 0 {
		b.WriteString(ui.StyleDim.Render("  nothing matches the filter") + "\n")
	}

	for i, mac := range list {
		// Padded plain, then styled, so the counts line up down the page.
		name := fmt.Sprintf("%-22s", ui.Truncate(mac.Name, 22))
		count := fmt.Sprintf("%-14s", fmt.Sprintf("%d command(s)", len(mac.Commands)))
		cmds := ui.Truncate(strings.Join(mac.Commands, " · "), max(10, m.width-58))

		// The model is the standing cross-model signal: warn-coloured when it
		// is not what the selection is, since the question is only asked once.
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
		"↑↓ move · enter pick, then choose elements · e edit · ctrl+d delete · "+
			copyKeyHint+" · / filter · esc menu"))
	return b.String()
}

// modeLabel names the mode on the element list. Macro mode carries the macro
// with it: the mode means nothing without saying which one is armed.
func (m tuiModel) modeLabel() string {
	label := ui.StyleElement.Render(m.mode.String() + " mode")
	if m.mode == modeMacro && m.armedMacro != "" {
		label += ui.StyleCmd.Render("  " + m.armedMacro)
	}
	return label
}

// pasteMacroCommands fills a line per pasted command, growing the form as it
// goes, and leaves an empty line at the bottom for the next one.
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

// listReturn is where esc, q and alt+w go from the element list: back to the
// macro page when a macro is what brought us here, since that is where the
// choice was made and where another one is made. The main menu otherwise.
func (m tuiModel) listReturn() tuiPhase {
	if m.mode == modeMacro && m.armedMacro != "" {
		return phaseMacros
	}
	return phaseMenu
}
