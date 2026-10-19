package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ioncmd"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

const (
	focusNav = iota
	focusContent
)

// The General rows, named because the key handler, the content and the tests
// all have to agree on the order. The refresh is last: it is the one entry
// here that does something rather than setting something.
// The order here is the order sectionContent prints them in, and
// TestGeneralRowsMatchTheirLabels is what keeps the two from drifting: a row
// that moves without its constant sends the cursor to someone else's setting.
const (
	guardRow = iota
	modeRow
	jumpRow
	zoneRow
	refreshRow
	generalRows
)

// generalRowLabels is each row's gutter label, for that test and for nothing
// else.
var generalRowLabels = map[int]string{
	guardRow: "commands", modeRow: "mode", jumpRow: "jump",
	zoneRow: "times", refreshRow: "elements",
}

var setSections = []string{"General", "Theme", "Batch", "Tenants", "Users", "Session", "Debug", "Reset"}

const (
	secGeneral = iota
	secTheme
	secBatch
	secTenants
	secUsers
	secSession
	secDebug
	secReset
)

func (m tuiModel) sectionRows() int {
	switch m.setSection {
	case secGeneral:
		return generalRows
	case secTheme:
		if m.themeEdit {
			// The editor has its own cursor; the section's would otherwise
			// move the theme list underneath it.
			return 0
		}
		return len(ui.Themes())
	case secBatch:
		return 4
	case secTenants:
		return len(m.cfg.Tenants)
	case secUsers:
		return len(m.tenant.Users)
	case secSession:
		return 2
	case secDebug:
		return 0
	case secReset:
		return 1
	}
	return 0
}

func (m tuiModel) onSettingsKey(key string) (tea.Model, tea.Cmd) {
	// Arming the reset lasts exactly one keypress. Leaving the row with it
	// still set would mean a later y resets without having been asked.
	armed := m.resetArmed
	m.resetArmed = false

	if key == "enter" && m.setFocus == focusNav {
		if m.sectionRows() > 0 {
			m.setFocus = focusContent
			m.setCursor = 0
		}
		return m, nil
	}

	switch key {
	case "esc", "q":
		if m.setFocus == focusContent {
			m.setFocus = focusNav
			m.status = ""
			return m, nil
		}
		m.phase = phaseMenu
		return m, nil

	case "tab", "right", "l":
		if m.sectionRows() > 0 {
			m.setFocus = focusContent
			m.setCursor = min(m.setCursor, m.sectionRows()-1)
		}
		return m, nil

	case "shift+tab", "left", "h":
		m.setFocus = focusNav
		return m, nil

	case "up", "k":
		if m.setFocus == focusNav {
			m.setSection = max(0, m.setSection-1)
			m.setCursor = 0
		} else {
			m.setCursor = max(0, m.setCursor-1)
		}
		return m, nil

	case "down", "j":
		if m.setFocus == focusNav {
			m.setSection = min(len(setSections)-1, m.setSection+1)
			m.setCursor = 0
		} else {
			m.setCursor = min(max(0, m.sectionRows()-1), m.setCursor+1)
		}
		return m, nil
	}

	if m.setFocus != focusContent {
		return m, nil
	}

	switch m.setSection {
	case secGeneral:
		return m.onGeneralKey(key)
	case secTheme:
		return m.onThemeKey(key)
	case secBatch:
		return m.onBatchKey(key)
	case secTenants:
		return m.onTenantsKey(key)
	case secUsers:
		return m.onUsersSectionKey(key)
	case secSession:
		return m.onSessionGeoKey(key)
	case secReset:
		return m.onResetKey(key, armed)
	}
	return m, nil
}

// onResetKey arms on enter and fires on y. Two keys, because nothing else in
// iontk destroys credentials that cannot be fetched again.
func (m tuiModel) onResetKey(key string, armed bool) (tea.Model, tea.Cmd) {
	if !armed {
		if key == "enter" {
			m.resetArmed = true
			m.status = ""
		}
		return m, nil
	}
	if key != "y" {
		m.status = "reset cancelled"
		return m, nil
	}
	return m.factoryReset()
}

// factoryReset throws away the saved state and leaves the model looking like a
// first run, which is the only honest way to show that it worked.
func (m tuiModel) factoryReset() (tea.Model, tea.Cmd) {
	// Sessions hold a token that has just been deleted, and a device login that
	// cannot be typed again from anywhere in this process.
	m.sessions.CloseAll()

	if err := m.store.Reset(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.cfg = &config.Config{}
	m.tenant, m.hasTenant, m.ionUser = config.Tenant{}, false, ""
	m.authMode = authBrowser
	m.pool = newClientPool()
	m.client = nil
	m.setElements(nil)
	m.sites = nil
	m.selected, m.lastCleared = map[string]bool{}, nil
	m.discovered, m.tenantIssues, m.runAs = nil, nil, nil
	m.ident, m.profile, m.tokenTTL = saseauth.Identity{}, api.Profile{}, 0
	m.tokenDead = ""
	m.cacheState, m.cacheCount, m.cacheAt = cacheNone, 0, time.Time{}
	m.setSection, m.setFocus, m.setCursor = secGeneral, focusNav, 0
	m.menuCursor = 0
	m.phase = phaseMenu
	m.status = "reset — nothing is saved any more"
	return m, nil
}

func (m tuiModel) onGeneralKey(key string) (tea.Model, tea.Cmd) {
	if key != "enter" && key != " " && key != "space" {
		return m, nil
	}
	// The refresh stays the last row of the section: it is the one entry here
	// that does something rather than setting something.
	if m.setCursor == refreshRow {
		return m.refreshNow()
	}
	switch m.setCursor {
	case zoneRow:
		// Typed rather than cycled, since there are 600 IANA names. Space
		// still reaches the two anyone wants without a form.
		if key == "enter" {
			return m.openZoneForm()
		}
		return m.cycleZone()
	case modeRow:
		return m.cycleDefaultMode()
	case jumpRow:
		return m.cyclePageStep()
	case guardRow:
		m.cfg.AllowWrites = !m.cfg.AllowWrites
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
		}
	}
	return m, nil
}

func (m tuiModel) onTenantsKey(key string) (tea.Model, tea.Cmd) {
	if m.setCursor >= len(m.cfg.Tenants) {
		return m, nil
	}
	t := m.cfg.Tenants[m.setCursor]

	switch key {
	case " ", "space":
		added := !t.Added
		t.Added = added
		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.tenant.Name == t.Name {
			m.tenant.Added = added
		}
		// Dropping a tenant from the set has to drop its elements too.
		if !added {
			kept := make([]elementRow, 0, len(m.all))
			for _, r := range m.all {
				if r.TSG != t.TSGID {
					kept = append(kept, r)
				}
			}
			m.setElements(kept)
			m.cacheCount = len(m.all)
		}
		return m, nil
	case "a":
		return m.openTenantForm(config.AuthServiceAccount)
	case "enter":
		return m.openEditTenantForm(t)
	case "s":
		m.tenant, m.hasTenant = t, true
		m.ionUser = t.DefaultUser()
		m.cfg.LastTenant = t.Name
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.all, m.matches, m.selected = nil, nil, map[string]bool{}
		m.cacheState, m.cacheCount, m.cacheAt = cacheNone, 0, time.Time{}
		m.status = "switched to " + t.Name
	case "u":
		return m.openUserFormFor(t)
	case "x":
		if err := forgetTenant(m.store, m.cfg, t); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.tenant.Name == t.Name {
			m.tenant, m.hasTenant = app.ResolveTenant(m.cfg, "")
			m.ionUser = m.tenant.DefaultUser()
		}
		m.setCursor = max(0, min(m.setCursor, len(m.cfg.Tenants)-1))
	}
	return m, nil
}

func (m tuiModel) onUsersSectionKey(key string) (tea.Model, tea.Cmd) {
	users := m.tenant.Users

	switch key {
	case "a":
		if m.tenantIndex() >= 0 {
			return m.openUserFormFor(m.tenant)
		}
		m.status = "no tenant selected"
		return m, nil
	}

	if m.setCursor >= len(users) {
		return m, nil
	}
	name := users[m.setCursor].Name

	switch key {
	case "p":
		return m.openEditPasswordForm(name)
	case "enter":
		return m.openEditUserForm(name)
	case "r":
		m.ionUser = name
		m.status = "running as " + name
	case "d":
		t := m.tenant
		t.AddUser(name, true)
		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.tenant, m.ionUser = t, name
	case "x":
		t := m.tenant
		if t.RemoveUser(name) {
			_ = m.store.Secrets.Delete(config.IONPassKey(t.TSGID, name))
			m.cfg.Upsert(t)
			if err := m.store.SaveConfig(m.cfg); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.tenant = t
			if m.ionUser == name {
				m.ionUser = t.DefaultUser()
			}
			m.setCursor = max(0, min(m.setCursor, len(t.Users)-1))
		}
	}
	return m, nil
}

func (m tuiModel) onSessionGeoKey(key string) (tea.Model, tea.Cmd) {
	cols, rows := m.sessionGeometry()

	if key == "enter" {
		return m.openGeometryForm(m.setCursor, cols, rows)
	}

	step := 0
	switch key {
	case "-", "_":
		step = -10
	case "+", "=":
		step = 10
	default:
		return m, nil
	}

	if m.setCursor == 0 {
		cols = min(400, max(40, cols+step))
	} else {
		rows = min(200, max(10, rows+step))
	}
	m.cfg.SessionCols, m.cfg.SessionRows = cols, rows
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
	}
	return m, nil
}

func (m tuiModel) sectionContent() []string {
	var out []string

	switch m.setSection {
	case secBatch:
		dest := ui.StyleDim.Render("stdout")
		if m.cfg.BatchToFile {
			dest = ui.StyleElement.Render("file")
		}
		folder := ui.StyleWarn.Render("not set")
		if m.cfg.BatchDir != "" {
			folder = ui.StyleDim.Render(ui.Truncate(m.cfg.BatchDir, 44))
		}
		// The row stays whether the format sorts rows or not: a section that
		// gains and loses a row as the format cycles moves everything under it.
		order := ui.StyleElement.Render(output.OrientationLabel(m.batchOrient())) +
			ui.StyleDim.Render("  "+output.OrientationHelp(m.batchOrient()))
		if !output.Tabular(m.batchFormat()) {
			order = ui.StyleDim.Render(output.OrientationLabel(m.batchOrient()) +
				"  only table and csv have rows to sort")
		}
		out = append(out,
			ui.Label("format")+ui.StyleElement.Render(m.batchFormat())+
				ui.StyleDim.Render("  "+output.FormatHelp(m.batchFormat())),
			ui.Label("rows")+order,
			ui.Label("output")+dest,
			ui.Label("folder")+folder,
		)
		if m.cfg.BatchToFile && m.cfg.BatchDir == "" {
			out = append(out, "", ui.StyleWarn.Render("set a folder, or output falls back to stdout"))
		}

	case secGeneral:
		guard := ui.StyleOK.Render("read-only enforced")
		if m.cfg.AllowWrites {
			guard = ui.StyleWarn.Render("writes allowed")
		}
		out = append(out,
			ui.Label("commands")+guard,
			ui.Label("mode")+ui.StyleElement.Render(m.defaultMode().String())+
				ui.StyleDim.Render("  what the element list opens in"),
			ui.Label("jump")+ui.StyleElement.Render(fmt.Sprintf("%d rows", m.pageStep()))+
				ui.StyleDim.Render("  how far alt+↑/↓ moves in the element list"),
			ui.Label("times")+m.zoneLine(),
			ui.Label("elements")+m.cacheLine(),
			ui.Label("views")+m.viewsLine(),
			"",
			ui.Label("secrets")+m.secretsLine(),
		)
		if config.Plaintext(m.store.Secrets) {
			if fp, ok := m.store.Secrets.(interface{ Path() string }); ok {
				out = append(out, ui.Label("")+ui.StyleDim.Render(shortenPath(fp.Path(), 52)))
			}
			out = append(out, ui.Label("")+ui.StyleWarn.Render("ION passwords are readable by anything that can read that file"))
		}

	case secTheme:
		out = append(out, m.themeContent()...)

	case secTenants:
		if len(m.cfg.Tenants) == 0 {
			out = append(out, ui.StyleDim.Render("no tenants — add one from the main menu"))
		}
		for _, t := range m.cfg.Tenants {
			tag := ""
			if t.Name == m.tenant.Name {
				tag = ui.StyleOK.Render("  (current)")
			}
			if t.Auth != config.AuthBrowser {
				tag += ui.StyleDim.Render("  (service account)")
			}
			box := "[ ] "
			if t.Added {
				box = ui.StyleOK.Render("[x] ")
			}
			// A tenant that keeps failing is worth flagging here, since this is
			// where it can be taken out of the set.
			if why, bad := m.tenantIssues[t.TSGID]; bad {
				tag += ui.StyleWarn.Render("  " + why)
			}
			out = append(out, box+fmt.Sprintf("%-24s", ui.Truncate(t.Name, 24))+
				ui.StyleDim.Render(t.TSGID)+tag)
		}

	case secUsers:
		if !m.hasTenant {
			out = append(out, ui.StyleDim.Render("no tenant selected"))
			break
		}
		if len(m.tenant.Users) == 0 {
			out = append(out, ui.StyleDim.Render("no ion users — press a to add one"))
		}
		for _, u := range m.tenant.Users {
			tags := ""
			if u.Default {
				tags += ui.StyleDim.Render("  (default)")
			}
			if u.Name == m.ionUser {
				tags += ui.StyleOK.Render("  (active)")
			}
			stored := ui.StyleWarn.Render("  no password")
			if v, err := m.store.Secrets.Get(config.IONPassKey(m.tenant.TSGID, u.Name)); err == nil && v != "" {
				stored = ui.StyleDim.Render("  saved")
			}
			out = append(out, ui.StyleElement.Render(u.Name)+tags+stored)
		}

	case secSession:
		cols, rows := m.sessionGeometry()
		warn := ui.StyleWarn.Render("  do not change unless you know what you are doing!")
		out = append(out,
			ui.Label("buffer cols")+fmt.Sprintf("%-6d", cols)+warn,
			ui.Label("buffer rows")+fmt.Sprintf("%-6d", rows)+warn,
		)

	case secReset:
		out = append(out, ui.StyleWarn.Render("factory reset"))
		if m.resetArmed {
			out = append(out,
				"",
				ui.StyleErr.Render("press y to delete every tenant, ION password and token"),
				ui.StyleDim.Render("the browser profile goes too, so this signs you out"),
				ui.StyleDim.Render("any other key cancels"),
			)
			break
		}
		out = append(out,
			"",
			ui.StyleDim.Render("deletes saved tenants, ION passwords, browser tokens,"),
			ui.StyleDim.Render("cached elements and the browser profile"),
			ui.StyleDim.Render("nothing here can be undone, and nothing is backed up"),
		)

	case secDebug:
		out = append(out,
			ui.Label("config")+ui.StyleDim.Render(m.store.Root),
			ui.Label("secrets")+ui.StyleDim.Render(m.store.Secrets.Backend()),
			ui.Label("version")+ui.StyleDim.Render(Version),
			ui.Label("stats")+ui.StyleDim.Render("alt+i overlays live counters"),
			ui.Label("colours")+ui.StyleDim.Render(ui.CurrentTheme().Name+"  "+profileLabel()),
			ui.Label("times")+ui.StyleDim.Render(clock.Name()),
			"",
			ui.Label("tenant")+ui.StyleDim.Render(m.tenant.Name+"  tsg "+m.tenant.TSGID),
			ui.Label("ion user")+ui.StyleDim.Render(m.ionUser),
			ui.Label("mode")+ui.StyleDim.Render(m.mode.String()),
			"",
			ui.Label("elements")+ui.StyleDim.Render(fmt.Sprintf("%d cached, %d shown", m.cacheCount, len(m.matches))),
			ui.Label("sessions")+ui.StyleDim.Render(fmt.Sprintf("%d open, %d detached", m.sessions.Count(), m.sessions.DetachedCount())),
			ui.Label("ha")+ui.StyleDim.Render(m.haPath()),
			ui.Label("catalog")+ui.StyleDim.Render(fmt.Sprintf("%d commands, %d offered", len(ioncmd.Embedded()), len(m.catalog()))),
			ui.Label("terminal")+ui.StyleDim.Render(fmt.Sprintf("%d x %d", m.width, m.height)),
		)
		if app.Debug {
			out = append(out, "", ui.StyleWarn.Render("--debug: throwaway config, no saved tenants, secrets held only in memory"))
		}
	}
	return out
}

func (m tuiModel) sectionHint() string {
	if m.setSection == secDebug {
		return "read only · ↑↓ section · esc back"
	}
	if m.setSection == secReset && m.resetArmed {
		return "y reset · any other key cancels"
	}
	if m.setFocus == focusNav {
		return "↑↓ section · enter/→ open · esc back"
	}
	switch m.setSection {
	case secGeneral:
		return "↑↓ row · enter change/refresh/type · space cycle · tab back · esc back"
	case secTheme:
		if m.themeEdit {
			return "↑↓ role · ←→ cycle · enter type · ctrl+d built-in · esc done"
		}
		return "↑↓ theme · enter use it · e edit its colours · tab back · esc back"
	case secBatch:
		return "↑↓ row · enter change · tab back · esc back"
	case secTenants:
		return "↑↓ row · space include · a add · enter edit · s switch · u add user · x forget"
	case secUsers:
		return "↑↓ row · enter edit · r run as · a add · p password · d default · x remove · tab back"
	case secSession:
		return "↑↓ row · enter type · -/+ adjust · tab back"
	case secDebug:
		return "read only · tab back · esc back"
	case secReset:
		if m.resetArmed {
			return "y reset · any other key cancels"
		}
		return "enter arm the reset · tab back · esc back"
	}
	return "tab back · esc back"
}

func (m tuiModel) viewSettings() string {
	navWidth := 12
	contentWidth := max(20, m.width-navWidth-4)
	bodyHeight := max(6, m.height-6)

	var nav strings.Builder
	for i, name := range setSections {
		cursor := "  "
		label := name
		if i == m.setSection {
			if m.setFocus == focusNav {
				cursor = ui.StyleCmd.Render("> ")
			} else {
				cursor = ui.StyleDim.Render("· ")
			}
			label = ui.StyleElement.Render(name)
		}
		nav.WriteString(cursor + label + "\n")
	}

	rows := m.sectionContent()
	var content strings.Builder
	for i, r := range rows {
		cursor := "  "
		if m.setFocus == focusContent && i == m.setCursor && i < m.sectionRows() {
			cursor = ui.StyleCmd.Render("> ")
		}
		content.WriteString(cursor + ui.Truncate(r, contentWidth) + "\n")
	}

	navPane := lipgloss.NewStyle().Width(navWidth).Height(bodyHeight).Render(nav.String())
	divider := ui.StyleDivider.Height(bodyHeight).
		Render(strings.TrimRight(strings.Repeat("│\n", bodyHeight), "\n"))
	contentPane := lipgloss.NewStyle().Width(contentWidth).Height(bodyHeight).Render(content.String())

	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render("settings") + "\n\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, navPane, divider+" ", contentPane) + "\n")

	body := strings.TrimRight(b.String(), "\n")
	used := len(strings.Split(body, "\n"))
	for i := used; i < m.height-2; i++ {
		body += "\n"
	}

	return body + "\n" + ui.Footer(m.status, m.sectionHint())
}

// batchFormat is the saved format, normalized so an unset or stale value still
// displays as something real.
func (m tuiModel) batchFormat() string {
	f, err := output.NormalizeFormat(m.cfg.BatchFormat)
	if err != nil {
		return output.FormatJSON
	}
	return f
}

// batchOrient is the saved row orientation, normalized the same way as the
// format so a stale value still displays as something real.
func (m tuiModel) batchOrient() string {
	o, err := output.NormalizeOrientation(m.cfg.BatchOrient)
	if err != nil {
		return output.OrientION
	}
	return o
}

func (m tuiModel) onBatchKey(key string) (tea.Model, tea.Cmd) {
	switch m.setCursor {
	case 0:
		if key != "enter" && key != " " && key != "space" {
			return m, nil
		}
		m.cfg.BatchFormat = output.NextFormat(m.batchFormat())
	case 1:
		if key != "enter" && key != " " && key != "space" {
			return m, nil
		}
		m.cfg.BatchOrient = output.NextOrientation(m.batchOrient())
	case 2:
		if key != "enter" && key != " " && key != "space" {
			return m, nil
		}
		m.cfg.BatchToFile = !m.cfg.BatchToFile
	case 3:
		if key != "enter" {
			return m, nil
		}
		return m.openBatchDirForm()
	default:
		return m, nil
	}

	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
	}
	return m, nil
}

func (m tuiModel) openBatchDirForm() (tea.Model, tea.Cmd) {
	dir := newField("folder", "~/ion-reports", false)
	dir.input.SetValue(m.cfg.BatchDir)
	return m.openForm(formBatchDir, "Folder for batch output", phaseAddTenant, []formField{dir})
}

// secretsLine names the secret store without softening a plaintext fallback.
func (m tuiModel) secretsLine() string {
	if config.Plaintext(m.store.Secrets) {
		return ui.StyleWarn.Render(config.SecretsWarning(m.store.Secrets))
	}
	return ui.StyleOK.Render(config.SecretsWarning(m.store.Secrets))
}

// shortenPath drops the front of a path rather than the end, since the file
// name is the part worth reading.
func shortenPath(p string, n int) string {
	r := []rune(p)
	if n <= 1 || len(r) <= n {
		return p
	}
	return "…" + string(r[len(r)-(n-1):])
}

// pageSteps is what the setting cycles through. Bounded at both ends: one row
// is what the arrows already do, and a step past a screen is what pgdn is.
var pageSteps = []int{5, 10, 20, 25, 50}

// DefaultPageStep is how far alt+up and alt+down move with nothing saved.
const DefaultPageStep = 10

func (m tuiModel) pageStep() int {
	if m.cfg != nil && m.cfg.PageStep > 0 {
		return m.cfg.PageStep
	}
	return DefaultPageStep
}

func (m tuiModel) cyclePageStep() (tea.Model, tea.Cmd) {
	next := pageSteps[0]
	for i, n := range pageSteps {
		if n == m.pageStep() {
			next = pageSteps[(i+1)%len(pageSteps)]
			break
		}
	}
	m.cfg.PageStep = next
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
	}
	return m, nil
}

// viewsLine says what rich-data mode has to show and where a view of one's own
// would go. A configurable feature nobody can find the file for is not
// configurable.
func (m tuiModel) viewsLine() string {
	n := len(m.viewNames())
	line := ui.StyleElement.Render(fmt.Sprintf("%d", n)) + ui.StyleDim.Render(" for rich-data mode")
	if m.viewsErr != "" {
		return line + "  " + ui.StyleErr.Render(ui.Truncate(m.viewsErr, 46))
	}
	path := ViewsPath(m.store)
	if path == "" {
		return line
	}
	if _, err := os.Stat(path); err != nil {
		return line + ui.StyleDim.Render("  add "+shortenPath(path, 40))
	}
	return line + ui.StyleDim.Render("  from "+shortenPath(path, 40))
}

// defaultMode is the saved mode the element list opens in.
func (m tuiModel) defaultMode() pickMode {
	if mode, ok := parseMode(m.cfg.DefaultMode); ok {
		return mode
	}
	return modeShell
}

func (m tuiModel) cycleDefaultMode() (tea.Model, tea.Cmd) {
	next := m.defaultMode().next()
	m.cfg.DefaultMode = next.String()
	// The change applies now as well as next launch, which is what pressing it
	// on this screen looks like it should do.
	m.mode = next
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
	}
	return m, nil
}
