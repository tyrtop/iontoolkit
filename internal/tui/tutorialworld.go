package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"tyrtop.com/iontk/internal/runtime"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/ui"
)

type realWorld struct {
	held bool

	fixture *fixture.Set

	flags app.Flags

	rt *runtime.Runtime

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

func FixturesPath(st *config.Store) string {
	if Fixtures != "" {
		return Fixtures
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, fixture.FileName)
}

var Fixtures string

func (m tuiModel) fakeFleet() (*fixture.Set, error) {
	if path := FixturesPath(m.store()); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return fixture.Parse(b, filepath.Base(path))
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return fixture.Tutorial()
}

func (m tuiModel) enterTestingWorld() (tuiModel, tea.Cmd) {
	set, err := m.fakeFleet()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}

	held := realWorld{
		held:       true,
		fixture:    app.Fixture,
		flags:      m.flags,
		rt:         m.runtime(),
		resumeData: m.resumeData,
	}

	app.Fixture = set
	st, err := app.Store()
	if err != nil {
		app.Fixture = held.fixture
		m.status = err.Error()
		return m, nil
	}
	cfg, err := st.Config()
	if err != nil {
		app.Fixture = held.fixture
		m.status = err.Error()
		return m, nil
	}

	m.real = held
	m.rt = app.NewRuntime(st, cfg)
	m.rt.SetViews(held.rt.Views(), held.rt.ViewsErr())
	m.rt.SetColumns(held.rt.Columns())
	m.setAuthMode(authTenant)
	m.runtime().UseTenant(cfg.Tenants[0])
	m.runtime().SetIONUser(m.tenant().DefaultUser())
	m.flags = app.TenantFlags(st, m.tenant(), m.ionUser())

	m.runtime().ForgetSelection()
	m.runtime().ResetFleet()
	m.resumeData = nil
	m.setElements(nil)

	logging.L().Info("testing world entered", "tenant", m.tenant().Name, "tsg", m.tenant().TSGID)
	next, cmd := m.refreshActive()
	return next.(tuiModel), cmd
}

func (m tuiModel) leaveTestingWorld() tuiModel {
	if !m.real.held {
		return m
	}

	m.sessions().CloseAll()

	held := m.real
	app.Fixture = held.fixture
	m.real = realWorld{}

	m.flags = held.flags
	m.rt = held.rt
	m.resumeData = held.resumeData
	m.runtime().ResetHALive()
	m.setElements(held.rt.Elements())

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
	m.flags = app.TenantFlags(m.store(), m.tenant(), m.ionUser())
	m.phase = phaseMenu
	m.setCacheState(cacheUpdating)
	return m
}
