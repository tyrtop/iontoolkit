package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/search"
	"tyrtop.com/iontk/internal/ui"
	"tyrtop.com/iontk/internal/view"
)

type tuiPhase int

const (
	phaseMenu tuiPhase = iota
	phaseSwitch
	phaseAddTenant
	phaseSettings
	phaseAddUser
	phasePassword
	phaseLoading
	phasePicking
	phaseCommand
	phaseRunning
	phaseResults
	phaseSession
	phaseUsers
	phaseSignIn
	phaseSignedIn
	phaseDiscover
	phaseTenantPick
	phaseWarming
	phaseData
	phaseMacros
	phaseZone
	phaseEdit
)

type pickMode int

const (
	modeShell pickMode = iota
	modeBatch
	modeData
	modeMacro
)

var pickModes = []pickMode{modeShell, modeBatch, modeData}

func (p pickMode) String() string {
	switch p {
	case modeBatch:
		return "batch"
	case modeData:
		return "rich data"
	case modeMacro:
		return "macro"
	}
	return "shell"
}

func (p pickMode) next() pickMode {
	for i, m := range pickModes {
		if m == p {
			return pickModes[(i+1)%len(pickModes)]
		}
	}
	return modeShell
}

func parseMode(s string) (pickMode, bool) {
	for _, m := range pickModes {
		if m.String() == s {
			return m, true
		}
	}
	return modeShell, false
}

type cacheState int

const (
	cacheNone cacheState = iota
	cacheUpdating
	cacheUpdated
	cacheCached
	cacheFailed
)

type formKind int

const (
	formNone formKind = iota
	formTenant
	formUser
	formPassword
	formEditPass
	formBatchDir
	formEditTenant
	formEditUser
	formGeometry
	formFavourite
	formMacro
	formColor
	formNewTheme
)

const cacheTTL = time.Hour

type elementsErrMsg string
type clientReadyMsg struct{ client *api.Client }
type clientErrMsg string

type needsSignInMsg struct{}

type discoveredMsg struct {
	d   saseauth.Discovery
	err error
}

type tokenErrMsg struct {
	err     error
	stopped bool
}

type signedInMsg struct {
	client  *api.Client
	ident   saseauth.Identity
	profile api.Profile
	ttl     time.Duration
	err     error
}

type formField struct {
	label string
	input textinput.Model
}

type tuiModel struct {
	ctx   context.Context
	store *config.Store
	cfg   *config.Config

	tenant    config.Tenant
	hasTenant bool
	ionUser   string

	client *api.Client
	flags  app.Flags

	phase  tuiPhase
	status string

	spinner spinner.Model

	cacheState cacheState
	cacheAt    time.Time
	cacheCount int

	menuCursor      int
	switchCursor    int
	setCursor       int
	tokenDead       string
	authMode        authMode
	browserAuthDone bool
	warming         int
	tenantIssues    map[string]string
	runAs           map[string]string
	usersTSG        string
	scope           string
	tenantCount     int
	showSelected    bool
	lastCleared     map[string]bool
	lastClearedView bool
	connectWait     int
	resumeRun       []string
	discovered      []saseauth.Tenant
	picked          map[string]bool
	pickCursor      int

	signInResume   bool
	resumeSessions []sessionTarget
	lastTargets    []sessionTarget

	ident    saseauth.Identity
	profile  api.Profile
	tokenTTL time.Duration

	form           []formField
	formCursor     int
	formKind       formKind
	formTitle      string
	formReturn     tuiPhase
	formTenantName string
	formTenantTSG  string
	formUserName   string
	formAuth       string
	formOldTSG     string
	formGeoRow     int

	setSection    int
	setFocus      int
	themeEdit     bool
	themeRow      int
	themeDraft    ui.Theme
	themeStart    ui.Theme
	themeUndo     []ui.Theme
	themeDel      string
	macroDel      string
	macroUndo     []config.Macro
	switchDel     string
	switchUndo    []config.Tenant
	tabEdit       bool
	tabRow        int
	pick          colorPick
	spinTick      int
	formThemeRole string
	typedZone     string
	resetArmed    bool

	all        []elementRow
	sites      map[string]string
	ha         map[string]string
	haCluster  map[string]string
	haFailed   int
	haErr      string
	haTotal    int
	haDone     int
	haAt       time.Time
	haQueue    map[string][]string
	haInFlight map[string]bool
	haCached   map[string]string
	haBulk     string

	matches  []int
	selected map[string]bool

	pool           *clientPool
	pendingTenants int
	table          table.Model
	filter         textinput.Model
	filterOn       bool
	listCols       []elementColumn
	listSpec       []elementColumn
	listFrom       string
	columnsErr     string
	fetched        map[string]map[string]fetchedCell
	fetchQueue     map[string][]string
	fetchOut       map[string]bool
	fetchDone      map[string]int
	fetchTotal     map[string]int
	fetchErr       map[string]string
	fetchBulk      map[string]string
	listSpecs      []column.Spec
	listPaths      map[string]string
	fleet          map[string]map[string]map[string]any
	fleetOut       map[string]bool
	fleetErr       map[string]string
	listWidths     []int
	listCells      []rowCells
	listCache      map[string]rowCells
	rowCtx         rowContext

	cmdInput textinput.Model
	cmdPick  suggest

	zoneInput  textinput.Model
	zonePick   suggest
	zoneReturn tuiPhase

	events    <-chan sase.Event
	ran       []string
	results   []sase.Result
	finished  int
	failed    int
	running   []inflight
	errGroups []errGroup
	progress  progress.Model

	resFilter   textinput.Model
	resFilterOn bool
	resMatches  []int
	resTable    table.Model
	resOpen     bool
	resScroll   int
	resLine     int
	elapsed     []time.Duration

	sessions    *ionterm.Manager
	sesScroll   int
	connecting  string
	dialing     map[string]dialState
	dialReports chan dialProgressMsg
	redialing   string
	askRetry    sessionTarget
	askRetryNo  bool

	stats        bool
	statsTicking bool

	cmds    *config.CommandStore
	cmdsErr string

	mode         pickMode
	views        *dataview.Set
	viewsErr     string
	viewFiles    []view.File
	keys         keymap
	keysErr      string
	scripts      *script.Set
	scriptsErr   string
	scriptOn     string
	menuSpec     []menuEntry
	menuFrom     string
	menuErr      string
	data         []dataTab
	dataActive   int
	dataFilter   textinput.Model
	dataFilterOn bool
	resumeData   []sessionTarget

	userCursor  int
	slash       slashMode
	slashInput  textinput.Model
	slashItems  []string
	slashCursor int
	freq        []config.CommandUse

	favPick   bool
	favCursor int

	macroCursor   int
	macroFilter   textinput.Model
	macroFilterOn bool
	formMacroName string
	armedMacro    string
	askMacro      string
	askMacroOn    []elementRow

	mouseOff        bool
	tut             tutorialRun
	flows           []tutorialFlow
	flowsErr        string
	edit            yamlEdit
	real            realWorld
	askTutorial     bool
	askTutorialNote bool

	width      int
	height     int
	termHeight int
}

var Version = "dev"

var Tenant string

func Run(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("iontk needs a terminal; use --cmd for batch mode")
	}

	store, err := app.Store()
	if err != nil {
		return err
	}
	cfg, err := store.Config()
	if err != nil {
		return err
	}
	if Tenant != "" {
		if _, ok := app.ResolveTenant(cfg, Tenant); !ok {
			return fmt.Errorf("no saved tenant named %q; %s", Tenant, app.TenantChoices(cfg))
		}
	}

	logging.L().Info("tui started", "version", Version, "tenant", Tenant, "config", store.Root)

	model := newTUIModel(ctx, store, cfg).raiseTutorialAsk()
	if Demo {
		model = model.openDemo()
	}
	defer model.sessions.CloseAll()
	// bubbletea's own reset is OSC 111, which not every terminal implements
	defer restoreTermColor(os.Stdout)

	_, err = tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

func newTUIModel(ctx context.Context, store *config.Store, cfg *config.Config) tuiModel {
	app.ApplyDisplaySettings(store, cfg)

	s := ui.NewSpinner(ui.CurrentTheme().Spinner)

	fi := textinput.New()
	fi.Prompt = "/"
	fi.Placeholder = "filter"
	fi.ShowSuggestions = true
	fi.SetWidth(26)
	fi.KeyMap.NextSuggestion = key.NewBinding(key.WithKeys("ctrl+n"))
	fi.KeyMap.PrevSuggestion = key.NewBinding(key.WithKeys("ctrl+p"))

	ci := textinput.New()
	ci.Prompt = "> "
	ci.Placeholder = "dump interface status 1, dump device status"
	ci.SetWidth(60)

	m := tuiModel{
		ctx:     ctx,
		store:   store,
		cfg:     cfg,
		phase:   phaseMenu,
		spinner: s,

		selected:    map[string]bool{},
		filter:      fi,
		cmdInput:    ci,
		table:       newElementTable(),
		dialing:     map[string]dialState{},
		dialReports: newDialReports(),
		progress:    progress.New(progress.WithDefaultBlend(), progress.WithWidth(30), progress.WithoutPercentage()),
		resFilter:   newResultsFilter(),
		resTable:    newResultsTable(),
		sessions:    ionterm.NewManager(),
		pool:        newClientPool(),
		width:       80,
		height:      24,
	}
	if mode, ok := parseMode(cfg.DefaultMode); ok {
		m.mode = mode
	}
	if z := cfg.Zone; z != "" && !strings.EqualFold(z, clock.UTC) && !strings.EqualFold(z, clock.Local) {
		m.typedZone = z
	}

	m.loadRuntime(store)

	m.dataFilter = newDataFilter()
	m.cmds = &config.CommandStore{}
	if store != nil {
		cmds, err := store.Commands()
		if err != nil {
			m.cmdsErr = err.Error()
		}
		m.cmds = cmds
	}
	if am, ok := parseAuthMode(cfg.AuthMode); ok {
		m.authMode = am
	} else if t, ok := app.ResolveTenant(cfg, Tenant); ok && !t.UsesBrowser() {
		m.authMode = authTenant
	}
	m.tenant, m.hasTenant = app.ResolveTenant(cfg, Tenant)
	if m.hasTenant {
		m.ionUser = m.tenant.DefaultUser()
		m.cacheState = cacheUpdating
		if c, err := store.Cache(m.tenant.TSGID); err == nil && c != nil && len(c.Elements) > 0 {
			m.cacheCount = len(c.Elements)
			m.cacheAt = time.Now().Add(-c.Age())
			if c.Age() < cacheFresh {
				m.cacheState = cacheCached
			}
		}
	}
	return m.readFlows()
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Selected = ui.StyleRow
	return s
}

func newElementTable() table.Model {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "", Width: 3},
			{Title: "", Width: 3},
			{Title: "Element", Width: 26},
			{Title: "Site", Width: 22},
			{Title: "Model", Width: 11},
			{Title: "State", Width: 9},
		}),
		table.WithFocused(true),
	)
	t.SetStyles(tableStyles())
	return t
}

// table.MoveUp raises the viewport offset while the cursor falls, so the
// highlight walks off the top and a held k stops appearing to do anything.
func (m *tuiModel) moveCursorTo(n int) {
	rows := m.table.Rows()
	if len(rows) == 0 {
		return
	}
	n = max(0, min(n, len(rows)-1))
	prev := m.table.Cursor()
	if n >= prev {
		m.table.MoveDown(n - prev)
		m.repaintCursorRows(prev)
		return
	}

	fresh := newElementTable()
	fresh.SetColumns(m.table.Columns())
	fresh.SetRows(rows)
	fresh.SetWidth(m.table.Width())
	fresh.SetHeight(m.listHeight())
	fresh.MoveDown(n)
	m.table = fresh
	m.repaintCursorRows(prev)
}

func (m *tuiModel) moveCursor(delta int) {
	m.moveCursorTo(m.table.Cursor() + delta)
}

const cacheFresh = config.CacheFresh

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if m.cacheState == cacheUpdating && m.hasTenant {
		f := app.TenantFlags(m.store, m.tenant, m.ionUser)
		if f.ValidateCreds() == nil {
			cmds = append(cmds, connect(m.ctx, m.store, f))
		} else {
			cmds = cmds[:1]
		}
	}
	cmds = append(cmds, probeTermColor())
	return tea.Batch(cmds...)
}

func (m tuiModel) refreshNow() (tea.Model, tea.Cmd) {
	if !m.hasTenant {
		m.status = "no tenant selected"
		return m, nil
	}
	f := app.TenantFlags(m.store, m.tenant, m.ionUser)
	if err := f.ValidateCreds(); err != nil {
		m.status = err.Error()
		m.cacheState = cacheFailed
		return m, nil
	}
	m.flags = f
	m.status = ""
	m.haFailed, m.haErr = 0, ""
	m.haTotal, m.haDone, m.haBulk = 0, 0, ""
	return m.refreshActive()
}

func connect(ctx context.Context, st *config.Store, f app.Flags) tea.Cmd {
	return func() tea.Msg {
		c, err := app.NewClient(ctx, st, f, io.Discard)
		if err != nil {
			if errors.Is(err, saseauth.ErrInteractiveRequired) {
				return needsSignInMsg{}
			}
			return clientErrMsg(err.Error())
		}
		return clientReadyMsg{client: c}
	}
}

func signIn(ctx context.Context, f app.Flags, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		httpClient := &http.Client{Timeout: f.HTTPTimeout}
		src := app.BrowserSource(f, httpClient, st)
		if err := src.SignIn(ctx); err != nil {
			return signedInMsg{err: err}
		}
		ident := src.Identity()
		if !ident.ScopedTo(f.TSGID) {
			return signedInMsg{err: fmt.Errorf("signed in, but the token is scoped to %q rather than tenant %s", ident.Scope, f.TSGID)}
		}

		c, err := app.NewClient(ctx, st, f, io.Discard)
		if err != nil {
			return signedInMsg{err: fmt.Errorf("signed in, but the saved credential did not work: %w", err)}
		}
		prof, err := c.Profile(ctx)
		if err != nil {
			return signedInMsg{err: fmt.Errorf("signed in, but the API rejected the token: %w", err)}
		}
		return signedInMsg{client: c, ident: ident, profile: prof, ttl: src.LastTTL()}
	}
}

func watchToken(ch <-chan error) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		err, ok := <-ch
		if !ok {
			return tokenErrMsg{stopped: true}
		}
		return tokenErrMsg{err: err}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)

	mm, ok := next.(tuiModel)
	if !ok || !mm.tut.on || !mm.tut.passing {
		return next, cmd
	}
	mm.tut.passing = false
	mm, passed := mm.passStep()
	return mm, tea.Batch(cmd, passed)
}

func (m tuiModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.ColorProfileMsg:
		ui.SetProfile(msg.Profile)
		if ui.ShouldQueryDepth() {
			return m, tea.Batch(tea.RequestCapability("RGB"), tea.RequestCapability("Tc"))
		}
		return m, nil

	case tea.BackgroundColorMsg:
		rememberTermColor(msg.Color, nil)
		return m, nil

	case tea.ForegroundColorMsg:
		rememberTermColor(nil, msg.Color)
		return m, nil

	case colorProbeDoneMsg:
		termColor.probed = true
		return m, nil

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.onWheel(true)
		case tea.MouseWheelDown:
			return m.onWheel(false)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.termHeight = msg.Width, msg.Height
		m.height = m.usableHeight()
		m.resFilter.SetWidth(min(26, max(10, m.width-30)))
		m.rebuildResultRows()
		m.filter.SetWidth(min(26, max(10, m.width-30)))
		m.cmdInput.SetWidth(min(60, max(20, m.width-10)))
		for i := range m.form {
			m.form[i].input.SetWidth(min(48, max(16, m.width-24)))
		}
		m.rebuildRows()
		m.fitEdit()
		return m, nil

	case clientErrMsg:
		m.status = string(msg)
		m.cacheState = cacheFailed
		m.phase = phaseMenu
		return m, nil

	case tokenErrMsg:
		if msg.stopped {
			if m.tokenDead == "" {
				m.tokenDead = "token expired — sign in again"
			}
			return m, nil
		}
		if errors.Is(msg.err, saseauth.ErrInteractiveRequired) {
			m.tokenDead = "sign-in expired"
			m.status = m.tokenDead + " — the next action will reopen your browser"
			return m, nil
		}
		m.status = msg.err.Error()
		if c := m.pool.get(m.tenant.TSGID); c != nil {
			return m, watchToken(c.TokenErrors())
		}
		return m, nil

	case needsSignInMsg:
		if m.phase == phaseMenu {
			m.status = "sign-in required — pick the browser sign-in below"
			m.cacheState = cacheFailed
			return m, nil
		}
		m.signInResume = true
		return m.startSignInAuto()

	case tenantsWarmedMsg:
		return m.onTenantsWarmed(msg)

	case discoveredMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.phase = phaseMenu
			return m, nil
		}
		m.ident = msg.d.Identity
		m.browserAuthDone = true
		if msg.d.RefreshToken != "" {
			if err := m.store.Secrets.Set(config.AccountRefreshTokenKey(), msg.d.RefreshToken); err != nil {
				m.status = err.Error()
			}
		}

		known := make([]config.KnownTenant, 0, len(msg.d.Tenants))
		for _, t := range msg.d.Tenants {
			known = append(known, config.KnownTenant{TSGID: t.ID, Name: t.Name})
		}
		if err := m.store.SaveTenantCache(&config.TenantCache{
			FetchedAt: time.Now(),
			Account:   msg.d.Identity.Display(),
			Tenants:   known,
		}); err != nil {
			m.status = err.Error()
		}

		m.discovered = msg.d.Tenants
		next, cmd := m.startTenantPick()
		return next, cmd

	case tokenRefreshedMsg:
		if msg.err != nil {
			if errors.Is(msg.err, saseauth.ErrInteractiveRequired) {
				m.tokenDead = "sign-in expired"
				m.signInResume = false
				return m.startSignInAuto()
			}
			m.status = "token refresh failed: " + msg.err.Error()
			return m, nil
		}
		m.ident = msg.ident
		m.tokenTTL = msg.ttl
		m.tokenDead = ""
		m.status = "token refreshed"
		m.pool.drop(m.tenant.TSGID)
		m.client = nil
		return m, nil

	case signedInMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.cacheState = cacheFailed
			m.signInResume = false
			m.phase = phaseMenu
			return m, nil
		}
		m.client = msg.client
		m.pool.set(m.tenant.TSGID, msg.client)
		m.browserAuthDone = true
		m.status = ""
		m.tokenDead = ""
		watch := watchToken(msg.client.TokenErrors())

		if targets := m.resumeSessions; len(targets) > 0 {
			m.resumeSessions = nil
			next, cmd := m.openSessions(targets)
			return next, tea.Batch(watch, cmd)
		}
		if targets := m.resumeData; len(targets) > 0 {
			m.resumeData = nil
			next, cmd := m.openDataTabs(targets)
			return next, tea.Batch(watch, cmd)
		}
		if m.signInResume {
			m.signInResume = false
			next, cmd := m.startContinue()
			return next, tea.Batch(watch, cmd)
		}

		m.ident = msg.ident
		m.profile = msg.profile
		m.tokenTTL = msg.ttl
		m.phase = phaseSignedIn
		next, cmd := m.refreshActive()
		return next, tea.Batch(m.spinner.Tick, watch, cmd)

	case clientReadyMsg:
		m.client = msg.client
		m.pool.set(m.tenant.TSGID, msg.client)
		watch := watchToken(msg.client.TokenErrors())
		if m.cacheState == cacheCached {
			if m.phase == phaseLoading {
				m.phase = phasePicking
			}
			return m, watch
		}
		next, cmd := m.refreshActive()
		return next, tea.Batch(watch, cmd)

	case elementsErrMsg:
		if app.IsAuthFailure(string(msg)) {
			m.tokenDead = "sign-in expired"
			m.signInResume = true
			return m.startSignInAuto()
		}
		m.status = string(msg)
		m.cacheState = cacheFailed
		if len(m.all) == 0 {
			m.phase = phaseMenu
		}
		return m, nil

	case tenantConnectedMsg:
		if m.connectWait > 0 {
			m.connectWait--
		}
		var watch tea.Cmd
		if msg.err != nil {
			m.tenantErr(msg.tsg, m.tenantName(msg.tsg), msg.err)
		} else {
			m.pool.set(msg.tsg, msg.client)
			if msg.fresh {
				watch = watchToken(msg.client.TokenErrors())
			}
		}
		if m.connectWait > 0 {
			return m, watch
		}
		if cmds := m.resumeRun; len(cmds) > 0 {
			m.resumeRun = nil
			next, cmd := m.startRun(cmds)
			return next, tea.Batch(watch, cmd)
		}
		if targets := m.resumeSessions; len(targets) > 0 {
			m.resumeSessions = nil
			next, cmd := m.openSessions(targets)
			return next, tea.Batch(watch, cmd)
		}
		if targets := m.resumeData; len(targets) > 0 {
			m.resumeData = nil
			next, cmd := m.openDataTabs(targets)
			return next, tea.Batch(watch, cmd)
		}
		return m, watch

	case tenantElementsMsg:
		var ha, cells tea.Cmd
		if m.pendingTenants > 0 {
			m.pendingTenants--
		}
		if msg.err != nil {
			m.tenantErr(msg.tsg, msg.tenant, msg.err)
		} else {
			delete(m.tenantIssues, msg.tsg)
			if msg.siteErr != nil && m.status == "" {
				m.status = msg.tenant + ": site names unavailable: " + shortTenantErr(msg.siteErr)
			}
			if m.sites == nil {
				m.sites = map[string]string{}
			}
			for k, v := range msg.sites {
				m.sites[k] = v
			}
			m.mergeTenantElements(msg.tsg, msg.tenant, msg.elements)
			if w := haClusters(msg.elements); len(w.elements) > 0 {
				m.haTotal += w.clusters()
				m.markHAOut(msg.tsg)
				ha = loadHABulk(m.ctx, m.pool.get(msg.tsg), msg.tsg, w)
			}
			cells = tea.Batch(m.startCells(), m.readFleets())
		}

		if m.pendingTenants == 0 {
			m.cacheCount = len(m.all)
			m.cacheAt = time.Now()
			if len(m.all) > 0 {
				m.cacheState = cacheUpdated
			} else {
				m.cacheState = cacheFailed
			}
		}
		if m.phase == phaseLoading && len(m.all) > 0 {
			m.phase = phasePicking
		}
		return m, tea.Batch(ha, cells)

	case spinner.TickMsg:
		if !m.wantsSpinner() {
			return m, nil
		}
		m.spinTick++
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd

	case dialProgressMsg:
		m.noteDial(msg)
		m.rebuildRows()
		return m, tea.Batch(waitForDial(m.dialReports), m.spinner.Tick)

	case sessionOpenedMsg:
		m = m.onSessionOpened(string(msg))
		return m, waitForSession(m.sessions.Dirty())

	case sessionErrMsg:
		m.connecting = ""
		if app.IsAuthFailure(msg.err) {
			m.dialDone(msg.target.eid)
			m.tokenDead = "sign-in expired"
			return m.signInThenSessions(m.lastTargets)
		}
		m.dialFailed(msg.target.eid, msg.err)
		m.status = msg.err
		m.rebuildRows()
		if msg.target.eid != "" {
			m.askRetry = msg.target
			return m, nil
		}
		if m.sessions.Count() == 0 && m.phase == phaseSession {
			m.phase = phasePicking
		}
		return m, nil

	case sessionDirtyMsg:
		if n := m.sessions.Reap(); n > 0 && m.phase == phaseSession && m.sessions.Count() == 0 {
			m.status = ""
			m.phase = phasePicking
		}
		return m, waitForSession(m.sessions.Dirty())

	case eventMsg:
		return m.onEvent(sase.Event(msg))

	case streamClosedMsg:
		m.phase = phaseResults
		m.running = nil
		m.resOpen, m.resScroll = false, 0
		m.resTable.SetCursor(0)
		m.applyResultFilter()
		if note := m.saveBatchResults(); note != "" {
			m.status = note
		}
		return m, nil

	case statsTickMsg:
		if !m.stats {
			m.statsTicking = false
			return m, nil
		}
		return m, statsTick()

	case dataLoadedMsg:
		return m.onDataLoaded(msg)

	case compareDoneMsg:
		return m.onCompareDone(msg)

	case commandsSavedMsg:
		return m.onCommandsSaved(msg)

	case haStatesMsg:
		return m.onHAStates(msg)

	case haTickMsg:
		return m.onHATick(msg.tsg)

	case scriptDoneMsg:
		return m.onScriptDone(msg)

	case fetchedMsg:
		return m.onFetchedCells(msg)

	case fleetReadMsg:
		return m.onFleetRead(msg)

	case fetchTickMsg:
		return m.onFetchTick(msg)

	case tea.KeyPressMsg:
		return m.onKey(msg)

	default:
		return m.forwardToInput(msg)
	}
}

func (m tuiModel) forwardToInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseAddTenant, phaseAddUser, phasePassword:
		if len(m.form) == 0 || m.formCursor >= len(m.form) {
			return m, nil
		}
		if p, ok := msg.(tea.PasteMsg); ok && m.formKind == formMacro && m.formCursor >= 1 {
			if cmds := pasteCommands(p.Content); len(cmds) > 1 {
				return m.pasteMacroCommands(cmds)
			}
		}
		var cmd tea.Cmd
		m.form[m.formCursor].input, cmd = m.form[m.formCursor].input.Update(msg)
		return m.syncMacroSuggestions(), cmd

	case phasePicking:
		if m.filterOn {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return m, tea.Batch(cmd, m.askVisibleHA(), m.askVisibleCells())
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	case phaseCommand:
		if p, ok := msg.(tea.PasteMsg); ok {
			if cmds := pasteCommands(p.Content); len(cmds) > 1 {
				msg = tea.PasteMsg{Content: strings.Join(cmds, ", ")}
			}
		}
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
		return m.refilterCommands(lastSegment(m.cmdInput.Value())), cmd

	case phaseZone:
		var cmd tea.Cmd
		m.zoneInput, cmd = m.zoneInput.Update(msg)
		m.zonePick = m.zonePick.refilter(clock.Zones(), m.zoneInput.Value())
		return m, cmd

	case phaseMacros:
		if !m.macroFilterOn {
			return m, nil
		}
		var cmd tea.Cmd
		m.macroFilter, cmd = m.macroFilter.Update(msg)
		return m, cmd

	case phaseResults:
		if m.resFilterOn {
			var cmd tea.Cmd
			m.resFilter, cmd = m.resFilter.Update(msg)
			m.applyResultFilter()
			return m, cmd
		}
		return m, nil

	case phaseSession:
		if p, ok := msg.(tea.PasteMsg); ok {
			if sess := m.sessions.Active(); sess != nil && sess.Err() == nil {
				sess.SendText(p.Content)
			}
		}
		return m, nil

	case phaseEdit:
		var cmd tea.Cmd
		m.edit.area, cmd = m.edit.area.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) wantsSpinner() bool {
	switch m.phase {
	case phaseLoading, phaseRunning, phaseSettings, phaseSignIn, phaseDiscover, phaseWarming:
		return true
	case phasePicking:
		return m.cacheState == cacheUpdating || m.fetchWorking()
	case phaseSession:
		return m.connectingAny() || m.awaitingDevice()
	case phaseEdit:
		return false
	case phaseData:
		for _, t := range m.data {
			if t.loading() || t.cmp.loading {
				return true
			}
		}
	}
	return false
}

func (m *tuiModel) setElements(els []elementRow) {
	m.all = els
	m.computeScope()
	sort.Slice(m.all, func(i, j int) bool {
		if m.all[i].Name != m.all[j].Name {
			return m.all[i].Name < m.all[j].Name
		}
		return m.all[i].TSG < m.all[j].TSG
	})

	names := make([]string, 0, len(m.all))
	for _, el := range m.all {
		if el.Name != "" {
			names = append(names, el.Name)
		}
	}
	m.filter.SetSuggestions(names)
	m.forgetRows()
	m.applyFilter()
}

func (m *tuiModel) siteName(el elementRow) string {
	return m.record(el).SiteLabel()
}

func (m *tuiModel) record(el elementRow) search.Record {
	return search.Of(el.Element, el.TSG, el.Tenant, search.SiteMap(m.sites))
}

func (m *tuiModel) showTenantColumn() bool { return m.tenantCount > 1 }

func (m *tuiModel) showHAColumn() bool {
	for _, r := range m.all {
		if r.ClusterID() != "" {
			return true
		}
	}
	return false
}

func (m *tuiModel) showSerialColumn() bool {
	for _, r := range m.all {
		if r.Serial != "" {
			return true
		}
	}
	return false
}

func (m *tuiModel) rebuildRows() {
	m.rowCtx = rowContext{}
	cols := m.columns()
	if !sameColumns(cols, m.listCols) {
		m.listCols, m.listCache = cols, nil
	}
	if m.listCache == nil {
		m.listCache = make(map[string]rowCells, len(m.all))
	}

	cells := make([]rowCells, 0, len(m.matches))
	widths := make([][]int, 0, len(m.matches))
	for _, i := range m.matches {
		rc := m.rowCells(cols, m.all[i])
		cells = append(cells, rc)
		widths = append(widths, rc.width)
	}

	m.listCells = cells
	m.listWidths = fitColumns(cols, widths, max(20, m.width))

	rows := make([]table.Row, len(cells))
	lo, hi := m.paintWindow(len(cells))
	for n := range cells {
		if n >= lo && n < hi {
			rows[n] = m.listRow(n)
		} else {
			rows[n] = m.listRowPlain(n)
		}
	}

	tcols := make([]table.Column, 0, len(cols))
	total := 0
	for i, c := range cols {
		tcols = append(tcols, table.Column{Title: c.title, Width: m.listWidths[i]})
		total += m.listWidths[i] + colGap
	}

	if len(m.table.Columns()) != len(tcols) {
		at := m.table.Cursor()
		m.table.SetRows(nil)
		m.table.SetColumns(tcols)
		m.table.SetRows(rows)
		if at >= 0 && at < len(rows) {
			m.table.SetCursor(at)
		}
	} else {
		m.table.SetColumns(tcols)
		m.table.SetRows(rows)
	}
	if len(rows) > 0 && m.table.Cursor() < 0 {
		m.table.SetCursor(0)
	}
	m.table.SetWidth(min(m.width, total))
	m.table.SetHeight(m.listHeight())
}

func (m *tuiModel) listRow(n int) table.Row {
	rc := m.listCells[n]
	el := m.all[m.matches[n]]
	here := n == m.table.Cursor()

	row := func(s string) string { return s }
	if el.Connected && !here {
		row = func(s string) string { return ui.StyleOK.Render(s) }
	}
	ticked := m.selected[el.key()]

	out := make(table.Row, len(m.listCols))
	for i, c := range m.listCols {
		cl := cell{
			el:     el,
			text:   truncCell(rc.text[i], rc.width[i], m.listWidths[i]),
			here:   here,
			ticked: ticked,
			row:    row,
		}
		if c.paint != nil {
			out[i] = c.paint(m, cl)
			continue
		}
		out[i] = row(cl.text)
	}
	return out
}

func (m *tuiModel) listRowPlain(n int) table.Row {
	rc := m.listCells[n]
	row := make(table.Row, len(rc.text))
	for i := range rc.text {
		row[i] = truncCell(rc.text[i], rc.width[i], m.listWidths[i])
	}
	return row
}

func (m *tuiModel) paintWindow(n int) (int, int) {
	return m.windowAround(m.table.Cursor(), n)
}

func (m *tuiModel) windowAround(at, n int) (int, int) {
	h := max(8, m.listHeight()) + 4
	return max(0, at-h), min(n, at+h)
}

func (m *tuiModel) repaintCursorRows(prev int) {
	rows := m.table.Rows()
	if len(rows) != len(m.listCells) {
		return
	}
	lo, hi := m.paintWindow(len(rows))
	plo, phi := m.windowAround(prev, len(rows))

	repaint := func(i int) {
		if i < 0 || i >= len(rows) {
			return
		}
		if i >= lo && i < hi {
			rows[i] = m.listRow(i)
			return
		}
		rows[i] = m.listRowPlain(i)
	}
	repaint(prev)
	repaint(m.table.Cursor())
	for i := min(lo, plo); i < max(lo, plo); i++ {
		repaint(i)
	}
	for i := min(hi, phi); i < max(hi, phi); i++ {
		repaint(i)
	}
	m.table.SetRows(rows)
}

func (m tuiModel) onEvent(ev sase.Event) (tea.Model, tea.Cmd) {
	if !ev.Done {
		m.running = append(m.running, inflight{element: ev.Element, started: time.Now()})
		return m, waitForEvent(m.events)
	}

	m.results[ev.Index] = ev.Result
	if ev.Index < len(m.elapsed) {
		m.elapsed[ev.Index] = ev.Elapsed
	}
	m.finished++
	m.applyResultFilter()
	for i, f := range m.running {
		if f.element == ev.Element {
			m.running = append(m.running[:i], m.running[i+1:]...)
			break
		}
	}
	if ev.Result.Error != "" {
		m.failed++
		m.recordTUIError(ev.Element, ev.Result.Error)
	}

	return m, tea.Batch(
		m.progress.SetPercent(float64(m.finished)/float64(m.total())),
		waitForEvent(m.events),
	)
}

func (m *tuiModel) recordTUIError(element, msg string) {
	key := normalizeErr(msg)
	for i := range m.errGroups {
		if m.errGroups[i].msg == key {
			m.errGroups[i].count++
			return
		}
	}
	m.errGroups = append(m.errGroups, errGroup{msg: key, count: 1, first: element})
}

func (m tuiModel) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" && m.phase != phaseSession {
		return m, tea.Quit
	}

	if m.askTutorial || m.askTutorialNote {
		return m.onTutorialAskKey(key)
	}

	if m.askMacro != "" {
		return m.onMacroWarnKey(key)
	}

	if m.askRetry.eid != "" {
		return m.onRetryKey(key)
	}

	if m.tut.on {
		next, cmd, handled := m.onTutorialKey(key)
		if handled {
			return next, cmd
		}
		m = next
	}

	if m.phase == phaseEdit {
		return m.onEditKey(msg, key)
	}

	if next, cmd, ran := m.runGlobalBinding(key); ran {
		return next, cmd
	}

	switch m.phase {
	case phaseSession:
		return m.onSessionKey(msg, key)
	case phaseData:
		if m.dataFilterOn {
			if next, cmd, done := m.onDataFilterKey(msg, key); done {
				return next, cmd
			}
		}
		return m.onDataKey(key)
	case phaseMacros:
		return m.onMacroKey(msg, key)
	case phaseZone:
		return m.onZoneKey(msg, key)
	case phaseMenu:
		return m.onMenuKey(key)
	case phaseSwitch:
		return m.onSwitchKey(key)
	case phaseSettings:
		return m.onSettingsKey(key)
	case phaseUsers:
		return m.onUsersKey(key)
	case phaseAddTenant, phaseAddUser, phasePassword:
		return m.onFormKey(msg, key)

	case phaseTenantPick:
		return m.onTenantPickKey(key)
	case phaseWarming:
		return m, nil
	case phaseDiscover:
		if key == "esc" || key == "q" {
			m.status = "cancelled"
			m.phase = phaseMenu
		}
		return m, nil
	case phaseSignIn:
		if key == "esc" || key == "q" {
			m.signInResume = false
			m.status = "sign-in cancelled"
			m.phase = phaseMenu
		}
		return m, nil
	case phaseSignedIn:
		switch key {
		case "enter", "esc", "q":
			if m.signInResume {
				m.signInResume = false
				return m.startContinue()
			}
			m.phase = phaseMenu
		}
		return m, nil
	case phaseLoading:
		if key == "esc" || key == "q" {
			m.phase = phaseMenu
		}
		return m, nil
	case phasePicking:
		return m.onPickKey(msg, key)
	case phaseCommand:
		return m.onCommandKey(msg, key)
	case phaseRunning:
		switch key {
		case "esc", "q", "tab", "/":
			if m.completedCount() > 0 {
				m.phase = phaseResults
				m.applyResultFilter()
			}
			return m, nil
		}
		return m, nil
	case phaseResults:
		return m.onResultsKey(msg, key)
	}
	return m, nil
}

func (m tuiModel) onPickKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if next, cmd, ran := m.bindingOn(scrElements, key); ran {
		return next, cmd
	}

	switch key {
	case "esc", "q":
		if key == "q" && m.filterOn {
			break
		}
		if m.filterOn {
			m.filterOn = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.applyFilter()
			return m, nil
		}
		m.phase = m.listReturn()
		return m, nil

	case "up", "down":
		step := 1
		if key == "up" {
			step = -1
		}
		m.moveCursor(step)
		return m, nil
	case "pgup", "pgdown":
		step := max(1, m.table.Height()-1)
		if key == "pgup" {
			step = -step
		}
		m.moveCursor(step)
		return m, nil

	case "k", "j", "home", "end":
		if !m.filterOn {
			switch key {
			case "k":
				m.moveCursor(-1)
			case "j":
				m.moveCursor(1)
			case "home":
				m.moveCursorTo(0)
			case "end":
				m.moveCursorTo(len(m.table.Rows()) - 1)
			}
			return m, nil
		}

	case "space":
		if !m.filterOn {
			return m.toggleCurrent(), nil
		}

	case "enter":
		if m.filterOn {
			m.filterOn = false
			m.filter.Blur()

			if len(m.matches) == 1 {
				el := m.all[m.matches[0]]
				if m.mode == modeBatch || m.mode == modeMacro {
					m.selected[el.key()] = true
					m.rebuildRows()
					return m, nil
				}
				m.table.SetCursor(0)
				return m.onPickTargets([]sessionTarget{
					{eid: el.ID, name: el.label(), tsg: el.TSG},
				})
			}

			return m, nil
		}
		return m.onPickEnter()
	}

	if m.filterOn {
		if key == "backspace" && m.filter.Value() == "" {
			m.filterOn = false
			m.filter.Blur()
			m.applyFilter()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, tea.Batch(cmd, m.askVisibleHA(), m.askVisibleCells())
	}

	if key == "alt+w" {
		m.status = ""
		m.phase = m.listReturn()
		return m, nil
	}
	if next, handled := m.onTabKey(key); handled {
		return next, nil
	}

	switch key {
	case "ctrl+]":
		if m.sessions.Count() > 0 {
			m.phase = phaseSession
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m tuiModel) pickTargets() []sessionTarget {
	var out []sessionTarget
	for _, el := range m.all {
		if !m.selected[el.key()] {
			continue
		}
		out = append(out, sessionTarget{eid: el.ID, name: el.label(), tsg: el.TSG})
	}
	if len(out) > 0 {
		return out
	}
	if el, ok := m.current(); ok {
		return []sessionTarget{{eid: el.ID, name: el.label(), tsg: el.TSG}}
	}
	return nil
}

func (m tuiModel) onPickEnter() (tea.Model, tea.Cmd) {
	if m.mode == modeBatch && len(m.selected) == 0 {
		return m.toggleCurrent(), nil
	}
	return m.onPickTargets(m.pickTargets())
}

func (m tuiModel) onPickTargets(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}

	for _, want := range targets {
		delete(m.tenantIssues, want.tsg)
	}

	if m.mode == modeData {
		return m.openDataTabs(targets)
	}

	for _, want := range targets {
		t, ok := m.tenantOf(want.tsg)
		if !ok {
			t = config.Tenant{Name: want.tsg, TSGID: want.tsg, Auth: config.AuthBrowser}
			for _, r := range m.all {
				if r.TSG == want.tsg {
					t.Name = r.Tenant
					break
				}
			}
		}
		if app.NeedsIONPassword(m.store, t, m.ionUserFor(t)) {
			return m.openPasswordForm(t, m.ionUserFor(t))
		}
	}

	if m.mode == modeShell {
		return m.openSessions(targets)
	}
	if m.mode == modeMacro {
		return m.runArmedMacro(m.rowsFor(targets))
	}

	m.status = ""
	m.phase = phaseCommand
	m.cmdPick = newSuggest(m.catalog(), lastSegment(m.cmdInput.Value()))
	return m, m.cmdInput.Focus()
}

func (m tuiModel) onCommandKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.cmdInput.Blur()
		m.phase = phasePicking
		return m, nil
	case "ctrl+n", "down":
		m.cmdPick = m.cmdPick.move(1)
		return m, nil
	case "ctrl+p", "up":
		m.cmdPick = m.cmdPick.move(-1)
		return m, nil
	case "tab":
		if pick, ok := m.cmdPick.current(); ok {
			return m.insertBatchCommand(pick), nil
		}
		if n := len(m.tenant.Users); n > 1 {
			for i, u := range m.tenant.Users {
				if u.Name == m.ionUser {
					m.ionUser = m.tenant.Users[(i+1)%n].Name
					return m, nil
				}
			}
			m.ionUser = m.tenant.Users[0].Name
		}
		return m, nil
	case "enter":
		cmds := app.SplitCommands([]string{m.cmdInput.Value()})
		if len(cmds) == 0 {
			m.status = "enter at least one command"
			return m, nil
		}
		return m.startRun(cmds)
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	m = m.refilterCommands(lastSegment(m.cmdInput.Value()))
	return m, cmd
}

func (m tuiModel) rowsFor(targets []sessionTarget) []elementRow {
	out := make([]elementRow, 0, len(targets))
	for _, t := range targets {
		if el, ok := m.elementByKey(t.tsg + "/" + t.eid); ok {
			out = append(out, el)
		}
	}
	return out
}

func (m tuiModel) selectedRows() ([]elementRow, []string) {
	var picked []elementRow
	var ids []string
	for _, el := range m.all {
		if m.selected[el.key()] {
			picked = append(picked, el)
			ids = append(ids, el.ID)
		}
	}
	return picked, ids
}

func (m tuiModel) startRun(cmds []string) (tea.Model, tea.Cmd) {
	picked, ids := m.selectedRows()
	return m.startRunOn(cmds, picked, ids)
}

func (m tuiModel) startRunOn(cmds []string, picked []elementRow, ids []string) (tea.Model, tea.Cmd) {
	if !app.Defaults.AllowWrites && !m.cfg.AllowWrites {
		if err := ioncli.CheckReadOnly(cmds); err != nil {
			m.status = err.Error()
			return m, nil
		}
	}

	targets := m.targetsFor(picked)
	for i, t := range targets {
		if t.User == "" || t.Pass == "" {
			m.status = "no ION login for " + picked[i].Tenant
			return m, nil
		}
	}

	if conn := m.ensureClients(tsgsOfRows(picked)); len(conn) > 0 {
		m.resumeRun = cmds
		m.connectWait = len(conn)
		m.status = "connecting tenants"
		return m, tea.Batch(append(conn, m.spinner.Tick)...)
	}

	f := app.TenantFlags(m.store, m.tenant, m.ionUser)
	f.Elements = ids
	f.Commands = cmds
	if f.IONUsername == "" || f.IONPassword == "" {
		f.IONUsername, f.IONPassword = targets[0].User, targets[0].Pass
	}
	if err := f.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	opts := f.Options()
	if err := opts.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.cmdInput.Blur()
	m.status = ""
	m.flags = f
	m.ran = ids
	save := m.recordRun(cmds, picked)
	m.results = make([]sase.Result, len(ids))
	m.elapsed = make([]time.Duration, len(ids))
	m.resMatches = nil
	m.resOpen, m.resScroll = false, 0
	m.resFilter.SetValue("")
	m.resFilterOn = false
	m.finished, m.failed = 0, 0
	m.running = nil
	m.errGroups = nil
	m.phase = phaseRunning
	m.events = sase.RunStream(m.ctx, opts, targets)

	return m, tea.Batch(waitForEvent(m.events), m.spinner.Tick, save)
}

func (m *tuiModel) applyFilter() {
	q := search.Parse(m.filter.Value())
	m.matches = m.matches[:0]
	for i, el := range m.all {
		if m.showSelected && !m.selected[el.key()] {
			continue
		}
		if q.Match(m.record(el)) {
			m.matches = append(m.matches, i)
		}
	}
	m.rebuildRows()
	if c := m.table.Cursor(); c >= len(m.matches) {
		m.table.SetCursor(max(0, len(m.matches)-1))
	}
}

func (m tuiModel) current() (elementRow, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.matches) {
		return elementRow{}, false
	}
	return m.all[m.matches[c]], true
}

func (m tuiModel) total() int { return len(m.ran) }

func (m tuiModel) listHeight() int {
	h := m.height - 13 - m.statsRows()
	if m.sessions.Count() > 0 {
		h--
	}
	return max(3, h)
}

func (m tuiModel) View() tea.View {
	var v tea.View
	switch m.phase {
	case phaseMenu:
		v = tea.NewView(m.viewMenu())
	case phaseSwitch:
		v = tea.NewView(m.viewSwitch())
	case phaseSettings:
		v = tea.NewView(m.viewSettings())
	case phaseUsers:
		v = tea.NewView(m.viewUsers())
	case phaseAddTenant, phaseAddUser, phasePassword:
		v = tea.NewView(m.viewForm())

	case phaseDiscover:
		v = tea.NewView(m.viewDiscover())
	case phaseTenantPick:
		v = tea.NewView(m.viewTenantPick())
	case phaseWarming:
		v = tea.NewView(m.viewWarming())
	case phaseSignIn:
		v = tea.NewView(m.viewSignIn())
	case phaseSignedIn:
		v = tea.NewView(m.viewSignedIn())
	case phaseLoading:
		v = tea.NewView(ui.Banner(m.width, m.height) +
			fmt.Sprintf("  %s loading elements\n", m.spinner.View()))
	case phasePicking:
		v = tea.NewView(m.viewPicking())
	case phaseCommand:
		v = tea.NewView(m.viewCommand())
	case phaseZone:
		v = tea.NewView(m.viewZonePick())
	case phaseRunning:
		v = tea.NewView(m.viewRunning())
	case phaseResults:
		v = tea.NewView(m.viewResults())
	case phaseSession:
		content, cur := m.renderSession()
		v = tea.NewView(content)
		v.Cursor = cur
	case phaseData:
		v = tea.NewView(m.viewData())
	case phaseMacros:
		v = tea.NewView(m.viewMacros())
	case phaseEdit:
		v = tea.NewView(m.viewEdit())
	}
	if m.askRetry.eid != "" {
		v = tea.NewView(m.overlayRetry(v.Content))
	}
	if m.tut.on {
		v = tea.NewView(m.overlayTutorial(v.Content))
	}
	if m.askMacro != "" {
		v = tea.NewView(m.overlayMacroWarn(v.Content))
	}
	switch {
	case m.askTutorial:
		v = tea.NewView(overlayUnder(v.Content, m.tutorialAsk(), m.termHeight))
	case m.askTutorialNote:
		v = tea.NewView(overlayUnder(v.Content, m.tutorialNote(), m.termHeight))
	}
	v.Content = ui.ClipFrame(v.Content, m.termHeight)
	v.AltScreen = true
	v.MouseMode = m.mouseMode()
	v.BackgroundColor, v.ForegroundColor = frameColors()
	return v
}

func (m tuiModel) viewPicking() string {
	var b strings.Builder

	if m.sessions.Count() > 0 {
		b.WriteString(m.tabBar() + "\n")
	}
	b.WriteString(ui.StyleCmd.Render("iontk") + "  " + m.modeLabel() +
		ui.StyleDim.Render("   "+m.scopeLabel()+" · "+m.countLabel()) +
		m.selectedNote() + "\n")
	b.WriteString(m.refreshNote() + "\n")

	if m.filterOn || m.filter.Value() != "" {
		line := m.filter.View()
		if n := len(m.matches); n > 0 && m.filter.Value() != "" {
			line += ui.StyleDim.Render(fmt.Sprintf("   %d match", n))
			if n != 1 {
				line += ui.StyleDim.Render("es")
			}
		}
		b.WriteString(line + "\n")
	} else {
		b.WriteString(ui.StyleDim.Render("press "+m.legend(scrElements, "to filter", "filter")) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(m.table.View() + "\n")

	b.WriteString("\n")
	{
		n := len(m.selected)
		if n == 0 {
			n = 1
		}
		act := fmt.Sprintf("enter open %d shell", n)
		if n != 1 {
			act += "s"
		}
		switch m.mode {
		case modeBatch:
			act = fmt.Sprintf("enter command on %d", n)
			if len(m.selected) == 0 {
				act = "enter select"
			}
		case modeData:
			act = fmt.Sprintf("enter data on %d", n)
		case modeMacro:
			act = fmt.Sprintf("enter run %s on %d", m.armedMacro, n)
		}
		clear := m.legend(scrElements, "clear", "clear-selection")
		if len(m.lastCleared) > 0 {
			clear = m.legend(scrElements, "undo clear", "undo-clear")
		}
		back := "back"
		if m.listReturn() == phaseMacros {
			back = "macros"
		}
		hint := fmt.Sprintf("↑↓/jk move · %s · space select · %s · %s · %s · %s · %s · %s · esc/q/alt+w %s",
			m.legend(scrElements, "filter", "filter"),
			m.legend(scrElements, "selected", "show-selected"),
			clear,
			m.legend(scrElements, "refresh", "refresh"),
			m.legend(scrElements, "mode", "cycle-mode"),
			act,
			m.legend(scrAny, "yaml", "edit-yaml"),
			back)
		if m.showSelected {
			hint = fmt.Sprintf("↑↓/jk move · alt+s all · alt+c clear · space unselect · %s · esc/q/alt+w %s", act, back)
		}
		if m.filterOn {
			enter := "enter apply"
			if len(m.matches) == 1 {
				enter = "enter open"
				if m.mode == modeBatch {
					enter = "enter select"
				}
			}
			hint = "↑↓ move · tab complete · " + enter + " · esc clear"
		}
		if m.stats {
			b.WriteString(m.statsPane() + "\n")
		}
		b.WriteString(ui.Footer(m.status, hint))
	}
	return b.String()
}

func (m tuiModel) viewCommand() string {
	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render("commands") +
		ui.StyleDim.Render(fmt.Sprintf("   %d elements selected", len(m.selected))) + "\n\n")

	runAs := m.ionUser
	if runAs == "" {
		runAs = ui.StyleWarn.Render("no ION user")
	}
	b.WriteString(ui.StyleDim.Render("run as  ") + ui.StyleElement.Render(runAs))
	if len(m.tenant.Users) > 1 {
		b.WriteString(ui.StyleDim.Render("   tab to change"))
	}
	b.WriteString("\n\n")

	b.WriteString(m.cmdInput.View() + "\n\n")
	b.WriteString(m.cmdSuggestBlock() + "\n")
	b.WriteString(ui.StyleDim.Render("comma separated · read-only commands only") + "\n")
	if m.status != "" {
		b.WriteString(ui.StyleErr.Render(m.status) + "\n")
	} else {
		b.WriteString(ui.StyleDim.Render("enter run · esc back") + "\n")
	}
	return b.String()
}

func (m tuiModel) viewRunning() string {
	var b strings.Builder

	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleCmd.Render(strings.Join(m.flags.Commands, ", ")) + "\n")

	counts := fmt.Sprintf("  %d/%d", m.finished, m.total())
	if m.failed > 0 {
		counts += ui.StyleErr.Render(fmt.Sprintf("  %d failed", m.failed))
	}
	b.WriteString(m.spinner.View() + " " + m.progress.View() + counts + "\n")

	avail := max(2, m.height-6-ui.BannerHeight(m.width, m.height))
	errShown := min(len(m.errGroups), max(1, avail/2))
	flightShown := min(len(m.running), max(1, avail-errShown))

	if errShown > 0 {
		b.WriteString("\n")
		for _, g := range m.errGroups[:errShown] {
			b.WriteString(fmt.Sprintf("%s %d × %s %s\n", ui.MarkCross, g.count, g.msg,
				ui.StyleDim.Render("(first: "+g.first+")")))
		}
		if n := len(m.errGroups) - errShown; n > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more error kinds\n", n)))
		}
	}

	if flightShown > 0 {
		b.WriteString("\n")
		for _, f := range m.running[:flightShown] {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				ui.StyleElement.Render(f.element), ui.StyleDim.Render(short(time.Since(f.started)))))
		}
		if n := len(m.running) - flightShown; n > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more\n", n)))
		}
	}

	return b.String()
}
