package main

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
)

type menuAction int

const (
	actShell menuAction = iota
	actBatch
	actSwitch
	actAddTenant
	actSettings
	actQuit
)

type menuEntry struct {
	label  string
	action menuAction
}

func (m tuiModel) menuEntries() []menuEntry {
	var e []menuEntry
	if m.cfg == nil {
		return []menuEntry{{"Quit", actQuit}}
	}
	if m.hasTenant {
		e = append(e,
			menuEntry{"start ION shell for " + m.tenant.Name, actShell},
			menuEntry{"send batch commands for " + m.tenant.Name, actBatch},
		)
	}
	if len(m.cfg.Tenants) > 1 {
		e = append(e, menuEntry{"switch tenant", actSwitch})
	}
	e = append(e,
		menuEntry{"Add Tenant", actAddTenant},
		menuEntry{"Settings", actSettings},
		menuEntry{"Quit", actQuit},
	)
	return e
}

func (m tuiModel) cacheLine() string {
	if !m.hasTenant {
		return styleDim.Render("no tenant configured")
	}
	switch m.cacheState {
	case cacheUpdating:
		return styleWarn.Render(fmt.Sprintf("%d elements cached (updating)", m.cacheCount))
	case cacheUpdated:
		if m.cacheAge > 0 {
			return styleOK.Render(fmt.Sprintf("%d elements cached (updated)", m.cacheCount)) +
				styleDim.Render("  "+short(m.cacheAge)+" ago")
		}
		return styleOK.Render(fmt.Sprintf("%d elements cached (updated)", m.cacheCount))
	case cacheFailed:
		return styleErr.Render("element refresh failed")
	default:
		return styleDim.Render("no cached elements")
	}
}

func (m tuiModel) onMenuKey(key string) (tea.Model, tea.Cmd) {
	entries := m.menuEntries()

	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.menuCursor = max(0, m.menuCursor-1)
		return m, nil
	case "down", "j":
		m.menuCursor = min(len(entries)-1, m.menuCursor+1)
		return m, nil
	case "enter":
		if m.menuCursor >= len(entries) {
			return m, nil
		}
		switch entries[m.menuCursor].action {
		case actShell:
			m.mode = modeShell
			return m.startContinue()
		case actBatch:
			m.mode = modeBatch
			return m.startContinue()
		case actSwitch:
			m.switchCursor = 0
			m.phase = phaseSwitch
			return m, nil
		case actAddTenant:
			return m.openTenantForm()
		case actSettings:
			m.setCursor = 0
			m.phase = phaseSettings
			return m, m.spinner.Tick
		case actQuit:
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m tuiModel) startContinue() (tea.Model, tea.Cmd) {
	m.status = ""
	m.cacheState = cacheUpdating

	if c, err := m.store.Cache(m.tenant.TSGID); err == nil && c != nil && len(c.Elements) > 0 {
		m.setElements(c.Elements)
		m.cacheCount = len(c.Elements)
		m.cacheAge = c.Age()
		m.phase = phasePicking
	} else {
		m.phase = phaseLoading
	}

	f := tenantFlags(m.store, m.tenant, m.ionUser)
	if err := f.validateCreds(); err != nil {
		m.status = err.Error()
		m.cacheState = cacheFailed
		m.phase = phaseMenu
		return m, nil
	}
	m.flags = f
	if f.IONUsername != "" {
		m.ionUser = f.IONUsername
	}

	return m, tea.Batch(m.spinner.Tick, connect(m.ctx, f))
}

func (m tuiModel) onSwitchKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.phase = phaseMenu
		return m, nil
	case "up", "k":
		m.switchCursor = max(0, m.switchCursor-1)
	case "down", "j":
		m.switchCursor = min(len(m.cfg.Tenants)-1, m.switchCursor+1)
	case "enter":
		if m.switchCursor < len(m.cfg.Tenants) {
			m.tenant = m.cfg.Tenants[m.switchCursor]
			m.hasTenant = true
			m.ionUser = m.tenant.DefaultUser()
			m.cfg.LastTenant = m.tenant.Name
			if err := m.store.SaveConfig(m.cfg); err != nil {
				m.status = err.Error()
			}
			m.all, m.matches, m.selected = nil, nil, map[string]bool{}
			m.cacheState, m.cacheCount, m.cacheAge = cacheNone, 0, 0
			if c, err := m.store.Cache(m.tenant.TSGID); err == nil && c != nil {
				m.cacheCount = len(c.Elements)
				m.cacheAge = c.Age()
				m.cacheState = cacheUpdated
			}
			m.menuCursor = 0
			m.phase = phaseMenu
		}
	}
	return m, nil
}

func newField(label, placeholder string, masked bool) formField {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	if masked {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
	}
	in.SetWidth(48)
	return formField{label: label, input: in}
}

func (m tuiModel) openForm(kind formKind, title string, phase tuiPhase, fields []formField) (tea.Model, tea.Cmd) {
	m.form = fields
	m.formCursor = 0
	m.formKind = kind
	m.formTitle = title
	m.formReturn = m.phase
	if m.phase == phaseUsers {
		m.formReturn = phasePicking
	}
	m.status = ""
	m.phase = phase
	return m, m.form[0].input.Focus()
}

func (m tuiModel) openTenantForm() (tea.Model, tea.Cmd) {
	return m.openForm(formTenant, "Add Tenant", phaseAddTenant, []formField{
		newField("name", "Acme Co", false),
		newField("tsg id", "1234567890", false),
		newField("client id", "svc@1234.iam.panserviceaccount.com", false),
		newField("client secret", "", true),
	})
}

func (m tuiModel) openUserFormFor(name string) (tea.Model, tea.Cmd) {
	m.formTenantName = name
	return m.openForm(formUser, "Add ION user to "+name, phaseAddUser, []formField{
		newField("ion user", "cisops", false),
		newField("ion password", "", true),
	})
}

func (m tuiModel) openEditPasswordForm(user string) (tea.Model, tea.Cmd) {
	m.formTenantName = m.tenant.Name
	m.formUserName = user
	return m.openForm(formEditPass, "New ION password for "+user, phaseAddUser, []formField{
		newField("ion password", "", true),
	})
}

func (m tuiModel) openPasswordForm() (tea.Model, tea.Cmd) {
	if m.ionUser == "" {
		return m.openForm(formPassword, "ION credentials for "+m.tenant.Name, phasePassword, []formField{
			newField("ion user", "cisops", false),
			newField("ion password", "", true),
		})
	}
	return m.openForm(formPassword, "ION password for "+m.ionUser, phasePassword, []formField{
		newField("ion password", "", true),
	})
}

func (m tuiModel) onFormKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.form = nil
		m.phase = m.formReturn
		if m.formKind == formPassword {
			m.phase = phasePicking
		}
		m.formKind = formNone
		return m, nil

	case "tab", "down":
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor + 1) % len(m.form)
		return m, m.form[m.formCursor].input.Focus()

	case "shift+tab", "up":
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor - 1 + len(m.form)) % len(m.form)
		return m, m.form[m.formCursor].input.Focus()

	case "enter":
		return m.submitForm()
	}

	var cmd tea.Cmd
	m.form[m.formCursor].input, cmd = m.form[m.formCursor].input.Update(msg)
	return m, cmd
}

func (m tuiModel) formValue(i int) string {
	return strings.TrimSpace(m.form[i].input.Value())
}

func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	switch m.formKind {

	case formTenant:
		name, tsg := m.formValue(0), m.formValue(1)
		if name == "" || tsg == "" {
			m.status = "name and tsg id are required"
			return m, nil
		}
		t := config.Tenant{Name: name, TSGID: tsg, ClientID: m.formValue(2)}
		m.cfg.Upsert(t)
		m.cfg.LastTenant = name
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if secret := m.formValue(3); secret != "" {
			if err := m.store.Secrets.Set(config.ClientSecretKey(tsg), secret); err != nil {
				m.status = err.Error()
				return m, nil
			}
		}
		m.tenant, m.hasTenant = t, true
		m.ionUser = ""
		m.all, m.matches, m.selected = nil, nil, map[string]bool{}
		m.cacheState, m.cacheCount, m.cacheAge = cacheNone, 0, 0
		m.form, m.formKind = nil, formNone
		m.menuCursor = 0
		m.phase = phaseMenu
		return m, nil

	case formUser:
		user, pass := m.formValue(0), m.formValue(1)
		if user == "" {
			m.status = "ion user is required"
			return m, nil
		}
		t, ok := m.cfg.Tenant(m.formTenantName)
		if !ok {
			m.status = "tenant " + m.formTenantName + " is gone"
			return m, nil
		}
		t.AddUser(user, t.DefaultUser() == "")
		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if pass != "" {
			if err := m.store.Secrets.Set(config.IONPassKey(t.TSGID, user), pass); err != nil {
				m.status = err.Error()
				return m, nil
			}
		}
		if m.tenant.Name == t.Name {
			m.tenant = t
			if m.ionUser == "" {
				m.ionUser = t.DefaultUser()
			}
		}
		m.form, m.formKind = nil, formNone
		m.phase = m.formReturn
		return m, nil

	case formEditTenant:
		name, tsg := m.formValue(0), m.formValue(1)
		if name == "" || tsg == "" {
			m.status = "name and tsg id are required"
			return m, nil
		}
		old, ok := m.cfg.Tenant(m.formTenantName)
		if !ok {
			m.status = "tenant " + m.formTenantName + " is gone"
			return m, nil
		}

		updated := old
		updated.Name, updated.TSGID, updated.ClientID = name, tsg, m.formValue(2)

		if tsg != m.formOldTSG {
			if v, err := m.store.Secrets.Get(config.ClientSecretKey(m.formOldTSG)); err == nil && v != "" {
				_ = m.store.Secrets.Set(config.ClientSecretKey(tsg), v)
				_ = m.store.Secrets.Delete(config.ClientSecretKey(m.formOldTSG))
			}
			for _, u := range old.Users {
				if v, err := m.store.Secrets.Get(config.IONPassKey(m.formOldTSG, u.Name)); err == nil && v != "" {
					_ = m.store.Secrets.Set(config.IONPassKey(tsg, u.Name), v)
					_ = m.store.Secrets.Delete(config.IONPassKey(m.formOldTSG, u.Name))
				}
			}
			_ = m.store.DropCache(m.formOldTSG)
		}
		if secret := m.formValue(3); secret != "" {
			if err := m.store.Secrets.Set(config.ClientSecretKey(tsg), secret); err != nil {
				m.status = err.Error()
				return m, nil
			}
		}

		wasLast := m.cfg.LastTenant == m.formTenantName
		if name != m.formTenantName {
			m.cfg.Remove(m.formTenantName)
		}
		m.cfg.Upsert(updated)
		if wasLast {
			m.cfg.LastTenant = name
		}
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.tenant.Name == m.formTenantName {
			m.tenant = updated
		}
		m.form, m.formKind = nil, formNone
		m.status = "saved " + name
		m.phase = m.formReturn
		return m, nil

	case formEditUser:
		name := m.formValue(0)
		if name == "" {
			m.status = "ion user is required"
			return m, nil
		}
		t, ok := m.cfg.Tenant(m.formTenantName)
		if !ok {
			m.status = "tenant " + m.formTenantName + " is gone"
			return m, nil
		}

		wasDefault := t.DefaultUser() == m.formUserName
		if name != m.formUserName {
			m.moveUserSecret(t.TSGID, m.formUserName, name)
			t.RemoveUser(m.formUserName)
			t.AddUser(name, wasDefault)
		}
		if pass := m.formValue(1); pass != "" {
			if err := m.store.Secrets.Set(config.IONPassKey(t.TSGID, name), pass); err != nil {
				m.status = err.Error()
				return m, nil
			}
		}

		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.tenant.Name == t.Name {
			m.tenant = t
		}
		if m.ionUser == m.formUserName {
			m.ionUser = name
		}
		m.form, m.formKind = nil, formNone
		m.status = "saved " + name
		m.phase = m.formReturn
		return m, nil

	case formGeometry:
		n, err := strconv.Atoi(m.formValue(0))
		if err != nil {
			m.status = "enter a number"
			return m, nil
		}
		cols, rows := m.sessionGeometry()
		if m.formGeoRow == 0 {
			cols = min(400, max(40, n))
		} else {
			rows = min(200, max(10, n))
		}
		m.cfg.SessionCols, m.cfg.SessionRows = cols, rows
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.status = fmt.Sprintf("buffer is now %dx%d", cols, rows)
		m.phase = m.formReturn
		return m, nil

	case formEditPass:
		pass := m.formValue(0)
		if pass == "" {
			m.status = "password is required"
			return m, nil
		}
		t, ok := m.cfg.Tenant(m.formTenantName)
		if !ok {
			m.status = "tenant " + m.formTenantName + " is gone"
			return m, nil
		}
		if err := m.store.Secrets.Set(config.IONPassKey(t.TSGID, m.formUserName), pass); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.status = "password updated for " + m.formUserName
		m.phase = m.formReturn
		return m, nil

	case formPassword:
		user, pass := m.ionUser, m.formValue(0)
		if len(m.form) == 2 {
			user, pass = m.formValue(0), m.formValue(1)
		}
		if user == "" || pass == "" {
			m.status = "user and password are required"
			return m, nil
		}
		if err := m.store.Secrets.Set(config.IONPassKey(m.tenant.TSGID, user), pass); err != nil {
			m.status = err.Error()
			return m, nil
		}
		t := m.tenant
		t.AddUser(user, t.DefaultUser() == "")
		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.tenant, m.ionUser = t, user
		m.form, m.formKind = nil, formNone
		m.status = ""
		m.phase = phasePicking
		return m.onPickEnter()
	}

	return m, nil
}

func (m tuiModel) viewMenu() string {
	var b strings.Builder

	b.WriteString(banner(m.width, m.height))
	b.WriteString(m.cacheLine() + "\n\n")

	for i, e := range m.menuEntries() {
		cursor := "  "
		label := e.label
		if i == m.menuCursor {
			cursor = styleCmd.Render("> ")
			label = styleElement.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}

	b.WriteString("\n")
	b.WriteString(footer(m.status, "↑↓ move · enter select · q quit"))
	return b.String()
}

func (m tuiModel) viewSwitch() string {
	var b strings.Builder
	b.WriteString(styleCmd.Render("switch tenant") + "\n\n")

	for i, t := range m.cfg.Tenants {
		cursor := "  "
		if i == m.switchCursor {
			cursor = styleCmd.Render("> ")
		}
		marker := ""
		if t.Name == m.cfg.LastTenant {
			marker = styleOK.Render("  (current)")
		}
		b.WriteString(fmt.Sprintf("%s%-24s %s%s\n",
			cursor, truncate(t.Name, 24), styleDim.Render(t.TSGID), marker))
	}

	b.WriteString("\n" + styleDim.Render("↑↓ move · enter select · esc back") + "\n")
	return b.String()
}

func (m tuiModel) viewForm() string {
	var b strings.Builder
	b.WriteString(styleCmd.Render(m.formTitle) + "\n\n")

	for i, f := range m.form {
		label := fmt.Sprintf("%-16s", f.label)
		if i == m.formCursor {
			label = styleElement.Render(label)
		} else {
			label = styleDim.Render(label)
		}
		b.WriteString("  " + label + f.input.View() + "\n")
	}

	b.WriteString("\n")
	b.WriteString(footer(m.status, "tab next · enter save · esc cancel"))
	return b.String()
}

func (m tuiModel) tenantIndex() int {
	for i, t := range m.cfg.Tenants {
		if t.Name == m.tenant.Name {
			return i
		}
	}
	return -1
}

func (m tuiModel) onUsersKey(key string) (tea.Model, tea.Cmd) {
	users := m.tenant.Users

	switch key {
	case "esc", "q":
		m.phase = phasePicking
		return m, nil
	case "up", "k":
		m.userCursor = max(0, m.userCursor-1)
	case "down", "j":
		m.userCursor = min(max(0, len(users)-1), m.userCursor+1)
	case "a":
		if m.tenantIndex() >= 0 {
			return m.openUserFormFor(m.tenant.Name)
		}
		m.status = "add this tenant from the menu first"
		return m, nil
	case "d":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			t := m.tenant
			t.AddUser(name, true)
			m.cfg.Upsert(t)
			if err := m.store.SaveConfig(m.cfg); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.tenant, m.ionUser = t, name
		}
	case "x":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
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
				m.userCursor = max(0, min(m.userCursor, len(t.Users)-1))
			}
		}
	case "p":
		if m.userCursor < len(users) {
			return m.openEditPasswordForm(users[m.userCursor].Name)
		}
		return m, nil
	case "enter":
		if m.userCursor < len(users) {
			m.ionUser = users[m.userCursor].Name
			m.status = "running as " + m.ionUser
		}
		return m, nil
	}
	return m, nil
}

func (m tuiModel) viewUsers() string {
	var b strings.Builder
	b.WriteString(styleCmd.Render("ion users") +
		styleDim.Render("   "+m.tenant.Name) + "\n\n")

	if len(m.tenant.Users) == 0 {
		b.WriteString(styleDim.Render("  no ion users yet — press a to add one") + "\n")
	}

	for i, u := range m.tenant.Users {
		cursor := "  "
		if i == m.userCursor {
			cursor = styleCmd.Render("> ")
		}
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
		b.WriteString(cursor + styleElement.Render(u.Name) + tags + stored + "\n")
	}

	b.WriteString("\n")
	b.WriteString(footer(m.status, "↑↓ move · enter run as · a add · p password · d default · x remove · esc elements"))
	return b.String()
}

func (m tuiModel) openEditTenantForm(t config.Tenant) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formOldTSG = t.TSGID

	name := newField("name", "", false)
	name.input.SetValue(t.Name)
	tsg := newField("tsg id", "", false)
	tsg.input.SetValue(t.TSGID)
	client := newField("client id", "", false)
	client.input.SetValue(t.ClientID)
	secret := newField("client secret", "leave blank to keep", true)

	return m.openForm(formEditTenant, "Edit "+t.Name, phaseAddTenant,
		[]formField{name, tsg, client, secret})
}

func (m tuiModel) openEditUserForm(user string) (tea.Model, tea.Cmd) {
	m.formTenantName = m.tenant.Name
	m.formUserName = user

	name := newField("ion user", "", false)
	name.input.SetValue(user)
	pass := newField("ion password", "leave blank to keep", true)

	return m.openForm(formEditUser, "Edit "+user, phaseAddUser, []formField{name, pass})
}

func (m tuiModel) openGeometryForm(row, cols, rows int) (tea.Model, tea.Cmd) {
	m.formGeoRow = row

	title, cur := "buffer cols", cols
	if row == 1 {
		title, cur = "buffer rows", rows
	}
	f := newField(title, "", false)
	f.input.SetValue(fmt.Sprintf("%d", cur))

	return m.openForm(formGeometry, "Set "+title, phaseAddUser, []formField{f})
}

func (m tuiModel) moveUserSecret(tsg, from, to string) {
	if from == to {
		return
	}
	if v, err := m.store.Secrets.Get(config.IONPassKey(tsg, from)); err == nil && v != "" {
		_ = m.store.Secrets.Set(config.IONPassKey(tsg, to), v)
		_ = m.store.Secrets.Delete(config.IONPassKey(tsg, from))
	}
}
