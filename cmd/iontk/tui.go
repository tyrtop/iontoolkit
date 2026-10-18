package main

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
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/saseauth"
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
	phaseAuthPick
)

type pickMode int

const (
	modeShell pickMode = iota
	modeBatch
)

func (p pickMode) String() string {
	if p == modeBatch {
		return "batch"
	}
	return "shell"
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

// signedInMsg carries the outcome of a browser sign-in, including the facts
// that prove it worked: a client that authenticated from the stored refresh
// token, and a profile fetched with it.
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
	flags  flags

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
	authCursor   int
	signInResume bool
	ident        saseauth.Identity
	profile      api.Profile
	tokenTTL     time.Duration

	form           []formField
	formCursor     int
	formKind       formKind
	formTitle      string
	formReturn     tuiPhase
	formTenantName string
	formUserName   string
	formAuth       string
	formOldTSG     string
	formGeoRow     int

	setSection int
	setFocus   int

	all      []api.Element
	sites    map[string]string
	matches  []int
	selected map[string]bool
	table    table.Model
	filter   textinput.Model
	filterOn bool

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

func runTUI(ctx context.Context) error {
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
		spinnerPick = defaultSpinner
	}
	s := newSpinner(spinnerPick)

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
		spinnerName: spinnerName(spinnerPick),
		selected:    map[string]bool{},
		filter:      fi,
		cmdInput:    ci,
		table:       newElementTable(),
		progress:    progress.New(progress.WithDefaultBlend(), progress.WithWidth(30), progress.WithoutPercentage()),
		resFilter:   newResultsFilter(),
		resTable:    newResultsTable(),
		sessions:    ionterm.NewManager(),
		width:       80,
		height:      24,
	}
	m.tenant, m.hasTenant = resolveTenant(cfg, "")
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
		f := tenantFlags(m.store, m.tenant, m.ionUser)
		if f.validateCreds() == nil {
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
	f := tenantFlags(m.store, m.tenant, m.ionUser)
	if err := f.validateCreds(); err != nil {
		m.status = err.Error()
		m.cacheState = cacheFailed
		return m, nil
	}
	m.flags = f
	m.status = ""
	m.cacheState = cacheUpdating
	if m.client != nil {
		return m, tea.Batch(m.spinner.Tick, refreshElements(m.ctx, m.client, m.store, m.tenant.TSGID))
	}
	return m, tea.Batch(m.spinner.Tick, connect(m.ctx, f))
}

func connect(ctx context.Context, f flags) tea.Cmd {
	return func() tea.Msg {
		c, err := newClient(ctx, f, io.Discard)
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
func signIn(ctx context.Context, f flags, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		httpClient := &http.Client{Timeout: f.HTTPTimeout}
		src := browserSource(f, httpClient, st)
		if err := src.SignIn(ctx); err != nil {
			return signedInMsg{err: err}
		}
		ident := src.Identity()
		if !ident.ScopedTo(f.TSGID) {
			return signedInMsg{err: fmt.Errorf("signed in, but the token is scoped to %q rather than tenant %s", ident.Scope, f.TSGID)}
		}

		c, err := newClient(ctx, f, io.Discard)
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

	case needsSignInMsg:
		if m.phase == phaseMenu {
			m.status = "sign-in required — pick the browser sign-in below"
			m.cacheState = cacheFailed
			return m, nil
		}
		m.signInResume = true
		return m.startSignIn()

	case signedInMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.cacheState = cacheFailed
			m.signInResume = false
			m.phase = phaseMenu
			return m, nil
		}
		m.client = msg.client
		m.ident = msg.ident
		m.profile = msg.profile
		m.tokenTTL = msg.ttl
		m.status = ""
		m.phase = phaseSignedIn
		// Elements are fetched behind the confirmation, so dismissing it lands on
		// a list that is already current.
		m.cacheState = cacheUpdating
		return m, tea.Batch(m.spinner.Tick, refreshElements(m.ctx, m.client, m.store, m.tenant.TSGID))

	case clientReadyMsg:
		m.client = msg.client
		if m.cacheState == cacheCached {
			if m.phase == phaseLoading {
				m.phase = phasePicking
			}
			return m, nil
		}
		return m, refreshElements(m.ctx, m.client, m.store, m.tenant.TSGID)

	case elementsErrMsg:
		m.status = string(msg)
		m.cacheState = cacheFailed
		if len(m.all) == 0 {
			m.phase = phaseMenu
		}
		return m, nil

	case elementsLoadedMsg:
		m.sites = msg.sites
		m.setElements(msg.elements)
		m.cacheState = cacheUpdated
		m.cacheAt = time.Now()
		m.cacheCount = len(m.all)
		if m.phase == phaseLoading {
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
	case phaseLoading, phaseRunning, phaseSettings, phaseSignIn:
		return true
	case phaseSession:
		return m.connecting != ""
	}
	return false
}

func (m *tuiModel) setElements(els []api.Element) {
	m.all = els
	sort.Slice(m.all, func(i, j int) bool { return m.all[i].Name < m.all[j].Name })

	names := make([]string, 0, len(m.all))
	for _, el := range m.all {
		if el.Name != "" {
			names = append(names, el.Name)
		}
	}
	m.filter.SetSuggestions(names)
	m.applyFilter()
}

func (m tuiModel) siteName(el api.Element) string {
	if n, ok := m.sites[el.SiteID]; ok && n != "" {
		return n
	}
	if el.SiteID != "" {
		return el.SiteID
	}
	return "-"
}

func (m *tuiModel) rebuildRows() {
	rows := make([]table.Row, 0, len(m.matches))
	for _, i := range m.matches {
		el := m.all[i]
		mark := " "
		if m.selected[el.ID] {
			mark = "x"
		}
		name := el.Name
		if name == "" {
			name = el.ID
		}
		state := "offline"
		if el.Connected {
			state = "connected"
		}
		rows = append(rows, table.Row{
			mark,
			truncate(name, 26),
			truncate(m.siteName(el), 22),
			truncate(el.Model, 11),
			state,
		})
	}
	m.table.SetRows(rows)
	m.table.SetWidth(min(m.width, 78))
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
	case phaseAuthPick:
		return m.onAuthPickKey(key)
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

	case "up", "down", "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	case "space":
		if el, ok := m.current(); ok {
			if m.selected[el.ID] {
				delete(m.selected, el.ID)
			} else {
				m.selected[el.ID] = true
			}
			m.rebuildRows()
		}
		return m, nil

	case "alt+r":
		return m.refreshNow()

	case "alt+u":
		m.userCursor = 0
		m.status = ""
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
			if !m.selected[m.all[i].ID] {
				all = false
				break
			}
		}
		for _, i := range m.matches {
			if all {
				delete(m.selected, m.all[i].ID)
			} else {
				m.selected[m.all[i].ID] = true
			}
		}
		m.rebuildRows()
		return m, nil

	case "enter":
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
		m.sessions.CloseAll()
		return m, tea.Quit
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

func (m tuiModel) pickTargets() [][2]string {
	var out [][2]string
	for _, el := range m.all {
		if !m.selected[el.ID] {
			continue
		}
		name := el.Name
		if name == "" {
			name = el.ID
		}
		out = append(out, [2]string{el.ID, name})
	}
	if len(out) > 0 {
		return out
	}
	if el, ok := m.current(); ok {
		name := el.Name
		if name == "" {
			name = el.ID
		}
		return [][2]string{{el.ID, name}}
	}
	return nil
}

func (m tuiModel) onPickEnter() (tea.Model, tea.Cmd) {
	targets := m.pickTargets()
	if len(targets) == 0 {
		return m, nil
	}

	if needsIONPassword(m.store, m.tenant, m.ionUser) {
		return m.openPasswordForm()
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
		cmds := splitCommands([]string{m.cmdInput.Value()})
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
	var ids []string
	for _, el := range m.all {
		if m.selected[el.ID] {
			ids = append(ids, el.ID)
		}
	}

	if !flagAllowWrites && !m.cfg.AllowWrites {
		if err := ioncli.CheckReadOnly(cmds); err != nil {
			m.status = err.Error()
			return m, nil
		}
	}

	f := tenantFlags(m.store, m.tenant, m.ionUser)
	f.Elements = ids
	f.Commands = cmds
	if err := f.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	opts := f.options()
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
	m.events = sase.RunStream(m.ctx, m.client, opts, ids)

	return m, tea.Batch(waitForEvent(m.events), m.spinner.Tick)
}

func (m *tuiModel) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.matches = m.matches[:0]
	for i, el := range m.all {
		if q == "" ||
			strings.Contains(strings.ToLower(el.Name), q) ||
			strings.Contains(strings.ToLower(el.ID), q) ||
			strings.Contains(strings.ToLower(m.siteName(el)), q) ||
			strings.Contains(strings.ToLower(el.Model), q) {
			m.matches = append(m.matches, i)
		}
	}
	m.rebuildRows()
	if c := m.table.Cursor(); c >= len(m.matches) {
		m.table.SetCursor(max(0, len(m.matches)-1))
	}
}

func (m tuiModel) current() (api.Element, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.matches) {
		return api.Element{}, false
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
	case phaseAuthPick:
		v = tea.NewView(m.viewAuthPick())
	case phaseSignIn:
		v = tea.NewView(m.viewSignIn())
	case phaseSignedIn:
		v = tea.NewView(m.viewSignedIn())
	case phaseLoading:
		v = tea.NewView(banner(m.width, m.height) +
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
	v.Content = clipFrame(v.Content, m.height)
	v.AltScreen = true
	return v
}

func footer(status, hint string) string {
	line := ""
	if status != "" {
		line = styleWarn.Render(status)
	}
	return line + "\n" + styleDim.Render(hint)
}

func clipFrame(s string, height int) string {
	if height <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}

func (m tuiModel) viewPicking() string {
	var b strings.Builder

	if m.sessions.Count() > 0 {
		b.WriteString(m.tabBar() + "\n")
	}
	modeLabel := styleElement.Render(m.mode.String() + " mode")
	b.WriteString(styleCmd.Render("iontk") + "  " + modeLabel +
		styleDim.Render(fmt.Sprintf("   %s · %d selected / %d elements", m.tenant.Name, len(m.selected), len(m.all))) + "\n")

	if m.filterOn || m.filter.Value() != "" {
		line := m.filter.View()
		if n := len(m.matches); n > 0 && m.filter.Value() != "" {
			line += styleDim.Render(fmt.Sprintf("   %d match", n))
			if n != 1 {
				line += styleDim.Render("es")
			}
		}
		b.WriteString(line + "\n")
	} else {
		b.WriteString(styleDim.Render("press / to filter") + "\n")
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
		hint := fmt.Sprintf("↑↓ move · / filter · space select · alt+m mode · alt+u user · alt+r refresh · %s · esc menu · alt+w quit", act)
		b.WriteString(footer(m.status, hint))
	}
	return b.String()
}

func (m tuiModel) viewCommand() string {
	var b strings.Builder
	b.WriteString(styleCmd.Render("commands") +
		styleDim.Render(fmt.Sprintf("   %d elements selected", len(m.selected))) + "\n\n")

	runAs := m.ionUser
	if runAs == "" {
		runAs = styleWarn.Render("no ION user")
	}
	b.WriteString(styleDim.Render("run as  ") + styleElement.Render(runAs))
	if len(m.tenant.Users) > 1 {
		b.WriteString(styleDim.Render("   tab to change"))
	}
	b.WriteString("\n\n")

	b.WriteString(m.cmdInput.View() + "\n\n")
	b.WriteString(styleDim.Render("comma separated · read-only commands only") + "\n")
	if m.status != "" {
		b.WriteString(styleErr.Render(m.status) + "\n")
	} else {
		b.WriteString(styleDim.Render("enter run · esc back") + "\n")
	}
	return b.String()
}

func (m tuiModel) viewRunning() string {
	var b strings.Builder

	b.WriteString(banner(m.width, m.height))
	b.WriteString(styleCmd.Render(strings.Join(m.flags.Commands, ", ")) + "\n")

	counts := fmt.Sprintf("  %d/%d", m.finished, m.total())
	if m.failed > 0 {
		counts += styleErr.Render(fmt.Sprintf("  %d failed", m.failed))
	}
	b.WriteString(m.spinner.View() + " " + m.progress.View() + counts + "\n")

	avail := max(2, m.height-6-bannerHeight(m.width, m.height))
	errShown := min(len(m.errGroups), max(1, avail/2))
	flightShown := min(len(m.running), max(1, avail-errShown))

	if errShown > 0 {
		b.WriteString("\n")
		for _, g := range m.errGroups[:errShown] {
			b.WriteString(fmt.Sprintf("%s %d × %s %s\n", markCross, g.count, g.msg,
				styleDim.Render("(first: "+g.first+")")))
		}
		if n := len(m.errGroups) - errShown; n > 0 {
			b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more error kinds\n", n)))
		}
	}

	if flightShown > 0 {
		b.WriteString("\n")
		for _, f := range m.running[:flightShown] {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				styleElement.Render(f.element), styleDim.Render(short(time.Since(f.started)))))
		}
		if n := len(m.running) - flightShown; n > 0 {
			b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more\n", n)))
		}
	}

	return b.String()
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}
