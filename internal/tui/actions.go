package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const (
	scrAny       = ""
	scrMenu      = "menu"
	scrElements  = "elements"
	scrData      = "data"
	scrSession   = "session"
	scrResults   = "results"
	scrMacros    = "macros"
	scrWorkflows = "workflows"
)

func screens() []string {
	return []string{scrAny, scrMenu, scrElements, scrData, scrSession, scrResults, scrMacros, scrWorkflows}
}

type builtinAction struct {
	name   string
	screen string
	run    func(m tuiModel) (tea.Model, tea.Cmd)
}

func builtinActions() []builtinAction {
	out := []builtinAction{
		{name: "edit-yaml", screen: scrAny, run: tuiModel.openEdit},
		{name: "stats", screen: scrAny, run: tuiModel.toggleStats},
		{name: "command-line", screen: scrMenu, run: tuiModel.openCmdline},
		{name: "command-line", screen: scrElements, run: tuiModel.openCmdline},
		{name: "command-line", screen: scrData, run: tuiModel.openCmdline},

		{name: "elements", screen: scrMenu, run: tuiModel.menuElements},
		{name: "service-accounts", screen: scrMenu, run: tuiModel.menuServiceAccounts},
		{name: "sign-in", screen: scrMenu, run: tuiModel.menuSignIn},
		{name: "refresh-token", screen: scrMenu, run: tuiModel.startTokenRefresh},
		{name: "macros", screen: scrMenu, run: tuiModel.openMacros},
		{name: "workflows", screen: scrMenu, run: tuiModel.openWorkflows},
		{name: "settings", screen: scrMenu, run: tuiModel.menuSettings},
		{name: "quit", screen: scrMenu, run: tuiModel.menuQuit},
		{name: "quit-closing-sessions", screen: scrMenu, run: tuiModel.menuQuitClosing},
		{name: "auth-mode", screen: scrMenu, run: tuiModel.cycleAuthMode},
		{name: "reopen-tab", screen: scrMenu, run: tuiModel.reopenFromMenu},

		{name: "filter", screen: scrElements, run: tuiModel.openFilter},
		{name: "show-selected", screen: scrElements, run: tuiModel.toggleShowSelected},
		{name: "clear-selection", screen: scrElements, run: tuiModel.clearSelection},
		{name: "undo-clear", screen: scrElements, run: tuiModel.undoClear},
		{name: "select-everything", screen: scrElements, run: tuiModel.tickEveryMatch},
		{name: "refresh", screen: scrElements, run: tuiModel.refreshNow},
		{name: "cycle-mode", screen: scrElements, run: tuiModel.cycleMode},
		{name: "ion-users", screen: scrElements, run: tuiModel.openIONUsers},
		{name: "jump-up", screen: scrElements, run: tuiModel.jumpUp},
		{name: "jump-down", screen: scrElements, run: tuiModel.jumpDown},
		{name: "reopen-tab", screen: scrElements, run: tuiModel.reopenLastTab},
		{name: "back", screen: scrElements, run: tuiModel.listBack},
		{name: "to-sessions", screen: scrElements, run: tuiModel.toSessions},
		{name: "next-tab", screen: scrElements, run: tuiModel.nextTab},
		{name: "prev-tab", screen: scrElements, run: tuiModel.prevTab},

		{name: "back", screen: scrData, run: tuiModel.dataBack},
		{name: "filter", screen: scrData, run: tuiModel.openDataFilter},
		{name: "refresh", screen: scrData, run: tuiModel.refreshData},
		{name: "compare", screen: scrData, run: tuiModel.startCompare},
		{name: "edit-record", screen: scrData, run: tuiModel.startWrite},
		{name: "export", screen: scrData, run: tuiModel.exportDataPage},
		{name: "next-tab", screen: scrData, run: tuiModel.nextTab},
		{name: "prev-tab", screen: scrData, run: tuiModel.prevTab},
		{name: "close-tab", screen: scrData, run: tuiModel.closeTab},

		{name: "back", screen: scrSession, run: tuiModel.sessionBack},
		{name: "reopen-tab", screen: scrSession, run: tuiModel.reopenLastTab},
		{name: "favourites", screen: scrSession, run: tuiModel.openFavourites},
		{name: "export", screen: scrSession, run: onActiveSession(tuiModel.exportSession)},
		{name: "mouse", screen: scrSession, run: onActiveSession(tuiModel.toggleMouse)},
		{name: "redial", screen: scrSession, run: onActiveSession(tuiModel.redialIfDead)},
		{name: "next-tab", screen: scrSession, run: tuiModel.nextTab},
		{name: "prev-tab", screen: scrSession, run: tuiModel.prevTab},
		{name: "detach-tab", screen: scrSession, run: tuiModel.detachTab},
		{name: "close-tab", screen: scrSession, run: tuiModel.closeTab},
	}
	out = append(out,
		builtinAction{name: "back", screen: scrResults, run: tuiModel.resultsBack},
		builtinAction{name: "open", screen: scrResults, run: tuiModel.toggleResult},
		builtinAction{name: "filter", screen: scrResults, run: tuiModel.openResultsFilter},
		builtinAction{name: "export", screen: scrResults, run: tuiModel.exportResults},
		builtinAction{name: "next-format", screen: scrResults, run: func(m tuiModel) (tea.Model, tea.Cmd) { return m.onResultsOutputKey("f") }},
		builtinAction{name: "next-orient", screen: scrResults, run: func(m tuiModel) (tea.Model, tea.Cmd) { return m.onResultsOutputKey("o") }},
		builtinAction{name: "save", screen: scrResults, run: func(m tuiModel) (tea.Model, tea.Cmd) { return m.onResultsOutputKey("s") }},
	)
	out = append(out,
		builtinAction{name: "back", screen: scrMacros, run: tuiModel.macrosBack},
		builtinAction{name: "filter", screen: scrMacros, run: tuiModel.openMacroFilter},
		builtinAction{name: "edit", screen: scrMacros, run: tuiModel.editMacro},
		builtinAction{name: "undo", screen: scrMacros, run: tuiModel.undoMacroDelete},
		builtinAction{name: "open", screen: scrMacros, run: tuiModel.pickMacro},

		builtinAction{name: "back", screen: scrWorkflows, run: tuiModel.workflowsBack},
		builtinAction{name: "filter", screen: scrWorkflows, run: tuiModel.openWorkflowFilter},
		builtinAction{name: "edit", screen: scrWorkflows, run: tuiModel.openEdit},
		builtinAction{name: "open", screen: scrWorkflows, run: tuiModel.pickWorkflow},
	)
	for n := 1; n <= 9; n++ {
		out = append(out, builtinAction{name: fmt.Sprintf("favourite-%d", n), screen: scrSession,
			run: func(m tuiModel) (tea.Model, tea.Cmd) { return m.useFavourite(n) }})
	}
	return out
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
	case phaseSession:
		return scrSession
	case phaseResults:
		return scrResults
	case phaseMacros:
		return scrMacros
	case phaseWorkflows:
		if m.wf.step == wfList {
			return scrWorkflows
		}
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
		m.runtime().Disarm()
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
	if err := m.runtime().Run("auth.mode", nil); err != nil {
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
