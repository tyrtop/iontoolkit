package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ioncmd"
)

const (
	focusNav = iota
	focusContent
)

var setSections = []string{"General", "Batch", "Tenants", "Users", "Session", "Debug"}

func label(s string) string { return styleDim.Render(fmt.Sprintf("%-13s", s)) }

const (
	secGeneral = iota
	secBatch
	secTenants
	secUsers
	secSession
	secDebug
)

func (m tuiModel) sectionRows() int {
	switch m.setSection {
	case secGeneral:
		return 2
	case secBatch:
		return 3
	case secTenants:
		return len(m.cfg.Tenants)
	case secUsers:
		return len(m.tenant.Users)
	case secSession:
		return 2
	case secDebug:
		return 0
	}
	return 0
}

func (m tuiModel) onSettingsKey(key string) (tea.Model, tea.Cmd) {
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
	case secBatch:
		return m.onBatchKey(key)
	case secTenants:
		return m.onTenantsKey(key)
	case secUsers:
		return m.onUsersSectionKey(key)
	case secSession:
		return m.onSessionGeoKey(key)
	}
	return m, nil
}

func (m tuiModel) onGeneralKey(key string) (tea.Model, tea.Cmd) {
	if key != "enter" && key != " " && key != "space" {
		return m, nil
	}
	switch m.setCursor {
	case 0:
		next := nextSpinnerName(m.spinnerName)
		m.cfg.Spinner = next
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		return m.withSpinner(next)
	case 1:
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
		m.cacheState, m.cacheCount, m.cacheAge = cacheNone, 0, 0
		m.status = "switched to " + t.Name
	case "u":
		return m.openUserFormFor(t.Name)
	case "x":
		if err := forgetTenant(m.store, m.cfg, t); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.tenant.Name == t.Name {
			m.tenant, m.hasTenant = resolveTenant(m.cfg, "")
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
			return m.openUserFormFor(m.tenant.Name)
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
		dest := styleDim.Render("stdout")
		if m.cfg.BatchToFile {
			dest = styleElement.Render("file")
		}
		folder := styleWarn.Render("not set")
		if m.cfg.BatchDir != "" {
			folder = styleDim.Render(truncate(m.cfg.BatchDir, 44))
		}
		out = append(out,
			label("format")+styleElement.Render(m.batchFormat())+
				styleDim.Render("  "+formatHelp(m.batchFormat())),
			label("output")+dest,
			label("folder")+folder,
		)
		if m.cfg.BatchToFile && m.cfg.BatchDir == "" {
			out = append(out, "", styleWarn.Render("set a folder, or output falls back to stdout"))
		}

	case secGeneral:
		guard := styleOK.Render("read-only enforced")
		if m.cfg.AllowWrites {
			guard = styleWarn.Render("writes allowed")
		}
		out = append(out,
			label("spinner")+m.spinner.View()+" "+styleElement.Render(m.spinnerName),
			label("commands")+guard,
			"",
			label("secrets")+styleDim.Render(m.store.Secrets.Backend()),
		)

	case secTenants:
		if len(m.cfg.Tenants) == 0 {
			out = append(out, styleDim.Render("no tenants — add one from the main menu"))
		}
		for _, t := range m.cfg.Tenants {
			tag := ""
			if t.Name == m.tenant.Name {
				tag = styleOK.Render("  (active)")
			}
			out = append(out, fmt.Sprintf("%-24s", truncate(t.Name, 24))+
				styleDim.Render(t.TSGID)+tag)
		}

	case secUsers:
		if !m.hasTenant {
			out = append(out, styleDim.Render("no tenant selected"))
			break
		}
		if len(m.tenant.Users) == 0 {
			out = append(out, styleDim.Render("no ion users — press a to add one"))
		}
		for _, u := range m.tenant.Users {
			tags := ""
			if u.Default {
				tags += styleDim.Render("  (default)")
			}
			if u.Name == m.ionUser {
				tags += styleOK.Render("  (active)")
			}
			stored := styleWarn.Render("  no password")
			if v, err := m.store.Secrets.Get(config.IONPassKey(m.tenant.TSGID, u.Name)); err == nil && v != "" {
				stored = styleDim.Render("  saved")
			}
			out = append(out, styleElement.Render(u.Name)+tags+stored)
		}

	case secSession:
		cols, rows := m.sessionGeometry()
		warn := styleWarn.Render("  do not change unless you know what you are doing!")
		out = append(out,
			label("buffer cols")+fmt.Sprintf("%-6d", cols)+warn,
			label("buffer rows")+fmt.Sprintf("%-6d", rows)+warn,
		)

	case secDebug:
		out = append(out,
			label("config")+styleDim.Render(m.store.Root),
			label("secrets")+styleDim.Render(m.store.Secrets.Backend()),
			label("version")+styleDim.Render(buildVersion()),
			"",
			label("tenant")+styleDim.Render(m.tenant.Name+"  tsg "+m.tenant.TSGID),
			label("ion user")+styleDim.Render(m.ionUser),
			label("mode")+styleDim.Render(m.mode.String()),
			"",
			label("elements")+styleDim.Render(fmt.Sprintf("%d cached, %d shown", m.cacheCount, len(m.matches))),
			label("sessions")+styleDim.Render(fmt.Sprintf("%d open, %d detached", m.sessions.Count(), m.sessions.DetachedCount())),
			label("catalog")+styleDim.Render(fmt.Sprintf("%d commands, %d offered", len(ioncmd.Embedded()), len(m.catalog()))),
			label("terminal")+styleDim.Render(fmt.Sprintf("%d x %d", m.width, m.height)),
		)
	}
	return out
}

func (m tuiModel) sectionHint() string {
	if m.setSection == secDebug {
		return "read only · ↑↓ section · esc back"
	}
	if m.setFocus == focusNav {
		return "↑↓ section · enter/→ open · esc back"
	}
	switch m.setSection {
	case secGeneral:
		return "↑↓ row · enter change · tab back · esc back"
	case secBatch:
		return "↑↓ row · enter change · tab back · esc back"
	case secTenants:
		return "↑↓ row · enter edit · s switch · u add user · x forget · tab back"
	case secUsers:
		return "↑↓ row · enter edit · r run as · a add · p password · d default · x remove · tab back"
	case secSession:
		return "↑↓ row · enter type · -/+ adjust · tab back"
	case secDebug:
		return "read only · tab back · esc back"
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
				cursor = styleCmd.Render("> ")
			} else {
				cursor = styleDim.Render("· ")
			}
			label = styleElement.Render(name)
		}
		nav.WriteString(cursor + label + "\n")
	}

	rows := m.sectionContent()
	var content strings.Builder
	for i, r := range rows {
		cursor := "  "
		if m.setFocus == focusContent && i == m.setCursor && i < m.sectionRows() {
			cursor = styleCmd.Render("> ")
		}
		content.WriteString(cursor + truncate(r, contentWidth) + "\n")
	}

	navPane := lipgloss.NewStyle().Width(navWidth).Height(bodyHeight).Render(nav.String())
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Height(bodyHeight).
		Render(strings.TrimRight(strings.Repeat("│\n", bodyHeight), "\n"))
	contentPane := lipgloss.NewStyle().Width(contentWidth).Height(bodyHeight).Render(content.String())

	var b strings.Builder
	b.WriteString(styleCmd.Render("settings") + "\n\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, navPane, divider+" ", contentPane) + "\n")

	body := strings.TrimRight(b.String(), "\n")
	used := len(strings.Split(body, "\n"))
	for i := used; i < m.height-2; i++ {
		body += "\n"
	}

	return body + "\n" + footer(m.status, m.sectionHint())
}

// batchFormat is the saved format, normalized so an unset or stale value still
// displays as something real.
func (m tuiModel) batchFormat() string {
	f, err := normalizeFormat(m.cfg.BatchFormat)
	if err != nil {
		return formatJSON
	}
	return f
}

func (m tuiModel) onBatchKey(key string) (tea.Model, tea.Cmd) {
	switch m.setCursor {
	case 0:
		if key != "enter" && key != " " && key != "space" {
			return m, nil
		}
		m.cfg.BatchFormat = nextFormat(m.batchFormat())
	case 1:
		if key != "enter" && key != " " && key != "space" {
			return m, nil
		}
		m.cfg.BatchToFile = !m.cfg.BatchToFile
	case 2:
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
