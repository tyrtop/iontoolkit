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
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

const (
	focusNav = iota
	focusContent
)

const (
	guardRow = iota
	modeRow
	jumpRow
	zoneRow
	refreshRow
	generalRows
)

var generalRowLabels = map[int]string{
	guardRow: "Commands", modeRow: "Mode", jumpRow: "Jump",
	zoneRow: "Times", refreshRow: "Elements",
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
			return 0
		}
		return len(ui.Themes()) + 1
	case secBatch:
		return 4
	case secTenants:
		return len(m.cfg.Tenants)
	case secUsers:
		if !m.hasTenant {
			return 0
		}
		return len(m.tenant.Users) + 1
	case secSession:
		return 2
	case secDebug:
		return 0
	case secReset:
		return 2
	}
	return 0
}

func (m tuiModel) onSettingsKey(key string) (tea.Model, tea.Cmd) {
	armed := m.resetArmed
	m.resetArmed = false

	if key != "x" && key != "ctrl+d" {
		m.themeDel = ""
	}

	if m.setSection == secTheme && (m.themeEdit || m.pick.on || m.tabEdit) {
		return m.onThemeKey(key)
	}

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

func (m tuiModel) onResetKey(key string, armed bool) (tea.Model, tea.Cmd) {
	if !armed {
		if key == "enter" {
			m.resetArmed = true
			m.status = ""
		}
		return m, nil
	}
	if key != "y" {
		m.status = "cancelled"
		return m, nil
	}
	if m.setCursor == themeRestoreRow {
		return m.restoreShippedThemes()
	}
	return m.factoryReset()
}

const (
	factoryResetRow = iota
	themeRestoreRow
)

func (m tuiModel) factoryReset() (tea.Model, tea.Cmd) {
	m.sessions.CloseAll()

	if err := app.RemoveThemes(m.store); err != nil {
		m.status = err.Error()
		return m, nil
	}
	ui.HideThemes(nil)
	if err := ui.LoadThemes(app.ThemesPath(m.store)); err != nil {
		m.status = err.Error()
	}

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
	if m.setCursor == refreshRow {
		return m.refreshNow()
	}
	switch m.setCursor {
	case zoneRow:
		if key == "enter" {
			return m.openZonePick()
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

	if m.setCursor == 0 {
		if key == "enter" {
			if m.tenantIndex() >= 0 {
				return m.openUserFormFor(m.tenant)
			}
			m.status = "no tenant selected"
		}
		return m, nil
	}

	if m.setCursor > len(users) {
		return m, nil
	}
	name := users[m.setCursor-1].Name

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
			m.setCursor = max(0, min(m.setCursor, len(t.Users)))
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
		order := ui.StyleElement.Render(output.OrientationLabel(m.batchOrient())) +
			ui.StyleDim.Render("  "+output.OrientationHelp(m.batchOrient()))
		if !output.Tabular(m.batchFormat()) {
			order = ui.StyleDim.Render(output.OrientationLabel(m.batchOrient()) +
				"  only table and csv have rows to sort")
		}
		out = append(out,
			ui.Label("Format")+ui.StyleElement.Render(m.batchFormat())+
				ui.StyleDim.Render("  "+output.FormatHelp(m.batchFormat())),
			ui.Label("Rows")+order,
			ui.Label("Output")+dest,
			ui.Label("Folder")+folder,
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
			ui.Label("Commands")+guard,
			ui.Label("Mode")+ui.StyleElement.Render(m.defaultMode().String())+
				ui.StyleDim.Render("  what the element list opens in"),
			ui.Label("Jump")+ui.StyleElement.Render(fmt.Sprintf("%d rows", m.pageStep()))+
				ui.StyleDim.Render("  how far alt+↑/↓ moves in the element list"),
			ui.Label("Times")+m.zoneLine(),
			ui.Label("Elements")+m.cacheLine(),
			ui.Label("Views")+m.viewsLine(),
			"",
			ui.Label("Secrets")+m.secretsLine(),
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
		out = append(out, ui.StyleGhost.Render("+ New ION User"))
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
			ui.Label("Buffer Cols")+fmt.Sprintf("%-6d", cols)+warn,
			ui.Label("Buffer Rows")+fmt.Sprintf("%-6d", rows)+warn,
		)

	case secReset:
		out = append(out, ui.StyleWarn.Render("Factory Reset"))
		out = append(out, ui.StyleWarn.Render("Restore Themes")+
			ui.StyleDim.Render("   restores default iontk themes - user themes are untouched"))
		if m.resetArmed {
			what := "delete every tenant, ION password, token and scheme"
			if m.setCursor == themeRestoreRow {
				what = "put the shipped schemes back"
			}
			out = append(out, "",
				ui.StyleErr.Render("press y to "+what),
				ui.StyleDim.Render("any other key cancels"),
			)
		}

	case secDebug:
		out = append(out,
			ui.Label("Config")+ui.StyleDim.Render(m.store.Root),
			ui.Label("Secrets")+ui.StyleDim.Render(m.store.Secrets.Backend()),
			ui.Label("Version")+ui.StyleDim.Render(Version),
			ui.Label("Stats")+ui.StyleDim.Render("alt+i overlays live counters"),
			ui.Label("Colors")+ui.StyleDim.Render(ui.CurrentTheme().Name+"  "+profileLabel()),
			ui.Label("Terminal")+ui.StyleDim.Render(termColorLine()),
			ui.Label("Times")+ui.StyleDim.Render(clock.Name()),
			ui.Label("Log")+logLine(),
			"",
			ui.Label("Tenant")+ui.StyleDim.Render(m.tenant.Name+"  tsg "+m.tenant.TSGID),
			ui.Label("ION User")+ui.StyleDim.Render(m.ionUser),
			ui.Label("Mode")+ui.StyleDim.Render(m.mode.String()),
			"",
			ui.Label("Elements")+ui.StyleDim.Render(fmt.Sprintf("%d cached, %d shown", m.cacheCount, len(m.matches))),
			ui.Label("Sessions")+ui.StyleDim.Render(fmt.Sprintf("%d open, %d detached", m.sessions.Count(), m.sessions.DetachedCount())),
			ui.Label("HA")+ui.StyleDim.Render(m.haPath()),
			ui.Label("Catalog")+ui.StyleDim.Render(fmt.Sprintf("%d commands, %d offered", len(ioncmd.Embedded()), len(m.catalog()))),
			ui.Label("Terminal")+ui.StyleDim.Render(fmt.Sprintf("%d x %d", m.width, m.height)),
		)
		if app.Debug {
			out = append(out, "", ui.StyleWarn.Render("--debug: throwaway config, no saved tenants, secrets held only in memory"))
		}
		if tail := logging.Recent(logTailLines); len(tail) > 0 {
			out = append(out, "")
			for _, line := range tail {
				out = append(out, ui.StyleDim.Render("  "+ui.Truncate(line, max(20, m.width-4))))
			}
		}
	}
	return out
}

const logTailLines = 5

func logLine() string {
	if err := logging.Problem(); err != nil {
		return ui.StyleWarn.Render("nothing written: " + err.Error())
	}
	if !logging.On() {
		return ui.StyleDim.Render("off")
	}
	return ui.StyleDim.Render(logging.Path() + "  " + logging.LevelName() + ", run " + logging.RunID())
}

func (m tuiModel) sectionHint() string {
	if m.setSection == secDebug {
		return "read only · ↑↓ section · esc back"
	}
	if m.setSection == secReset && m.resetArmed {
		if m.setCursor == themeRestoreRow {
			return "y restore the shipped themes · any other key cancels"
		}
		return "y reset · any other key cancels"
	}
	if m.setFocus == focusNav {
		return "↑↓ section · enter/→ open · esc back"
	}
	switch m.setSection {
	case secGeneral:
		return "↑↓ row · enter change/refresh/type · space cycle · tab back · esc back"
	case secTheme:
		if m.pick.on {
			return "↑↓←→ move · +/- brightness · tab disc/chart · t type · enter keep · esc cancel"
		}
		return ""
	case secBatch:
		return "↑↓ row · enter change · tab back · esc back"
	case secTenants:
		return "↑↓ row · space include · a add · enter edit · s switch · u add user · x forget"
	case secUsers:
		return "↑↓ row · enter new/edit · r run as · p password · d default · x remove · tab back"
	case secSession:
		return "↑↓ row · enter type · -/+ adjust · tab back"
	case secDebug:
		return "read only · tab back · esc back"
	case secReset:
		if m.resetArmed {
			if m.setCursor == themeRestoreRow {
				return "y restore the shipped themes · any other key cancels"
			}
			return "y reset · any other key cancels"
		}
		if m.setCursor == themeRestoreRow {
			return "↑↓ row · enter arm the restore · esc back"
		}
		return "↑↓ row · enter arm the reset · esc back"
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
			label = ui.StyleRow.Render(name)
		}
		nav.WriteString(cursor + label + "\n")
	}

	rows := m.sectionContent()
	if len(rows) > bodyHeight {
		rows = rows[:bodyHeight]
	}
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
	b.WriteString(ui.StyleCmd.Render("Settings") + "\n\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, navPane, divider+" ", contentPane) + "\n")

	body := strings.TrimRight(b.String(), "\n")
	used := len(strings.Split(body, "\n"))
	for i := used; i < m.height-2; i++ {
		body += "\n"
	}

	return body + "\n" + ui.Footer(m.status, m.sectionHint())
}

func (m tuiModel) batchFormat() string {
	f, err := output.NormalizeFormat(m.cfg.BatchFormat)
	if err != nil {
		return output.FormatJSON
	}
	return f
}

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

func (m tuiModel) secretsLine() string {
	if config.Plaintext(m.store.Secrets) {
		return ui.StyleWarn.Render(config.SecretsWarning(m.store.Secrets))
	}
	return ui.StyleOK.Render(config.SecretsWarning(m.store.Secrets))
}

func shortenPath(p string, n int) string {
	r := []rune(p)
	if n <= 1 || len(r) <= n {
		return p
	}
	return "…" + string(r[len(r)-(n-1):])
}

var pageSteps = []int{5, 10, 20, 25, 50}

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

func (m tuiModel) defaultMode() pickMode {
	if mode, ok := parseMode(m.cfg.DefaultMode); ok {
		return mode
	}
	return modeShell
}

func (m tuiModel) cycleDefaultMode() (tea.Model, tea.Cmd) {
	next := m.defaultMode().next()
	m.cfg.DefaultMode = next.String()
	m.mode = next
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
	}
	return m, nil
}
