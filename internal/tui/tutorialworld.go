package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/ui"
)

type realWorld struct {
	held    bool
	testing *app.Testing

	resumeData []sessionTarget
}

func (m tuiModel) testingBlocked() string {
	if m.sessions().Count() > 0 || m.sessions().DetachedCount() > 0 {
		return "close the open sessions first — testing mode swaps in fake elements"
	}
	if m.phase == phaseRunning {
		return "wait for the run to finish — testing mode swaps in fake elements"
	}
	return ""
}

func (m tuiModel) enterTestingWorld() (tuiModel, tea.Cmd) {
	tw, err := app.EnterTesting(m.runtime())
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.real = realWorld{held: true, testing: tw, resumeData: m.resumeData}
	m.rt = tw.Runtime
	m.resumeData = nil
	m.listChanged()

	logging.L().Info("testing world entered", "tenant", m.tenant().Name, "tsg", m.tenant().TSGID)
	next, cmd := m.refreshActive()
	return next.(tuiModel), cmd
}

func (m tuiModel) leaveTestingWorld() tuiModel {
	if !m.real.held {
		return m
	}
	held := m.real
	m.real = realWorld{}
	m.rt = held.testing.Leave()
	m.resumeData = held.resumeData
	m.listChanged()

	logging.L().Info("testing world left", "tenant", m.tenant().Name)
	return m
}

func (m tuiModel) testingLine() string {
	switch {
	case m.real.held:
		return ui.StyleWarn.Render("on") +
			ui.StyleDim.Render(fmt.Sprintf("  %s, %d elements", m.tenant().Name, len(m.rows())))
	case app.LaunchedFake != "":
		return ui.StyleWarn.Render("on") +
			ui.StyleDim.Render("  this whole run, from "+app.LaunchedFake)
	}
	return ui.StyleDim.Render("off")
}

func (m tuiModel) onTestingKey(key string) (tea.Model, tea.Cmd) {
	if key != "enter" {
		return m, nil
	}
	if m.tut.on {
		m.status = "the tutorial is running in the fake fleet already"
		return m, nil
	}
	if !m.real.held && app.LaunchedFake != "" {
		m.status = "this run was launched on a fake fleet"
		return m, nil
	}
	if m.real.held {
		next := m.leaveTestingWorld()
		next.status = "back on " + next.tenant().Name
		return next, nil
	}
	if why := m.testingBlocked(); why != "" {
		m.status = why
		return m, nil
	}
	next, cmd := m.enterTestingWorld()
	next.status = "testing mode: nothing here reaches the network"
	return next, cmd
}

var Demo bool

func (m tuiModel) openDemo() tuiModel {
	m.askTutorial = false
	if len(m.cfg().Tenants) == 0 {
		m.status = "the fake fleet has no tenants"
		return m
	}
	m.setAuthMode(authTenant)
	m.runtime().UseTenant(m.cfg().Tenants[0])
	m.runtime().SetIONUser(m.tenant().DefaultUser())
	m.phase = phaseMenu
	m.runtime().PeekCache(m.tenant().TSGID)
	return m
}
