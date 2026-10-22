package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
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
}

func (m tuiModel) tutorialBlocked() string {
	if m.sessions.Count() > 0 || m.sessions.DetachedCount() > 0 {
		return "close the open sessions first — the tutorial swaps in fake elements"
	}
	if m.phase == phaseRunning {
		return "wait for the run to finish — the tutorial swaps in fake elements"
	}
	return ""
}

func (m tuiModel) enterTutorialWorld() (tuiModel, tea.Cmd) {
	set, err := fixture.Tutorial()
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
	m.setElements(nil)

	logging.L().Info("tutorial world entered", "tenant", m.tenant.Name, "tsg", m.tenant.TSGID)
	next, cmd := m.refreshActive()
	return next.(tuiModel), cmd
}

func (m tuiModel) leaveTutorialWorld() tuiModel {
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
	m.ha, m.haCluster, m.haQueue, m.haInFlight = nil, nil, nil, nil
	m.haFailed, m.haErr, m.haTotal, m.haDone, m.haBulk = 0, "", 0, 0, ""
	m.setElements(held.all)

	logging.L().Info("tutorial world left", "tenant", m.tenant.Name)
	return m
}
