package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const (
	scrAny      = ""
	scrMenu     = "menu"
	scrElements = "elements"
	scrData     = "data"
)

func screens() []string { return []string{scrAny, scrMenu, scrElements, scrData} }

type builtinAction struct {
	name   string
	screen string
	keys   []string
	run    func(m tuiModel) (tea.Model, tea.Cmd)
}

func builtinActions() []builtinAction {
	return []builtinAction{
		{name: "edit-yaml", screen: scrAny, keys: []string{editKey}, run: tuiModel.openEdit},
		{name: "stats", screen: scrAny, keys: []string{"alt+i"}, run: tuiModel.toggleStats},
		{name: "command-line", screen: scrMenu, keys: []string{":"}, run: tuiModel.openCmdline},
		{name: "command-line", screen: scrElements, keys: []string{":"}, run: tuiModel.openCmdline},
		{name: "command-line", screen: scrData, keys: []string{":"}, run: tuiModel.openCmdline},

		{name: "elements", screen: scrMenu, run: tuiModel.menuElements},
		{name: "service-accounts", screen: scrMenu, run: tuiModel.menuServiceAccounts},
		{name: "sign-in", screen: scrMenu, run: tuiModel.menuSignIn},
		{name: "refresh-token", screen: scrMenu, run: tuiModel.startTokenRefresh},
		{name: "macros", screen: scrMenu, run: tuiModel.openMacros},
		{name: "settings", screen: scrMenu, run: tuiModel.menuSettings},
		{name: "quit", screen: scrMenu, keys: []string{"q"}, run: tuiModel.menuQuit},
		{name: "quit-closing-sessions", screen: scrMenu, keys: []string{"alt+w"}, run: tuiModel.menuQuitClosing},
		{name: "auth-mode", screen: scrMenu, keys: []string{"alt+m", "alt+a"}, run: tuiModel.cycleAuthMode},
		{name: "reopen-tab", screen: scrMenu, keys: []string{"alt+t"}, run: tuiModel.reopenFromMenu},

		{name: "filter", screen: scrElements, keys: []string{"/"}, run: tuiModel.openFilter},
		{name: "show-selected", screen: scrElements, keys: []string{"alt+s"}, run: tuiModel.toggleShowSelected},
		{name: "clear-selection", screen: scrElements, keys: []string{"alt+c"}, run: tuiModel.clearSelection},
		{name: "undo-clear", screen: scrElements, keys: []string{"alt+z", "ctrl+z"}, run: tuiModel.undoClear},
		{name: "select-everything", screen: scrElements, keys: []string{"ctrl+a"}, run: tuiModel.tickEveryMatch},
		{name: "refresh", screen: scrElements, keys: []string{"alt+r"}, run: tuiModel.refreshNow},
		{name: "cycle-mode", screen: scrElements, keys: []string{"alt+m", "alt+a"}, run: tuiModel.cycleMode},
		{name: "ion-users", screen: scrElements, keys: []string{"alt+u"}, run: tuiModel.openIONUsers},
		{name: "jump-up", screen: scrElements, keys: []string{"alt+up", "alt+k"}, run: tuiModel.jumpUp},
		{name: "jump-down", screen: scrElements, keys: []string{"alt+down", "alt+j"}, run: tuiModel.jumpDown},
		{name: "reopen-tab", screen: scrElements, keys: []string{"alt+t"}, run: tuiModel.reopenLastTab},
	}
}

func builtinAt(screen, name string) (builtinAction, bool) {
	for _, a := range builtinActions() {
		if a.name == name && (a.screen == screen || a.screen == scrAny) {
			return a, true
		}
	}
	return builtinAction{}, false
}

func actionNamesOn(screen string) []string {
	var out []string
	for _, a := range builtinActions() {
		if a.screen == screen || a.screen == scrAny {
			out = append(out, a.name)
		}
	}
	return out
}

func (m tuiModel) screen() string {
	switch m.phase {
	case phaseMenu:
		return scrMenu
	case phasePicking:
		return scrElements
	case phaseData:
		return scrData
	}
	return scrAny
}

func (m tuiModel) runNamed(screen, name string, depth int) (tea.Model, tea.Cmd, bool) {
	if depth > actionDepth {
		m.status = fmt.Sprintf("%s runs itself; stopped after %d steps", name, actionDepth)
		return m, nil, true
	}
	if steps, ok := m.binds().actions[name]; ok {
		next, cmd := m.runSteps(name, steps, depth)
		return next, cmd, true
	}
	if m.hasAction(name) {
		next, cmd := m.startScript(name)
		return next, cmd, true
	}
	if a, ok := builtinAt(screen, name); ok {
		next, cmd := a.run(m)
		return next, cmd, true
	}
	if depth > 0 {
		if a, ok := anyBuiltin(name); ok {
			next, cmd := a.run(m)
			return next, cmd, true
		}
	}
	if cmd, args, rest, err := m.runtime().Parse(name); err == nil {
		next, c := m.execCommand(cmd, args, rest)
		return next, c, true
	}
	return m, nil, false
}

const actionDepth = 8

func (m tuiModel) menuElements() (tea.Model, tea.Cmd) {
	if m.mode() == modeMacro {
		mode := modeShell
		if saved, ok := parseMode(m.cfg().DefaultMode); ok {
			mode = saved
		}
		m.setMode(mode)
	}
	return m.startContinue()
}

func (m tuiModel) menuServiceAccounts() (tea.Model, tea.Cmd) {
	m.switchCursor = 0
	m.status = ""
	m.phase = phaseSwitch
	return m, nil
}

func (m tuiModel) menuSignIn() (tea.Model, tea.Cmd) {
	m.switchCursor = 0
	m.status = ""
	if len(m.browserTenants()) > 0 {
		m.phase = phaseSwitch
		return m, nil
	}
	return m.startDiscover()
}

func (m tuiModel) menuSettings() (tea.Model, tea.Cmd) {
	m.setCursor = 0
	m.phase = phaseSettings
	return m.enteredSection(), m.spinner.Tick
}

func (m tuiModel) menuQuit() (tea.Model, tea.Cmd) { return m, tea.Quit }

func (m tuiModel) menuQuitClosing() (tea.Model, tea.Cmd) {
	m.sessions().CloseAll()
	return m, tea.Quit
}

func (m tuiModel) cycleAuthMode() (tea.Model, tea.Cmd) {
	m.setAuthMode(m.authMode().next())
	m.cfg().AuthMode = m.authMode().String()
	if err := m.store().SaveConfig(m.cfg()); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.switchCursor = 0
	m.status = ""
	return m, nil
}

func (m tuiModel) openFilter() (tea.Model, tea.Cmd) {
	if m.filter.Value() != "" {
		m.filter.SetValue("")
		m.applyFilter()
	}
	m.filterOn = true
	return m, m.filter.Focus()
}

func (m tuiModel) toggleShowSelected() (tea.Model, tea.Cmd) {
	return m.listCommand("list.show-selected", true), nil
}

func (m tuiModel) clearSelection() (tea.Model, tea.Cmd) {
	return m.listCommand("list.clear", false), nil
}

func (m tuiModel) undoClear() (tea.Model, tea.Cmd) {
	return m.listCommand("list.undo-clear", false), nil
}

func (m tuiModel) tickEveryMatch() (tea.Model, tea.Cmd) {
	_ = m.runtime().Run("list.select-matches", nil)
	m.rebuildRows()
	return m, nil
}

func (m tuiModel) listCommand(name string, toTop bool) tuiModel {
	if err := m.runtime().Run(name, nil); err != nil {
		m.status = err.Error()
		return m
	}
	m.status = m.rt.Status()
	m.applyFilter()
	if toTop {
		m.moveCursorTo(0)
	}
	return m
}

func (m tuiModel) cycleMode() (tea.Model, tea.Cmd) {
	_ = m.runtime().Run("list.mode", nil)
	m.status = ""
	return m, nil
}

func (m tuiModel) openIONUsers() (tea.Model, tea.Cmd) {
	m.userCursor = 0
	m.status = ""
	m.usersTSG = m.tenant().TSGID
	if el, ok := m.current(); ok {
		m.usersTSG = el.TSG
	}
	m.phase = phaseUsers
	return m, nil
}

func (m tuiModel) jumpUp() (tea.Model, tea.Cmd) {
	m.moveCursor(-m.pageStep())
	return m, nil
}

func (m tuiModel) jumpDown() (tea.Model, tea.Cmd) {
	m.moveCursor(m.pageStep())
	return m, nil
}

func isTextKey(key string) bool { return len([]rune(key)) == 1 && !strings.Contains(key, "+") }
