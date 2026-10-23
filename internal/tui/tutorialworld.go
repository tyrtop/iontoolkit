package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

type realWorld struct {
	held bool

	fixture *fixture.Set
	store   *config.Store
	cfg     *config.Config
	cmds    *config.CommandStore
	client  *api.Client
	pool    *clientPool

	tenant    config.Tenant
	hasTenant bool
	ionUser   string
	flags     app.Flags
	authMode  authMode

	all        []elementRow
	sites      map[string]string
	selected   map[string]bool
	cacheState cacheState
	cacheCount int
	cacheAt    time.Time

	data       []dataTab
	dataActive int
	resumeData []sessionTarget
}

func (m tuiModel) testingBlocked() string {
	if m.sessions.Count() > 0 || m.sessions.DetachedCount() > 0 {
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
	if path := FixturesPath(m.store); path != "" {
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
		store:      m.store,
		cfg:        m.cfg,
		cmds:       m.cmds,
		client:     m.client,
		pool:       m.pool,
		tenant:     m.tenant,
		hasTenant:  m.hasTenant,
		ionUser:    m.ionUser,
		flags:      m.flags,
		authMode:   m.authMode,
		all:        m.all,
		sites:      m.sites,
		selected:   m.selected,
		cacheState: m.cacheState,
		cacheCount: m.cacheCount,
		cacheAt:    m.cacheAt,
		data:       m.data,
		dataActive: m.dataActive,
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
	m.store, m.cfg = st, cfg
	m.cmds, m.cmdsErr = &config.CommandStore{}, ""
	if cmds, err := st.Commands(); err == nil {
		m.cmds = cmds
	} else {
		m.cmdsErr = err.Error()
	}
	m.authMode = authTenant
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	m.ionUser = m.tenant.DefaultUser()
	m.flags = app.TenantFlags(st, m.tenant, m.ionUser)
	m.client, m.pool = nil, newClientPool()
	m.sites, m.selected, m.lastCleared = nil, map[string]bool{}, nil
	m.cacheState, m.cacheCount, m.cacheAt = cacheNone, 0, time.Time{}
	m.data, m.dataActive, m.resumeData = nil, 0, nil
	m.setElements(nil)

	logging.L().Info("testing world entered", "tenant", m.tenant.Name, "tsg", m.tenant.TSGID)
	next, cmd := m.refreshActive()
	return next.(tuiModel), cmd
}

func (m tuiModel) leaveTestingWorld() tuiModel {
	if !m.real.held {
		return m
	}

	m.sessions.CloseAll()

	held := m.real
	app.Fixture = held.fixture
	m.real = realWorld{}
	m.store, m.cfg, m.cmds = held.store, held.cfg, held.cmds
	m.client, m.pool = held.client, held.pool
	m.tenant, m.hasTenant, m.ionUser = held.tenant, held.hasTenant, held.ionUser
	m.flags, m.authMode = held.flags, held.authMode
	m.sites, m.selected, m.lastCleared = held.sites, held.selected, nil
	m.cacheState, m.cacheCount, m.cacheAt = held.cacheState, held.cacheCount, held.cacheAt
	m.data, m.dataActive, m.resumeData = held.data, held.dataActive, held.resumeData
	m.ha, m.haCluster, m.haQueue, m.haInFlight = nil, nil, nil, nil
	m.haFailed, m.haErr, m.haTotal, m.haDone, m.haBulk = 0, "", 0, 0, ""
	m.setElements(held.all)

	logging.L().Info("testing world left", "tenant", m.tenant.Name)
	return m
}

func (m tuiModel) testingLine() string {
	switch {
	case m.real.held:
		return ui.StyleWarn.Render("on") +
			ui.StyleDim.Render(fmt.Sprintf("  %s, %d elements", m.tenant.Name, len(m.all)))
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
		next.status = "back on " + next.tenant.Name
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
	if len(m.cfg.Tenants) == 0 {
		m.status = "the fake fleet has no tenants"
		return m
	}
	m.authMode = authTenant
	m.tenant, m.hasTenant = m.cfg.Tenants[0], true
	m.ionUser = m.tenant.DefaultUser()
	m.flags = app.TenantFlags(m.store, m.tenant, m.ionUser)
	m.phase, m.cacheState = phaseMenu, cacheUpdating
	return m
}
