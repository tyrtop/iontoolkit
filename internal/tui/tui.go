package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"tyrtop.com/iontk/internal/runtime"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/saseauth"
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
	phaseWorkflows
)

const (
	modeShell = runtime.ModeShell
	modeBatch = runtime.ModeBatch
	modeData  = runtime.ModeData
	modeMacro = runtime.ModeMacro
)

func (m tuiModel) mode() string {
	if m.rt == nil {
		return modeShell
	}
	return m.rt.Mode()
}

func (m tuiModel) armed() string {
	if m.rt == nil {
		return ""
	}
	return m.rt.Armed()
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

type discoveredMsg struct {
	err error
}

type signedInMsg struct {
	err error
}

type formField struct {
	label string
	input textinput.Model
}

type tuiModel struct {
	ctx context.Context

	phase  tuiPhase
	status string

	spinner spinner.Model

	menuCursor   int
	switchCursor int
	setCursor    int
	usersTSG     string
	pickCursor   int

	signInResume   bool
	resumeSessions []sessionTarget
	lastTargets    []sessionTarget

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
	switchDel     string
	tabEdit       bool
	tabRow        int
	pick          colorPick
	spinTick      int
	formThemeRole string
	typedZone     string
	resetArmed    bool

	rt *runtime.Runtime

	table      table.Model
	filter     textinput.Model
	filterOn   bool
	listCols   []elementColumn
	listSpec   []elementColumn
	listFrom   string
	columnsErr string
	listWidths []int
	listCells  []rowCells
	listCache  map[string]rowCells

	cmdInput textinput.Model
	cmdPick  suggest

	zoneInput  textinput.Model
	zonePick   suggest
	zoneReturn tuiPhase

	events   <-chan sase.Event
	progress progress.Model

	resFilter   textinput.Model
	resFilterOn bool
	resMatches  []int
	resTable    table.Model
	resOpen     bool
	resScroll   int
	resLine     int

	sesScroll  int
	connecting string
	redialing  string
	askRetry   sessionTarget
	askRetryNo bool

	stats        bool
	statsTicking bool

	viewFiles    []view.File
	keys         keymap
	keysErr      string
	wr           writeState
	wf           wfState
	scriptOn     string
	menuSpec     []menuEntry
	menuFrom     string
	menuErr      string
	dataFilter   textinput.Model
	dataFilterOn bool

	cmdline      textinput.Model
	cmdlineOn    bool
	seenSessions int
	seenData     int
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
	askMacro      string
	askMacroOn    []elementRow
	askMacroWhy   string

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

func Run(ctx context.Context, rt *runtime.Runtime) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("iontk needs a terminal; use --cmd for batch mode")
	}

	store, cfg := rt.Store(), rt.Config()
	if store == nil || cfg == nil {
		return fmt.Errorf("iontk could not open its config")
	}
	if Tenant != "" {
		if _, ok := app.ResolveTenant(cfg, Tenant); !ok {
			return fmt.Errorf("no saved tenant named %q; %s", Tenant, app.TenantChoices(cfg))
		}
	}

	logging.L().Info("tui started", "version", Version, "tenant", Tenant, "config", store.Root)

	model := modelOn(ctx, rt).raiseTutorialAsk()
	if Demo {
		model = model.openDemo()
	}
	defer model.sessions().CloseAll()
	// bubbletea's own reset is OSC 111, which not every terminal implements
	defer restoreTermColor(os.Stdout)

	p := tea.NewProgram(model, tea.WithContext(ctx))
	startFeed(p.Send, model.rt)
	_, err := p.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

func modelOn(ctx context.Context, rt *runtime.Runtime) tuiModel {
	store, cfg := rt.Store(), rt.Config()
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
		phase:   phaseMenu,
		spinner: s,

		filter:    fi,
		cmdInput:  ci,
		table:     newElementTable(),
		progress:  progress.New(progress.WithDefaultBlend(), progress.WithWidth(30), progress.WithoutPercentage()),
		resFilter: newResultsFilter(),
		resTable:  newResultsTable(),
		rt:        rt,
		width:     80,
		height:    24,
	}
	if z := cfg.Zone; z != "" && !strings.EqualFold(z, clock.UTC) && !strings.EqualFold(z, clock.Local) {
		m.typedZone = z
	}

	m.drawRuntime(app.LoadList(store))

	m.dataFilter = newDataFilter()
	if m.hasTenant() {
		m.runtime().PeekCache(m.tenant().TSGID)
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

func (m *tuiModel) moveCursorTo(n int) {
	m.moveCursor(n - m.cursor())
}

func (m *tuiModel) syncCursor() {
	rows := m.table.Rows()
	if len(rows) == 0 {
		return
	}
	n := max(0, min(m.cursor(), len(rows)-1))
	prev := m.table.Cursor()
	if n == prev {
		return
	}
	if n >= prev {
		m.table.MoveDown(n - prev)
		m.repaintCursorRows(prev)
		return
	}

	// table.MoveUp raises the viewport offset while the cursor falls
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
	_ = m.runtime().Run("list.cursor", map[string]string{"to": fmt.Sprintf("%+d", delta)})
	m.syncCursor()
}

func (m tuiModel) cursor() int {
	if m.rt == nil {
		return 0
	}
	return m.rt.Cursor()
}

func (m *tuiModel) listNav(key string) bool {
	page := max(1, m.table.Height())
	var to string
	switch key {
	case "up", "k":
		to = "up"
	case "down", "j":
		to = "down"
	case "b", "pgup":
		to = strconv.Itoa(-page)
	case "f", "pgdown", "space":
		to = "+" + strconv.Itoa(page)
	case "u", "ctrl+u":
		to = strconv.Itoa(-page / 2)
	case "d", "ctrl+d":
		to = "+" + strconv.Itoa(page/2)
	case "home", "g":
		to = "top"
	case "end", "G":
		to = "bottom"
	default:
		return false
	}
	_ = m.runtime().Run("list.cursor", nil, to)
	m.syncCursor()
	return true
}

const cacheFresh = config.CacheFresh

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if m.cacheState() == cacheUpdating && m.hasTenant() && m.runtime().NeedsConnect(m.tenant().TSGID) {
		cmds = append(cmds, fleetLoad(m.ctx, m.runtime()))
	}
	cmds = append(cmds, probeTermColor())
	return tea.Batch(cmds...)
}

func (m tuiModel) refreshNow() (tea.Model, tea.Cmd) {
	if !m.hasTenant() {
		m.status = "no tenant selected"
		return m, nil
	}
	m.status = ""
	m.runtime().ResetHAProgress()
	return m.refreshActive()
}

func signIn(ctx context.Context, rt *runtime.Runtime) tea.Cmd {
	return func() tea.Msg {
		return signedInMsg{err: rt.Exec(&runtime.Call{Ctx: ctx}, "auth.tenant")}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)

	mm, ok := next.(tuiModel)
	if ok && mm.rt != m.rt {
		watchRuntime(mm.rt)
	}
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

	case tenantsWarmedMsg:
		return m.onTenantsWarmed(msg)

	case discoveredMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.phase = phaseMenu
			return m, nil
		}
		next, cmd := m.startTenantPick()
		return next, cmd

	case tokenRefreshedMsg:
		if msg.err != nil {
			if errors.Is(msg.err, saseauth.ErrInteractiveRequired) {
				m.signInResume = false
				return m.startSignInAuto()
			}
			m.status = "token refresh failed: " + msg.err.Error()
			return m, nil
		}
		m.status = "token refreshed"
		return m, nil

	case signedInMsg:
		if msg.err != nil {
			m.status = "sign-in failed: " + msg.err.Error()
			m.signInResume = false
			m.phase = phaseMenu
			return m, nil
		}
		m.status = ""
		if targets := m.resumeSessions; len(targets) > 0 {
			m.resumeSessions = nil
			next, cmd := m.openSessions(targets)
			return next, cmd
		}
		if targets := m.resumeData; len(targets) > 0 {
			m.resumeData = nil
			next, cmd := m.openDataTabs(targets)
			return next, cmd
		}
		if m.signInResume {
			m.signInResume = false
			next, cmd := m.startContinue()
			return next, cmd
		}

		m.phase = phaseSignedIn
		next, cmd := m.refreshActive()
		return next, tea.Batch(m.spinner.Tick, cmd)

	case fleetDoneMsg:
		return m.onFleetDone(msg)

	case runtimeMsg:
		return m.onRuntime(msg)

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
		return m, tea.Batch(waitForDial(m.runtime().DialReports()), m.spinner.Tick)

	case sessionOpenedMsg:
		m = m.onSessionOpened(string(msg))
		return m, waitForSession(m.sessions().Dirty())

	case sessionErrMsg:
		m.connecting = ""
		if m.runtime().NeedsSignIn([]string{msg.target.tsg}) {
			m.dialDone(msg.target.eid)
			return m.signInThenSessions(m.lastTargets)
		}
		m.dialFailed(msg.target.eid, msg.err)
		m.status = msg.err
		m.rebuildRows()
		if msg.target.eid != "" {
			m.askRetry = msg.target
			return m, nil
		}
		if m.sessions().Count() == 0 && m.phase == phaseSession {
			m.phase = phasePicking
		}
		return m, nil

	case sessionDirtyMsg:
		if n := m.sessions().Reap(); n > 0 && m.phase == phaseSession && m.sessions().Count() == 0 {
			m.status = ""
			m.phase = phasePicking
		}
		return m, waitForSession(m.sessions().Dirty())

	case batchStartedMsg:
		return m.onBatchStarted(msg)

	case eventMsg:
		return m.onEvent(sase.Event(msg))

	case streamClosedMsg:
		m.phase = phaseResults
		m.runtime().EndBatch()
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

	case dataDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		return m, nil

	case compareDoneMsg:
		return m.onCompareDone(msg)

	case writePlannedMsg:
		return m.onWritePlanned(msg)

	case writeRecsMsg:
		return m.onWriteRecs(msg)

	case wfLineMsg:
		return m.onWorkflowLine(msg)

	case wfDoneMsg:
		return m.onWorkflowDone(msg)

	case writeSentMsg:
		return m.onWriteSent(msg)

	case commandsSavedMsg:
		return m.onCommandsSaved(msg)

	case scriptDoneMsg:
		return m.onScriptDone(msg)

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
			return m, cmd
		}
		return m, nil

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
			if sess := m.sessions().Active(); sess != nil && sess.Err() == nil {
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
		return m.cacheState() == cacheUpdating || m.fetchWorking()
	case phaseSession:
		return m.connectingAny() || m.awaitingDevice()
	case phaseEdit:
		return false
	case phaseWorkflows:
		return m.wf.step == wfRunning
	case phaseData:
		if m.wr.step == writeLoading || m.wr.step == writeSending {
			return true
		}
		for _, t := range m.dataTabs() {
			if t.Loading() || t.Check.Loading {
				return true
			}
		}
	}
	return false
}

func (m *tuiModel) setElements(els []elementRow) {
	m.runtime().SetElements(els)

	names := make([]string, 0, len(els))
	for _, el := range m.rows() {
		if el.Name != "" {
			names = append(names, el.Name)
		}
	}
	m.filter.SetSuggestions(names)
	m.forgetRows()
	m.applyFilter()
}

func (m *tuiModel) rebuildRows() {
	cols := m.columns()
	if !sameColumns(cols, m.listCols) {
		m.listCols, m.listCache = cols, nil
	}
	if m.listCache == nil {
		m.listCache = make(map[string]rowCells, len(m.rows()))
	}

	cells := make([]rowCells, 0, len(m.matched()))
	widths := make([][]int, 0, len(m.matched()))
	for _, i := range m.matched() {
		rc := m.rowCells(cols, m.rows()[i])
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
		m.table.SetRows(nil)
	}
	m.table.SetColumns(tcols)
	m.table.SetRows(rows)
	if len(rows) > 0 && m.table.Cursor() != m.cursor() {
		m.table.SetCursor(m.cursor())
	}
	m.table.SetWidth(min(m.width, total))
	m.table.SetHeight(m.listHeight())
}

func (m *tuiModel) listRow(n int) table.Row {
	rc := m.listCells[n]
	el := m.rows()[m.matched()[n]]
	here := n == m.cursor()

	row := func(s string) string { return s }
	if el.Connected && !here {
		row = func(s string) string { return ui.StyleOK.Render(s) }
	}
	ticked := m.isSelected(el.Key())

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
	return m.windowAround(m.cursor(), n)
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
	repaint(m.cursor())
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
		return m, waitForEvent(m.events)
	}
	m.applyResultFilter()
	return m, tea.Batch(
		m.progress.SetPercent(float64(m.batch().Finished)/float64(m.total())),
		waitForEvent(m.events),
	)
}

func (m tuiModel) batch() runtime.Batch {
	if m.rt == nil {
		return runtime.Batch{}
	}
	return m.rt.Batch()
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

	if m.phase == phaseData && m.wr.step != writeOff {
		return m.onWriteKey(msg, key)
	}

	if m.phase == phaseWorkflows {
		return m.onWorkflowKey(msg, key)
	}

	if m.cmdlineOn {
		return m.onCmdlineKey(msg, key)
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

			if len(m.matched()) == 1 {
				el := m.rows()[m.matched()[0]]
				if m.mode() == modeBatch || m.mode() == modeMacro {
					m.runtime().Select(el.Key())
					m.rebuildRows()
					return m, nil
				}
				m.moveCursorTo(0)
				return m.onPickTargets([]sessionTarget{
					{eid: el.ID, name: el.Label(), tsg: el.TSG},
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
		return m, cmd
	}

	m.listNav(key)
	return m, nil
}

func (m tuiModel) pickTargets() []sessionTarget { return targetsOf(m.runtime().Targets()) }

func (m tuiModel) onPickEnter() (tea.Model, tea.Cmd) {
	if m.mode() == modeBatch && m.selectedCount() == 0 {
		return m.toggleCurrent(), nil
	}
	return m.onPickTargets(m.pickTargets())
}

func (m tuiModel) onPickTargets(targets []sessionTarget) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		return m, nil
	}

	if m.mode() == modeData {
		return m.openDataTabs(targets)
	}

	for _, want := range targets {
		t := m.runtime().TenantFor(want.tsg)
		if app.NeedsIONPassword(m.store(), t, m.ionUserFor(t)) {
			return m.openPasswordForm(t, m.ionUserFor(t))
		}
	}

	if m.mode() == modeShell {
		return m.openSessions(targets)
	}
	if m.mode() == modeMacro {
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
		_ = m.runtime().Run("user.next", nil)
		return m, nil
	case "enter":
		cmds := runtime.SplitCommands(m.cmdInput.Value())
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

func (m tuiModel) startRun(cmds []string) (tea.Model, tea.Cmd) {
	return m.startRunOn(cmds, m.runtime().Selected())
}

func (m tuiModel) startRunOn(cmds []string, picked []elementRow) (tea.Model, tea.Cmd) {
	m.status = "starting the run"
	for _, tsg := range tsgsOfRows(picked) {
		if m.runtime().NeedsConnect(tsg) {
			m.status = "connecting tenants"
			break
		}
	}
	rt, ctx := m.runtime(), m.ctx
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		events, err := rt.StartBatch(ctx, cmds, picked)
		return batchStartedMsg{events: events, err: err}
	})
}

func (m tuiModel) onBatchStarted(msg batchStartedMsg) (tea.Model, tea.Cmd) {
	var mismatch *runtime.MacroMismatchError
	if errors.As(msg.err, &mismatch) {
		m.status = ""
		m.askMacro, m.askMacroOn, m.askMacroWhy = msg.macro, msg.rows, mismatch.Why
		return m, nil
	}
	if msg.err != nil {
		m.status = msg.err.Error()
		return m, nil
	}
	m.cmdInput.Blur()
	m.status = ""
	m.resMatches = nil
	m.resOpen, m.resScroll = false, 0
	m.resFilter.SetValue("")
	m.resFilterOn = false
	m.phase = phaseRunning
	m.events = msg.events
	return m, tea.Batch(waitForEvent(m.events), m.spinner.Tick)
}

func (m *tuiModel) applyFilter() {
	_ = m.runtime().Run("list.filter", map[string]string{"query": m.filter.Value()})
	m.rebuildRows()
}

func (m tuiModel) current() (elementRow, bool) {
	c := m.cursor()
	if c < 0 || c >= len(m.matched()) {
		return elementRow{}, false
	}
	return m.rows()[m.matched()[c]], true
}

func (m tuiModel) total() int { return m.batch().Total() }

func (m tuiModel) listHeight() int {
	h := m.height - 13 - m.statsRows()
	if m.sessions().Count() > 0 {
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
		if m.wr.step != writeOff {
			v = tea.NewView(m.viewWrite())
			break
		}
		v = tea.NewView(m.viewData())
	case phaseMacros:
		v = tea.NewView(m.viewMacros())
	case phaseWorkflows:
		v = tea.NewView(m.viewWorkflows())
	case phaseEdit:
		v = tea.NewView(m.viewEdit())
	}
	if m.cmdlineOn {
		v = tea.NewView(m.overlayCmdline(v.Content))
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

	if m.sessions().Count() > 0 {
		b.WriteString(m.tabBar() + "\n")
	}
	b.WriteString(ui.StyleCmd.Render("iontk") + "  " + m.modeLabel() +
		ui.StyleDim.Render("   "+m.scopeLabel()+" · "+m.countLabel()) +
		m.selectedNote() + "\n")
	b.WriteString(m.refreshNote() + "\n")

	if m.filterOn || m.filter.Value() != "" {
		line := m.filter.View()
		if n := len(m.matched()); n > 0 && m.filter.Value() != "" {
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
		n := m.selectedCount()
		if n == 0 {
			n = 1
		}
		act := fmt.Sprintf("enter open %d shell", n)
		if n != 1 {
			act += "s"
		}
		switch m.mode() {
		case modeBatch:
			act = fmt.Sprintf("enter command on %d", n)
			if m.selectedCount() == 0 {
				act = "enter select"
			}
		case modeData:
			act = fmt.Sprintf("enter data on %d", n)
		case modeMacro:
			act = fmt.Sprintf("enter run %s on %d", m.armed(), n)
		}
		clear := m.legend(scrElements, "clear", "clear-selection")
		if m.canUndoClear() {
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
		if m.showingSelected() {
			hint = fmt.Sprintf("↑↓/jk move · alt+s all · alt+c clear · space unselect · %s · esc/q/alt+w %s", act, back)
		}
		if m.filterOn {
			enter := "enter apply"
			if len(m.matched()) == 1 {
				enter = "enter open"
				if m.mode() == modeBatch {
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
		ui.StyleDim.Render(fmt.Sprintf("   %d elements selected", m.selectedCount())) + "\n\n")

	runAs := m.ionUser()
	if runAs == "" {
		runAs = ui.StyleWarn.Render("no ION user")
	}
	b.WriteString(ui.StyleDim.Render("run as  ") + ui.StyleElement.Render(runAs))
	if len(m.tenant().Users) > 1 {
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
	b.WriteString(ui.StyleCmd.Render(strings.Join(m.batch().Commands, ", ")) + "\n")

	counts := fmt.Sprintf("  %d/%d", m.batch().Finished, m.total())
	if m.batch().Failed > 0 {
		counts += ui.StyleErr.Render(fmt.Sprintf("  %d failed", m.batch().Failed))
	}
	b.WriteString(m.spinner.View() + " " + m.progress.View() + counts + "\n")

	avail := max(2, m.height-6-ui.BannerHeight(m.width, m.height))
	errShown := min(len(m.batch().Errs), max(1, avail/2))
	flightShown := min(len(m.batch().Running), max(1, avail-errShown))

	if errShown > 0 {
		b.WriteString("\n")
		for _, g := range m.batch().Errs[:errShown] {
			b.WriteString(fmt.Sprintf("%s %d × %s %s\n", ui.MarkCross, g.Count, g.Msg,
				ui.StyleDim.Render("(first: "+g.First+")")))
		}
		if n := len(m.batch().Errs) - errShown; n > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more error kinds\n", n)))
		}
	}

	if flightShown > 0 {
		b.WriteString("\n")
		for _, f := range m.batch().Running[:flightShown] {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				ui.StyleElement.Render(f.Element), ui.StyleDim.Render(short(time.Since(f.Started)))))
		}
		if n := len(m.batch().Running) - flightShown; n > 0 {
			b.WriteString(ui.StyleDim.Render(fmt.Sprintf("  … %d more\n", n)))
		}
	}

	return b.String()
}

func (m tuiModel) listBack() (tea.Model, tea.Cmd) {
	m.status = ""
	m.phase = m.listReturn()
	return m, nil
}

func (m tuiModel) toSessions() (tea.Model, tea.Cmd) {
	if m.sessions().Count() > 0 {
		m.phase = phaseSession
	}
	return m, nil
}
