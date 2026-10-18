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
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
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
)

type pickMode int

const (
	modeShell pickMode = iota
	modeBatch
)

// pickModes is the cycle alt+m walks. Adding a mode means adding it here.
var pickModes = []pickMode{modeShell, modeBatch}

func (p pickMode) String() string {
	if p == modeBatch {
		return "batch"
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
)

const cacheTTL = time.Hour

type elementsLoadedMsg struct {
	elements []api.Element
	sites    map[string]string
}
type elementsErrMsg string
type clientReadyMsg struct{ client *api.Client }
type clientErrMsg string

// needsSignInMsg says the tenant's stored credentials are spent and only a
// browser can renew them.
type needsSignInMsg struct{}

// discoveredMsg carries the tenants a browser sign-in found.
type discoveredMsg struct {
	d   saseauth.Discovery
	err error
}

// signedInMsg carries the outcome of a browser sign-in, including the facts
// that prove it worked: a client that authenticated from the stored refresh
// token, and a profile fetched with it.
// tokenErrMsg is a refresh failure reported by the client. A closed channel
// means refreshing has stopped for good.
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

	spinner     spinner.Model
	spinnerName string

	cacheState cacheState
	cacheAt    time.Time
	cacheCount int

	menuCursor   int
	switchCursor int
	setCursor    int
	tokenDead    string
	authMode     authMode
	tenantIssues map[string]string
	// The device login chosen per tenant, keyed by tenant ID, and which tenant
	// the users panel is managing.
	runAs    map[string]string
	usersTSG string
	// Derived when the element list changes, not per render.
	scope       string
	tenantCount int
	// showSelected narrows the list to what has been picked.
	showSelected bool
	// The last selection alt+c threw away, so it can be put back.
	lastCleared     map[string]bool
	lastClearedView bool
	// Outstanding tenant connections, and the batch commands waiting on them.
	connectWait int
	resumeRun   []string
	discovered  []saseauth.Tenant
	picked      map[string]bool
	pickCursor  int

	// What to replay once an unplanned sign-in finishes. At most one is set.
	// A sign-in with something to replay skips the confirmation screen, since
	// it is plumbing in the middle of an action the user already chose.
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

	setSection int
	setFocus   int

	all      []elementRow
	sites    map[string]string
	matches  []int
	selected map[string]bool

	// One client per tenant, built on demand. A browser account can reach many
	// tenants, so there is no single "the" client any more.
	pool           *clientPool
	pendingTenants int
	table          table.Model
	filter         textinput.Model
	filterOn       bool

	cmdInput textinput.Model

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
	elapsed     []time.Duration

	sessions   *ionterm.Manager
	connecting string

	mode        pickMode
	userCursor  int
	slash       slashMode
	slashInput  textinput.Model
	slashItems  []string
	slashCursor int

	width  int
	height int
}

// Version is what the settings page reports. The command resolves it,
// because the release build sets it through main.
var Version = "dev"

func Run(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("iontk needs a terminal; use --cmd for batch mode")
	}

	store, err := config.Open()
	if err != nil {
		return err
	}
	cfg, err := store.Config()
	if err != nil {
		return err
	}

	model := newTUIModel(ctx, store, cfg)
	defer model.sessions.CloseAll()

	_, err = tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

func newTUIModel(ctx context.Context, store *config.Store, cfg *config.Config) tuiModel {
	spinnerPick := cfg.Spinner
	if spinnerPick == "" {
		spinnerPick = ui.DefaultSpinner
	}
	s := ui.NewSpinner(spinnerPick)

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
		ctx:         ctx,
		store:       store,
		cfg:         cfg,
		phase:       phaseMenu,
		spinner:     s,
		spinnerName: ui.SpinnerName(spinnerPick),
		selected:    map[string]bool{},
		filter:      fi,
		cmdInput:    ci,
		table:       newElementTable(),
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
	if am, ok := parseAuthMode(cfg.AuthMode); ok {
		m.authMode = am
	} else if t, ok := app.ResolveTenant(cfg, ""); ok && !t.UsesBrowser() {
		// Nothing saved: follow whatever the tenant already uses, so an existing
		// service account setup does not open on a browser prompt.
		m.authMode = authTenant
	}
	m.tenant, m.hasTenant = app.ResolveTenant(cfg, "")
	if m.hasTenant {
		m.ionUser = m.tenant.DefaultUser()
		// Elements are refreshed on every launch so the list keeps up with the
		// tenant; the cache only exists to reach the menu without waiting. A
		// cache younger than cacheFresh is taken as current and left alone.
		m.cacheState = cacheUpdating
		if c, err := store.Cache(m.tenant.TSGID); err == nil && c != nil && len(c.Elements) > 0 {
			m.cacheCount = len(c.Elements)
			m.cacheAt = time.Now().Add(-c.Age())
			if c.Age() < cacheFresh {
				m.cacheState = cacheCached
			}
		}
	}
	return m
}

func newElementTable() table.Model {
	return table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 3},
			{Title: "element", Width: 26},
			{Title: "site", Width: 22},
			{Title: "model", Width: 11},
			{Title: "state", Width: 9},
		}),
		table.WithFocused(true),
	)
}

// cacheFresh is how long cached elements are treated as current.
const cacheFresh = 15 * time.Minute

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if m.cacheState == cacheUpdating && m.hasTenant {
		f := app.TenantFlags(m.store, m.tenant, m.ionUser)
		if f.ValidateCreds() == nil {
			cmds = append(cmds, connect(m.ctx, f))
		} else {
			cmds = cmds[:1]
		}
	}
	return tea.Batch(cmds...)
}

// refreshNow re-fetches elements, authenticating first if that has not happened
// yet. It is what the refresh keybind and the settings entry both call.
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
	return m.refreshActive()
}

func connect(ctx context.Context, f app.Flags) tea.Cmd {
	return func() tea.Msg {
		c, err := app.NewClient(ctx, f, io.Discard)
		if err != nil {
			if errors.Is(err, saseauth.ErrInteractiveRequired) {
				return needsSignInMsg{}
			}
			return clientErrMsg(err.Error())
		}
		return clientReadyMsg{client: c}
	}
}

// signIn opens the browser and waits for the sign-in to finish. It is the only
// path in iontk that ever shows a browser window.
//
// Finishing the exchange is not the same as working, so this goes on to build a
// client the ordinary way and fetch a profile with it. That exercises the
// stored refresh token, which is what decides whether the next run needs a
// browser at all, and the profile call is the one that proves the SD-WAN API
// accepts the token.
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

		c, err := app.NewClient(ctx, f, io.Discard)
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

// watchToken surfaces refresh failures instead of letting them print into the
// frame. The api package no longer writes to stderr, which the TUI never
// redirected, so a failed refresh used to land in the middle of the display.
func watchToken(ch <-chan error) tea.Cmd {
	// A static token has no refresh loop, so there is no channel and nothing to
	// watch. Receiving from a nil channel would block this goroutine forever.
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

func refreshElements(ctx context.Context, c *api.Client, st *config.Store, tsg string) tea.Cmd {
	return func() tea.Msg {
		els, err := c.ListElements(ctx)
		if err != nil {
			return elementsErrMsg(err.Error())
		}
		sites := map[string]string{}
		if ss, err := c.ListSites(ctx); err == nil {
			for _, site := range ss {
				sites[site.ID] = site.Name
			}
		}
		_ = st.SaveCache(&config.ElementCache{
			TSGID: tsg, FetchedAt: time.Now(), Elements: els, Sites: sites,
		})
		return elementsLoadedMsg{elements: els, sites: sites}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resFilter.SetWidth(min(26, max(10, m.width-30)))
		m.rebuildResultRows()
		m.filter.SetWidth(min(26, max(10, m.width-30)))
		m.cmdInput.SetWidth(min(60, max(20, m.width-10)))
		for i := range m.form {
			m.form[i].input.SetWidth(min(48, max(16, m.width-24)))
		}
		m.rebuildRows()
		return m, nil

	case clientErrMsg:
		m.status = string(msg)
		m.cacheState = cacheFailed
		m.phase = phaseMenu
		return m, nil

	case tokenErrMsg:
		if msg.stopped {
			// Refreshing gave up. Whatever is on screen will start failing, so
			// say so rather than letting it look like the device is at fault.
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
		return m.startSignIn()

	case discoveredMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.phase = phaseMenu
			return m, nil
		}
		m.ident = msg.d.Identity
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
				// The stored credential is spent, so the quiet path cannot
				// finish; fall through to the browser.
				m.tokenDead = "sign-in expired"
				m.signInResume = false
				return m.startSignIn()
			}
			m.status = "token refresh failed: " + msg.err.Error()
			return m, nil
		}
		m.ident = msg.ident
		m.tokenTTL = msg.ttl
		m.tokenDead = ""
		m.status = "token refreshed"
		// The old client holds the previous token, so let the next action build
		// one with the new.
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
		m.status = ""
		m.tokenDead = ""
		watch := watchToken(msg.client.TokenErrors())

		// Something was interrupted to get here, so go straight back to it
		// rather than making the user acknowledge a screen first.
		if targets := m.resumeSessions; len(targets) > 0 {
			m.resumeSessions = nil
			next, cmd := m.openSessions(targets)
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
		// Elements are fetched behind the confirmation, so dismissing it lands on
		// a list that is already current.
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
			return m.startSignIn()
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
		if msg.err != nil {
			m.tenantErr(msg.tsg, m.tenantName(msg.tsg), msg.err)
		} else {
			m.pool.set(msg.tsg, msg.client)
		}
		if m.connectWait > 0 {
			return m, nil
		}
		// Everything that was waiting on a connection can go ahead now.
		if cmds := m.resumeRun; len(cmds) > 0 {
			m.resumeRun = nil
			return m.startRun(cmds)
		}
		if targets := m.resumeSessions; len(targets) > 0 {
			m.resumeSessions = nil
			return m.openSessions(targets)
		}
		return m, nil

	case tenantElementsMsg:
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
		return m, nil

	case spinner.TickMsg:
		if !m.wantsSpinner() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd

	case sessionOpenedMsg:
		m.connecting = ""
		return m, waitForSession(m.sessions.Dirty())

	case sessionErrMsg:
		m.connecting = ""
		if app.IsAuthFailure(string(msg)) {
			m.tokenDead = "sign-in expired"
			return m.signInThenSessions(m.lastTargets)
		}
		m.status = string(msg)
		return m, nil

	case sessionDirtyMsg:
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
		var cmd tea.Cmd
		m.form[m.formCursor].input, cmd = m.form[m.formCursor].input.Update(msg)
		return m, cmd

	case phasePicking:
		if m.filterOn {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return m, cmd
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	case phaseCommand:
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
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
	}
	return m, nil
}

func (m tuiModel) wantsSpinner() bool {
	switch m.phase {
	case phaseLoading, phaseRunning, phaseSettings, phaseSignIn, phaseDiscover:
		return true
	case phasePicking:
		// Only while a refresh is actually in flight, so the list is not
		// repainted several times a second for nothing.
		return m.cacheState == cacheUpdating
	case phaseSession:
		return m.connecting != ""
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
	m.applyFilter()
}

// siteName looks up a site within its own tenant, since two tenants can use the
// same site identifier for different places.
func (m tuiModel) siteName(el elementRow) string {
	if n, ok := m.sites[el.TSG+"/"+el.SiteID]; ok && n != "" {
		return n
	}
	if el.SiteID != "" {
		return el.SiteID
	}
	return "-"
}

// showTenantColumn is true once more than one tenant is loaded; with a single
// tenant the column is just noise.
func (m tuiModel) showTenantColumn() bool { return m.tenantCount > 1 }

func (m *tuiModel) rebuildRows() {
	rows := make([]table.Row, 0, len(m.matches))
	tenants := m.showTenantColumn()
	for _, i := range m.matches {
		el := m.all[i]
		mark := " "
		if m.selected[el.key()] {
			mark = "x"
		}
		name := el.label()
		state := "offline"
		if el.Connected {
			state = "connected"
		}
		row := table.Row{
			mark,
			ui.Truncate(name, 26),
			ui.Truncate(m.siteName(el), 22),
			ui.Truncate(el.Model, 11),
			state,
		}
		if tenants {
			row = append(row, ui.Truncate(el.Tenant, 20))
		}
		rows = append(rows, row)
	}

	cols := []table.Column{
		{Title: "", Width: 3},
		{Title: "element", Width: 26},
		{Title: "site", Width: 22},
		{Title: "model", Width: 11},
		{Title: "state", Width: 9},
	}
	width := 78
	if tenants {
		cols = append(cols, table.Column{Title: "tenant", Width: 20})
		width = 100
	}
	// Columns and rows have to agree at every moment: the table re-renders
	// inside each setter, so changing the column count while the old rows are
	// still in place indexes past the end of them. Clearing the rows first is
	// safe but resets the cursor, so only do it when the shape really changed.
	if len(m.table.Columns()) != len(cols) {
		cursor := m.table.Cursor()
		m.table.SetRows(nil)
		m.table.SetColumns(cols)
		m.table.SetRows(rows)
		if cursor > 0 && cursor < len(rows) {
			m.table.SetCursor(cursor)
		}
	} else {
		m.table.SetRows(rows)
	}
	m.table.SetWidth(min(m.width, width))
	m.table.SetHeight(m.listHeight())
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

	switch m.phase {
	case phaseSession:
		return m.onSessionKey(msg, key)
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
	case phaseDiscover:
		if key == "esc" || key == "q" {
			m.status = "cancelled"
			m.phase = phaseMenu
		}
		return m, nil
	case phaseSignIn:
		// The browser owns the keyboard until it is done; esc only stops waiting.
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
		case "q":
			return m, tea.Quit
		case "esc", "tab", "/":
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
	switch key {
	case "esc":
		if m.filterOn || m.filter.Value() != "" {
			m.filterOn = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.applyFilter()
			return m, nil
		}
		m.phase = phaseMenu
		return m, nil

	case "up", "down", "k", "j", "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	case "alt+c":
		if len(m.selected) == 0 {
			m.status = "nothing selected"
			return m, nil
		}
		n := len(m.selected)
		// Kept so alt+z can put it back. The map is copied rather than
		// aliased: the model is passed by value and the live selection is
		// about to be replaced.
		m.lastCleared = make(map[string]bool, n)
		for k := range m.selected {
			m.lastCleared[k] = true
		}
		m.lastClearedView = m.showSelected

		m.selected = map[string]bool{}
		// Showing only the selection with nothing selected would be an empty
		// list and no way back, so that view is dropped too.
		m.showSelected = false
		m.applyFilter()
		m.status = fmt.Sprintf("cleared %d selected — alt+z to undo", n)
		return m, nil

	case "alt+z", "ctrl+z":
		if len(m.lastCleared) == 0 {
			m.status = "nothing to undo"
			return m, nil
		}
		// Anything selected since the clear is kept: undo restores, it does
		// not replace.
		for k := range m.lastCleared {
			m.selected[k] = true
		}
		n := len(m.lastCleared)
		m.lastCleared = nil
		m.showSelected = m.lastClearedView
		m.applyFilter()
		m.status = fmt.Sprintf("restored %d selected", n)
		return m, nil

	case "alt+s":
		if !m.showSelected && len(m.selected) == 0 {
			m.status = "nothing selected"
			return m, nil
		}
		m.showSelected = !m.showSelected
		m.status = ""
		m.applyFilter()
		m.table.SetCursor(0)
		return m, nil

	case "space":
		// Filtering and selecting are separate modes: while the query is being
		// typed every space belongs to it, and selection resumes once the
		// filter is applied with enter.
		if !m.filterOn {
			return m.toggleCurrent(), nil
		}

	case "alt+r":
		return m.refreshNow()

	case "alt+u":
		m.userCursor = 0
		m.status = ""
		// Open on the tenant of whatever is highlighted, which is the one the
		// user is looking at; the browsed tenant is the fallback.
		m.usersTSG = m.tenant.TSGID
		if el, ok := m.current(); ok {
			m.usersTSG = el.TSG
		}
		m.phase = phaseUsers
		return m, nil

	case "alt+m":
		if m.mode == modeShell {
			m.mode = modeBatch
		} else {
			m.mode = modeShell
		}
		m.status = ""
		return m, nil

	case "ctrl+a":
		all := true
		for _, i := range m.matches {
			if !m.selected[m.all[i].key()] {
				all = false
				break
			}
		}
		for _, i := range m.matches {
			if all {
				delete(m.selected, m.all[i].key())
			} else {
				m.selected[m.all[i].key()] = true
			}
		}
		m.rebuildRows()
		return m, nil

	case "enter":
		if m.filterOn {
			m.filterOn = false
			m.filter.Blur()

			// A query that has narrowed to a single ION is unambiguous, so
			// enter acts on it instead of only applying the filter. In shell
			// mode that means going straight in; in batch mode it means
			// selecting it and staying, since a command still has to be chosen.
			if len(m.matches) == 1 {
				el := m.all[m.matches[0]]
				if m.mode == modeBatch {
					m.selected[el.key()] = true
					m.rebuildRows()
					return m, nil
				}
				m.table.SetCursor(0)
				return m.onPickTargets([]sessionTarget{
					{eid: el.ID, name: el.label(), tsg: el.TSG},
				})
			}

			// Otherwise the query is applied and the narrowed list selected from.
			return m, nil
		}
		return m.onPickEnter()
	}

	if m.filterOn {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	if key == "alt+t" {
		return m.reopenLastTab()
	}
	if key == "alt+w" {
		// alt+w closes what you are in. On the element list that is the list
		// itself, so it goes back to the menu; only the menu's alt+w quits.
		// Any sessions keep running and are still reachable with alt+t.
		m.status = ""
		m.phase = phaseMenu
		return m, nil
	}
	if next, handled := m.onTabKey(key); handled {
		return next, nil
	}

	switch key {
	case "/":
		m.filterOn = true
		return m, m.filter.Focus()
	case "ctrl+]":
		if m.sessions.Count() > 0 {
			m.phase = phaseSession
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// pickTargets is what enter acts on: everything selected, or the highlighted
// row when nothing is.
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
	return m.onPickTargets(m.pickTargets())
}

// onPickTargets acts on an explicit set of elements, running the per-tenant
// credential and connection checks first. Separate from onPickEnter so a search
// that narrows to a single ION can act on that one rather than on whatever
// happens to be selected.
func (m tuiModel) onPickTargets(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}

	// An explicit action is a fresh attempt, so an earlier connection failure
	// stops blocking the tenants it affected.
	for _, want := range targets {
		delete(m.tenantIssues, want.tsg)
	}

	// A selection can span tenants, and each keeps its own device login, so
	// every tenant involved has to be asked about rather than only the first.
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

	m.status = ""
	m.phase = phaseCommand
	return m, m.cmdInput.Focus()
}

func (m tuiModel) onCommandKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.cmdInput.Blur()
		m.phase = phasePicking
		return m, nil
	case "tab":
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
	return m, cmd
}

func (m tuiModel) startRun(cmds []string) (tea.Model, tea.Cmd) {
	var picked []elementRow
	var ids []string
	for _, el := range m.all {
		if m.selected[el.key()] {
			picked = append(picked, el)
			ids = append(ids, el.ID)
		}
	}

	if !app.Defaults.AllowWrites && !m.cfg.AllowWrites {
		if err := ioncli.CheckReadOnly(cmds); err != nil {
			m.status = err.Error()
			return m, nil
		}
	}

	// Credentials are resolved per element, since a run can span tenants. The
	// flags still carry one pair so the shared validation has something to
	// check, but it is only a fallback for targets that supply none.
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

	return m, tea.Batch(waitForEvent(m.events), m.spinner.Tick)
}

func (m *tuiModel) applyFilter() {
	terms := strings.Fields(strings.ToLower(m.filter.Value()))
	m.matches = m.matches[:0]
	for i, el := range m.all {
		if m.showSelected && !m.selected[el.key()] {
			continue
		}
		if m.matchesTerms(el, terms) {
			m.matches = append(m.matches, i)
		}
	}
	m.rebuildRows()
	if c := m.table.Cursor(); c >= len(m.matches) {
		m.table.SetCursor(max(0, len(m.matches)-1))
	}
}

// matchesTerms requires every term to match the element somewhere. Splitting on
// whitespace is what makes a query like "s50 ion 1200" work: each word can land
// in a different field, so the store number and the model are found together
// without having to know which field holds what.
func (m tuiModel) matchesTerms(el elementRow, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	fields := m.searchFields(el)
	for _, term := range terms {
		if !matchesAnyField(term, fields) {
			return false
		}
	}
	return true
}

// searchField is one value a term can match.
type searchField struct {
	value string
	// exact restricts this field to whole-value matches. Opaque identifiers are
	// long digit strings, so a partial match in one is almost always
	// coincidence: searching "1200" for a model found an ion 2000 whose site id
	// happened to contain those digits.
	exact bool
}

// searchFields is everything a term may match against.
func (m tuiModel) searchFields(el elementRow) []searchField {
	state := "offline"
	if el.Connected {
		state = "connected"
	}

	// A site that did not resolve shows its raw id, which is as opaque as any
	// other identifier and must not be matched piecemeal.
	site := searchField{value: strings.ToLower(m.siteName(el))}
	if _, resolved := m.sites[el.TSG+"/"+el.SiteID]; !resolved {
		site.exact = true
	}

	return []searchField{
		{value: strings.ToLower(el.Name)},
		{value: strings.ToLower(el.ID), exact: true},
		site,
		{value: strings.ToLower(el.Tenant)},
		{value: strings.ToLower(el.Model)},
		{value: strings.ToLower(el.HWID), exact: true},
		{value: strings.ToLower(el.Software)},
		{value: strings.ToLower(el.TSG), exact: true},
		{value: state},
	}
}

func matchesAnyField(term string, fields []searchField) bool {
	for _, f := range fields {
		if f.exact {
			if f.value == term {
				return true
			}
			continue
		}
		if strings.Contains(f.value, term) {
			return true
		}
	}
	return false
}
func looseMatch(term, s string) bool {
	if term == "" {
		return true
	}
	at := 0
	for _, r := range s {
		if rune(term[at]) == r {
			at++
			if at == len(term) {
				return true
			}
		}
	}
	return false
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
	h := m.height - 11
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
	case phaseRunning:
		v = tea.NewView(m.viewRunning())
	case phaseResults:
		v = tea.NewView(m.viewResults())
	case phaseSession:
		content, cur := m.renderSession()
		v = tea.NewView(content)
		v.Cursor = cur
	}
	v.Content = ui.ClipFrame(v.Content, m.height)
	v.AltScreen = true
	return v
}

func (m tuiModel) viewPicking() string {
	var b strings.Builder

	if m.sessions.Count() > 0 {
		b.WriteString(m.tabBar() + "\n")
	}
	modeLabel := ui.StyleElement.Render(m.mode.String() + " mode")
	b.WriteString(ui.StyleCmd.Render("iontk") + "  " + modeLabel +
		ui.StyleDim.Render("   "+m.scopeLabel()+" · "+m.countLabel()) +
		m.selectedNote() + m.refreshNote() + "\n")

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
		b.WriteString(ui.StyleDim.Render("press / to filter") + "\n")
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
		if m.mode == modeBatch {
			act = fmt.Sprintf("enter command on %d", n)
		}
		// While typing a query the only keys that matter are the ones that end
		// it, so the hint says just those.
		clear := "alt+c clear"
		if len(m.lastCleared) > 0 {
			clear = "alt+z undo clear"
		}
		hint := fmt.Sprintf("↑↓/jk move · / filter · space select · alt+s selected · %s · alt+r refresh · alt+m mode · alt+u user · %s · esc/alt+w menu", clear, act)
		if m.showSelected {
			hint = fmt.Sprintf("↑↓/jk move · alt+s all · alt+c clear · space unselect · %s · esc/alt+w menu", act)
		}
		if m.filterOn {
			// With one match left, enter does the thing rather than just
			// applying the query, so the hint says which.
			enter := "enter apply"
			if len(m.matches) == 1 {
				enter = "enter open"
				if m.mode == modeBatch {
					enter = "enter select"
				}
			}
			hint = "↑↓ move · tab complete · " + enter + " · esc clear"
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
