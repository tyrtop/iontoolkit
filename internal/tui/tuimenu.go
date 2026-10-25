package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

type menuAction int

const (
	actElements menuAction = iota
	actSignIn
	actRefreshToken
	actSwitch
	actMacros
	actSettings
	actQuit
	actWorkflows
	actCommand
)

type menuEntry struct {
	label   string
	action  menuAction
	command string
	when    *tmpl.T
}

func (m tuiModel) menuEntries() []menuEntry {
	if m.cfg() == nil {
		return []menuEntry{{label: "Quit", action: actQuit}}
	}
	all := m.menuSpec
	if all == nil {
		all, _ = builtinMenu()
	}
	at := map[string]any{"auth": m.runtime().AuthMode(), "signed_in": m.runtime().HasSignIn()}
	var out []menuEntry
	for _, e := range all {
		if e.when == nil || tmpl.Truthy(e.when.Render(at)) {
			out = append(out, e)
		}
	}
	return out
}

func (m tuiModel) cacheLine() string {
	if !m.hasTenant() {
		return ui.StyleDim.Render("no tenant configured")
	}
	switch m.cacheState() {
	case cacheUpdating:
		if m.cacheCount() == 0 {
			return ui.StyleWarn.Render("fetching elements")
		}
		return ui.StyleWarn.Render(fmt.Sprintf("%d elements cached (updating)", m.cacheCount()))
	case cacheFailed:
		return ui.StyleErr.Render("element refresh failed")
	case cacheNone:
		return ui.StyleDim.Render("no cached elements")
	}

	d := m.cacheAgeNow()
	if m.cacheState() == cacheUpdated && d < cacheFresh {
		line := ui.StyleOK.Render(fmt.Sprintf("%d elements cached (updated)", m.cacheCount()))
		if d >= time.Second {
			line += ui.StyleDim.Render("  " + ui.Age(d) + " ago")
		}
		return line
	}
	return ui.StyleDim.Render(fmt.Sprintf("%d elements cached", m.cacheCount())) +
		ui.StyleDim.Render("  "+ui.Age(d)+" ago")
}

func (m tuiModel) cacheAgeNow() time.Duration {
	if m.cacheAt().IsZero() {
		return 0
	}
	return time.Since(m.cacheAt())
}

func (m tuiModel) onMenuKey(key string) (tea.Model, tea.Cmd) {
	entries := m.menuEntries()

	if next, cmd, ran := m.bindingOn(scrMenu, key); ran {
		return next, cmd
	}

	switch key {
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
		e := entries[m.menuCursor]
		if e.action == actCommand {
			return m.execLine(e.command)
		}
		if a, ok := builtinAt(scrMenu, menuActionName(e.action)); ok {
			return a.run(m)
		}
	}
	return m, nil
}

func (m tuiModel) startContinue() (tea.Model, tea.Cmd) {
	m.status = m.columnsErr
	next, cmd := m.loadFleet()
	m = next.(tuiModel)
	if len(m.rows()) > 0 {
		m.phase = phasePicking
	} else {
		m.phase = phaseLoading
	}
	return m, cmd
}

func (m tuiModel) onSwitchKey(key string) (tea.Model, tea.Cmd) {
	armed := m.switchDel
	if key != "x" {
		m.switchDel = ""
	}

	switch key {
	case "esc", "q":
		m.phase = phaseMenu
		return m, nil
	case "up", "k":
		m.switchCursor = max(0, m.switchCursor-1)
	case "down", "j":
		m.switchCursor = min(m.switchRows()-1, m.switchCursor+1)

	case "x":
		return m.armOrForgetAccount(armed)
	case "u":
		return m.undoForgetAccount()
	case "alt+r":
		if m.authMode() == authTenant {
			m.status = "service accounts are added in settings, not discovered"
			return m, nil
		}
		return m.startDiscover()
	case "enter":
		c, ok := m.switchAt(m.switchCursor)
		if !ok {
			if m.authMode() == authTenant {
				return m.openTenantForm(config.AuthServiceAccount)
			}
			return m, nil
		}
		next, err := m.useTenant(c)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m = next
		m.menuCursor = 0
		m.phase = phaseMenu
		if m.cacheState() == cacheUpdating {
			return m.refreshNow()
		}
		return m, nil
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

func (m tuiModel) openTenantForm(auth string) (tea.Model, tea.Cmd) {
	m.formAuth = auth
	fields := []formField{
		newField("name", "Acme Co", false),
		newField("tsg id", "1234567890", false),
	}
	if auth != config.AuthBrowser {
		fields = append(fields,
			newField("client id", "svc@1234.iam.panserviceaccount.com", false),
			newField("client secret", "", true),
		)
	}
	return m.openForm(formTenant, "Add service account", phaseAddTenant, fields)
}

func (m tuiModel) openUserFormFor(t config.Tenant) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	return m.openForm(formUser, "Add ION user to "+t.Name, phaseAddUser, []formField{
		newField("ion user", "", false),
		newField("ion password", "", true),
	})
}

func (m tuiModel) openEditPasswordForm(user string) (tea.Model, tea.Cmd) {
	return m.openEditPasswordFormFor(m.tenant(), user)
}

func (m tuiModel) openEditPasswordFormFor(t config.Tenant, user string) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	m.formUserName = user
	return m.openForm(formEditPass, "New ION password for "+user+" on "+t.Name, phaseAddUser, []formField{
		newField("ion password", "", true),
	})
}

func (m tuiModel) openPasswordForm(t config.Tenant, user string) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	if user == "" {
		return m.openForm(formPassword, "ION credentials for "+t.Name, phasePassword, []formField{
			newField("ion user", "", false),
			newField("ion password", "", true),
		})
	}
	m.formUserName = user
	return m.openForm(formPassword, "ION password for "+user+" on "+t.Name, phasePassword, []formField{
		newField("ion password", "", true),
	})
}

func (m tuiModel) onFormKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.formKind == formMacro && m.formCursor >= 1 {
		switch key {
		case "ctrl+n":
			m.cmdPick = m.cmdPick.move(1)
			return m, nil
		case "ctrl+p":
			m.cmdPick = m.cmdPick.move(-1)
			return m, nil
		case "tab":
			if pick, ok := m.cmdPick.current(); ok {
				return m.insertMacroCommand(pick), nil
			}
		}
	}

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
		if m.formKind == formMacro && m.formCursor == len(m.form)-1 && m.formValue(m.formCursor) != "" {
			return m.addMacroCommand()
		}
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor + 1) % len(m.form)
		return m.syncMacroSuggestions(), m.form[m.formCursor].input.Focus()

	case "shift+tab", "up":
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor - 1 + len(m.form)) % len(m.form)
		return m.syncMacroSuggestions(), m.form[m.formCursor].input.Focus()

	case "enter":
		return m.submitForm()

	case "backspace":
		if m.formKind == formMacro && m.formCursor >= 1 && len(m.form) > 2 && m.formValue(m.formCursor) == "" {
			return m.removeMacroCommand()
		}
	}

	var cmd tea.Cmd
	m.form[m.formCursor].input, cmd = m.form[m.formCursor].input.Update(msg)
	return m.syncMacroSuggestions(), cmd
}

func (m tuiModel) formValue(i int) string {
	if i >= len(m.form) {
		return ""
	}
	return strings.TrimSpace(m.form[i].input.Value())
}

func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	switch m.formKind {
	case formBatchDir:
		if err := m.runtime().Set("batch_dir", m.formValue(0)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.status = ""
		m.form, m.formKind = nil, formNone
		m.phase = m.formReturn
		return m, nil

	case formTenant:
		t := config.Tenant{Name: m.formValue(0), TSGID: m.formValue(1), ClientID: m.formValue(2), Auth: m.formAuth}
		if err := m.runtime().AddTenant(t, m.formValue(3)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.forgetRows()
		m.form, m.formKind = nil, formNone
		m.menuCursor = 0
		m.phase = phaseMenu
		return m, nil

	case formUser:
		if _, err := m.runtime().AddUser(m.formTenantName, m.formTenantTSG, m.formValue(0), m.formValue(1)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.phase = m.formReturn
		return m, nil

	case formEditTenant:
		t, err := m.runtime().EditTenant(m.formTenantName, m.formValue(0), m.formValue(1), m.formValue(2), m.formValue(3))
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.status = "saved " + t.Name
		m.phase = m.formReturn
		return m, nil

	case formEditUser:
		name := m.formValue(0)
		if err := m.runtime().EditUser(m.formTenantName, m.formUserName, name, m.formValue(1)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.status = "saved " + name
		m.phase = m.formReturn
		return m, nil

	case formColor:
		return m.submitColor()

	case formNewTheme:
		return m.submitNewTheme()

	case formFavourite:
		return m.submitFavourite()

	case formMacro:
		return m.submitMacro()

	case formGeometry:
		n, err := strconv.Atoi(m.formValue(0))
		if err != nil {
			m.status = "enter a number"
			return m, nil
		}
		name := "session_cols"
		if m.formGeoRow != 0 {
			name = "session_rows"
		}
		if err := m.runtime().Set(name, strconv.Itoa(n)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		cols, rows := m.sessionGeometry()
		m.form, m.formKind = nil, formNone
		m.status = fmt.Sprintf("buffer is now %dx%d", cols, rows)
		m.phase = m.formReturn
		return m, nil

	case formEditPass:
		if err := m.runtime().SetPassword(m.formTenantName, m.formUserName, m.formValue(0)); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.status = "password updated for " + m.formUserName
		m.phase = m.formReturn
		return m, nil

	case formPassword:
		user, pass := m.formUserName, m.formValue(0)
		if len(m.form) == 2 {
			user, pass = m.formValue(0), m.formValue(1)
		}
		if _, err := m.runtime().SaveLogin(m.formTenantName, m.formTenantTSG, user, pass); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.form, m.formKind = nil, formNone
		m.formUserName = ""
		m.status = ""
		m.phase = phasePicking
		return m.onPickEnter()
	}

	return m, nil
}

func (m tuiModel) viewMenu() string {
	var b strings.Builder

	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(m.cacheLine() + "\n")
	b.WriteString(m.authLine() + "\n\n")

	for i, e := range m.menuEntries() {
		cursor := "  "
		label := e.label
		if i == m.menuCursor {
			cursor = ui.StyleCmd.Render("> ")
			label = ui.StyleRow.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}

	b.WriteString("\n")
	hint := "↑↓ move · enter select · " + m.legend(scrMenu, "service account or browser", "auth-mode") +
		" · " + m.legend(scrMenu, "quit", "quit", "quit-closing-sessions")
	if m.sessions().DetachedCount() > 0 || m.sessions().Count() > 0 {
		hint = "↑↓ move · enter select · " + m.legend(scrMenu, "service account or browser", "auth-mode") +
			" · " + m.legend(scrMenu, "reattach", "reopen-tab") +
			" · " + m.legend(scrMenu, "quit", "quit", "quit-closing-sessions")
	}
	b.WriteString(ui.Footer(m.status, hint))
	return b.String()
}

func (m tuiModel) reopenFromMenu() (tea.Model, tea.Cmd) {
	if len(m.rows()) == 0 {
		m.loadFromCache()
	}
	return m.reopenLastTab()
}

func (m tuiModel) viewSwitch() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))

	choices := m.switchChoices()
	noun := "tenant"
	if m.authMode() == authTenant {
		noun = "service account"
	}
	count := fmt.Sprintf("%d %s", len(choices), noun)
	if len(choices) != 1 {
		count += "s"
	}
	b.WriteString(ui.StyleCmd.Render(count) + "\n")

	if m.authMode() == authTenant {
		b.WriteString(ui.StyleDim.Render("one tenant each") + "\n")
	} else if c, err := m.store().TenantCache(); err == nil && c != nil && c.Account != "" {
		b.WriteString(ui.StyleDim.Render("from "+c.Account+", listed "+ui.Age(c.Age())+" ago") + "\n")
	}
	b.WriteString("\n")

	row := 0
	if m.authMode() == authTenant {
		const newRow = "+ New"
		cursor := "  "
		if m.switchCursor == 0 {
			cursor = ui.StyleCmd.Render("> ")
		}
		b.WriteString(cursor + ui.StyleGhost.Render(newRow) + "\n")
		row = 1
	}

	if len(choices) == 0 && m.authMode() != authTenant {
		b.WriteString("  " + ui.StyleDim.Render("none yet — add an account from the menu") + "\n")
	}
	for i, c := range choices {
		cursor := "  "
		marker := ""
		switch {
		case c.tsg == m.tenant().TSGID && m.hasTenant():
			marker = ui.StyleOK.Render("  (current)")
			if !c.saved {
				marker += ui.StyleDim.Render(" from the environment")
			}
		case !c.known && c.saved:
			marker = ui.StyleDim.Render("  (service account)")
		}
		line := fmt.Sprintf("%-30s %s", ui.Truncate(c.name, 30), c.tsg)
		if i+row == m.switchCursor {
			cursor = ui.StyleCmd.Render("> ")
			line = ui.StyleRow.Render(line)
		} else {
			line = ui.StyleDim.Render(line)
		}
		b.WriteString(cursor + line + marker + "\n")
	}

	b.WriteString("\n")
	hint := "↑↓ move · enter use · alt+r re-list · esc back"
	if m.authMode() == authTenant {
		hint = "↑↓ move · enter use · x forget · u undo · esc back"
	}
	b.WriteString(ui.Footer(m.status, hint))
	return b.String()
}

func (m tuiModel) viewForm() string {
	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render(m.formTitle) + "\n\n")

	for i, f := range m.form {
		label := fmt.Sprintf("%-16s", f.label)
		if i == m.formCursor {
			label = ui.StyleElement.Render(label)
		} else {
			label = ui.StyleDim.Render(label)
		}
		b.WriteString("  " + label + f.input.View() + "\n")
	}

	if m.formKind == formMacro {
		b.WriteString("  " + strings.Repeat(" ", 16) + ui.StyleGhost.Render("+ another command") + "\n")
		b.WriteString("\n" + m.cmdSuggestBlock())
	}

	if w := m.plaintextWarning(); w != "" {
		b.WriteString("\n  " + w + "\n")
	}

	b.WriteString("\n")
	hint := "tab next · enter save · esc cancel"
	if m.formKind == formMacro {
		hint = "tab next/add · backspace on an empty line removes it · enter save · esc cancel"
	}
	b.WriteString(ui.Footer(m.status, hint))
	return b.String()
}

func (m tuiModel) plaintextWarning() string {
	if m.store() == nil || !config.Plaintext(m.store().Secrets) {
		return ""
	}
	masked := false
	for _, f := range m.form {
		if f.input.EchoMode == textinput.EchoPassword {
			masked = true
			break
		}
	}
	if !masked {
		return ""
	}
	return ui.StyleWarn.Render("saved as plaintext: no OS keyring is available")
}

func (m tuiModel) tenantIndex() int {
	for i, t := range m.cfg().Tenants {
		if t.Name == m.tenant().Name {
			return i
		}
	}
	return -1
}

func (m tuiModel) onUsersKey(key string) (tea.Model, tea.Cmd) {
	t := m.usersPanelTenant()
	users := t.Users

	switch key {
	case "esc", "q":
		m.phase = phasePicking
		return m, nil
	case "left", "h":
		return m.cycleUsersTenant(-1), nil
	case "right", "l":
		return m.cycleUsersTenant(1), nil
	case "up", "k":
		m.userCursor = max(0, m.userCursor-1)
	case "down", "j":
		m.userCursor = min(max(0, len(users)-1), m.userCursor+1)
	case "a":
		return m.openUserFormFor(t)
	case "d":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			if err := m.runtime().SetDefaultUser(t.Name, name); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.status = name + " is the default for " + t.Name
		}
	case "x":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			left, err := m.runtime().RemoveUser(t.Name, name)
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.userCursor = max(0, min(m.userCursor, len(left.Users)-1))
		}
	case "p":
		if m.userCursor < len(users) {
			return m.openEditPasswordFormFor(t, users[m.userCursor].Name)
		}
		return m, nil
	case "enter":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			if err := m.runtime().RunAsUser(t.Name, name); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.status = "running as " + name + " on " + t.Name
		}
		return m, nil
	}
	return m, nil
}

func (m tuiModel) viewUsers() string {
	var b strings.Builder
	t := m.usersPanelTenant()

	b.WriteString(ui.StyleCmd.Render("ion users") + ui.StyleDim.Render("   "+t.Name))
	if n := len(m.usersPanelTenants()); n > 1 {
		b.WriteString(ui.StyleDim.Render(fmt.Sprintf("   ←/→ of %d tenants", n)))
	}
	b.WriteString("\n\n")

	if len(t.Users) == 0 {
		b.WriteString(ui.StyleDim.Render("  no ion users for this tenant yet — press a to add one") + "\n")
	}

	runAs := m.runAsFor(t.TSGID)
	for i, u := range t.Users {
		cursor := "  "
		if i == m.userCursor {
			cursor = ui.StyleCmd.Render("> ")
		}
		tags := ""
		if u.Default {
			tags += ui.StyleDim.Render("  (default)")
		}
		if u.Name == runAs {
			tags += ui.StyleOK.Render("  (active)")
		}
		stored := ui.StyleWarn.Render("  no password")
		if m.runtime().HasPassword(t.TSGID, u.Name) {
			stored = ui.StyleDim.Render("  saved")
		}
		b.WriteString(cursor + ui.StyleElement.Render(u.Name) + tags + stored + "\n")
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status,
		"↑↓ move · ←→ tenant · enter run as · a add · p password · d default · x remove · esc elements"))
	return b.String()
}

func (m tuiModel) openEditTenantForm(t config.Tenant) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formOldTSG = t.TSGID

	name := newField("name", "", false)
	name.input.SetValue(t.Name)
	tsg := newField("tsg id", "", false)
	tsg.input.SetValue(t.TSGID)
	m.formAuth = t.Auth
	if t.UsesBrowser() {
		return m.openForm(formEditTenant, "Edit "+t.Name+" (browser sign-in)", phaseAddTenant,
			[]formField{name, tsg})
	}

	client := newField("client id", "", false)
	client.input.SetValue(t.ClientID)
	secret := newField("client secret", "leave blank to keep", true)

	return m.openForm(formEditTenant, "Edit "+t.Name, phaseAddTenant,
		[]formField{name, tsg, client, secret})
}

func (m tuiModel) openEditUserForm(user string) (tea.Model, tea.Cmd) {
	m.formTenantName = m.tenant().Name
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

func (m tuiModel) startSignInAuto() (tea.Model, tea.Cmd) {
	why := m.tenantIssues()[m.tenant().TSGID]
	if !m.runtime().BrowserSignedIn() || why == "" {
		return m.startSignIn()
	}
	logging.L().Info("automatic sign-in refused", "tsg", m.tenant().TSGID, "why", why)
	m.signInResume = false
	m.status = m.tenantName(m.tenant().TSGID) + ": " + why +
		" — already signed in, so another browser sign-in will not help"
	if m.phase == phaseLoading || m.phase == phaseSignIn {
		m.phase = phaseMenu
	}
	return m, nil
}

func (m tuiModel) startSignIn() (tea.Model, tea.Cmd) {
	if !m.hasTenant() {
		m.status = "no tenant to sign in to"
		m.phase = phaseMenu
		return m, nil
	}
	m.status = ""
	m.phase = phaseSignIn
	return m, tea.Batch(m.spinner.Tick, signIn(m.ctx, m.runtime()))
}

func (m tuiModel) viewSignIn() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(fmt.Sprintf("  %s waiting for browser\n\n", m.spinner.View()))
	b.WriteString("  " + ui.StyleDim.Render("finish signing in to "+m.tenant().Name+" in the browser window") + "\n")
	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "esc stop waiting"))
	return b.String()
}

func (m tuiModel) viewSignedIn() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in to "+m.tenant().Name) + "\n\n")

	row := func(k, v string) {
		b.WriteString("  " + ui.Label(k) + v + "\n")
	}

	ident, profile := m.runtime().Identity(), m.runtime().Profile()
	row("account", ui.StyleElement.Render(ident.Display()))
	row("tenant", m.tenant().TSGID)
	if profile.Region != "" {
		row("region", profile.Region)
	}
	if len(profile.Roles) > 0 {
		row("roles", strings.Join(profile.Roles, ", "))
	}
	if ttl := m.runtime().TokenTTL(); ttl > 0 {
		row("api token", ui.StyleDim.Render("renews every "+ui.Age(ttl)))
	}
	if left, ok := ident.SessionLeft(); ok {
		if left <= 0 {
			row("sign-in", ui.StyleErr.Render("expired"))
		} else {
			line := "no browser needed for " + ui.Age(left)
			if left < time.Hour {
				row("sign-in", ui.StyleWarn.Render(line))
			} else {
				row("sign-in", ui.StyleOK.Render(line))
			}
		}
	} else {
		row("sign-in", ui.StyleDim.Render("lifetime not stated by the token"))
	}

	b.WriteString("\n")
	switch m.cacheState() {
	case cacheUpdating:
		b.WriteString("  " + m.spinner.View() + " " + ui.StyleDim.Render("fetching elements") + "\n")
	default:
		b.WriteString("  " + m.cacheLine() + "\n")
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "enter continue · esc back"))
	return b.String()
}

func (m tuiModel) armOrForgetAccount(armed string) (tea.Model, tea.Cmd) {
	if m.authMode() != authTenant {
		return m, nil
	}
	c, ok := m.switchAt(m.switchCursor)
	if !ok {
		return m, nil
	}
	t, saved := m.cfg().Tenant(c.name)
	if !saved {
		m.status = c.name + " has no saved record to forget"
		return m, nil
	}

	if !strings.EqualFold(armed, c.name) {
		m.switchDel = c.name
		m.status = "x again to forget " + c.name + " and its secret"
		return m, nil
	}

	if err := m.runtime().RemoveTenant(t.Name); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.switchCursor = max(0, min(m.switchCursor, m.switchRows()-1))
	m.status = "forgot " + t.Name + " · u puts the record back, not the secret"
	return m, nil
}

func (m tuiModel) undoForgetAccount() (tea.Model, tea.Cmd) {
	t, err := m.runtime().UndoRemoveTenant()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.status = t.Name + " is back · enter its client secret again to use it"
	return m, nil
}
