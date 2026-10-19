package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
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

	// One entry for the list, because shell versus batch is a property of the
	// list rather than a different thing to start.
	e = append(e, menuEntry{"elements", actElements})

	// What follows depends on where tenants come from, which is the credential
	// mode and a separate axis from shell versus batch.
	switch {
	case m.authMode == authTenant:
		// Named for what it lists: a service account reaches one tenant each,
		// so this is the saved accounts rather than every tenant.
		e = append(e, menuEntry{"service accounts", actSwitch})
	case m.signedIn():
		e = append(e,
			menuEntry{"browser tenants", actSignIn},
			menuEntry{"refresh browser token", actRefreshToken},
		)
	default:
		e = append(e, menuEntry{"sign in with browser", actSignIn})
	}

	e = append(e,
		menuEntry{"macros", actMacros},
		menuEntry{"Settings", actSettings},
		menuEntry{"Quit", actQuit},
	)
	return e
}

func (m tuiModel) cacheLine() string {
	if !m.hasTenant {
		return ui.StyleDim.Render("no tenant configured")
	}
	switch m.cacheState {
	case cacheUpdating:
		if m.cacheCount == 0 {
			return ui.StyleWarn.Render("fetching elements")
		}
		return ui.StyleWarn.Render(fmt.Sprintf("%d elements cached (updating)", m.cacheCount))
	case cacheFailed:
		return ui.StyleErr.Render("element refresh failed")
	case cacheNone:
		return ui.StyleDim.Render("no cached elements")
	}

	// cacheUpdated and cacheCached differ only in how recent the fetch was, and
	// an "updated" that has since gone stale must stop claiming to be current.
	d := m.cacheAgeNow()
	if m.cacheState == cacheUpdated && d < cacheFresh {
		line := ui.StyleOK.Render(fmt.Sprintf("%d elements cached (updated)", m.cacheCount))
		if d >= time.Second {
			line += ui.StyleDim.Render("  " + ui.Age(d) + " ago")
		}
		return line
	}
	return ui.StyleDim.Render(fmt.Sprintf("%d elements cached", m.cacheCount)) +
		ui.StyleDim.Render("  "+ui.Age(d)+" ago")
}

func (m tuiModel) cacheAgeNow() time.Duration {
	if m.cacheAt.IsZero() {
		return 0
	}
	return time.Since(m.cacheAt)
}

func (m tuiModel) onMenuKey(key string) (tea.Model, tea.Cmd) {
	entries := m.menuEntries()

	switch key {
	case "q":
		return m, tea.Quit
	case "alt+w":
		m.sessions.CloseAll()
		return m, tea.Quit
	case "alt+t":
		return m.reopenFromMenu()
	case "alt+m", "alt+a":
		// The only mode the menu has. Shell versus batch belongs to the element
		// list, and switching it from here changed nothing you could see.
		m.authMode = m.authMode.next()
		m.cfg.AuthMode = m.authMode.String()
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.switchCursor = 0
		m.status = ""
		return m, nil
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
		case actElements:
			// Macro mode is only reachable by picking a macro, so the list
			// opened from here is never in it: it would otherwise still be
			// holding one from earlier in the session.
			if m.mode == modeMacro {
				m.mode = modeShell
				if saved, ok := parseMode(m.cfg.DefaultMode); ok {
					m.mode = saved
				}
			}
			m.armedMacro = ""
			return m.startContinue()
		case actSwitch:
			m.switchCursor = 0
			m.status = ""
			m.phase = phaseSwitch
			return m, nil
		case actRefreshToken:
			return m.startTokenRefresh()
		case actSignIn:
			m.switchCursor = 0
			m.status = ""
			// A list already discovered is shown straight away; r re-lists it.
			// Only an account that has never signed in needs the browser now.
			if len(m.browserTenants()) > 0 {
				m.phase = phaseSwitch
				return m, nil
			}
			return m.startDiscover()
		case actMacros:
			return m.openMacros()
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

	m.loadFromCache()
	if len(m.all) > 0 {
		m.phase = phasePicking
	} else {
		m.phase = phaseLoading
	}

	f := app.TenantFlags(m.store, m.tenant, m.ionUser)
	if err := f.ValidateCreds(); err != nil {
		m.status = err.Error()
		m.cacheState = cacheFailed
		m.phase = phaseMenu
		return m, nil
	}
	m.flags = f
	if f.IONUsername != "" {
		m.ionUser = f.IONUsername
	}

	return m, tea.Batch(m.spinner.Tick, connect(m.ctx, m.store, f))
}

func (m tuiModel) onSwitchKey(key string) (tea.Model, tea.Cmd) {
	choices := m.switchChoices()
	switch key {
	case "esc", "q":
		m.phase = phaseMenu
		return m, nil
	case "up", "k":
		m.switchCursor = max(0, m.switchCursor-1)
	case "down", "j":
		m.switchCursor = min(len(choices)-1, m.switchCursor+1)
	case "alt+r":
		if m.authMode == authTenant {
			// There is nothing to ask: a service account is one tenant, and its
			// record is the only thing that says the tenant exists.
			m.status = "service accounts are added in settings, not discovered"
			return m, nil
		}
		// Re-ask the account what it can reach, in case tenants were added or
		// revoked since the last sign-in.
		return m.startDiscover()
	case "enter":
		if m.switchCursor >= len(choices) {
			return m, nil
		}
		next, err := m.useTenant(choices[m.switchCursor])
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m = next
		m.menuCursor = 0
		m.phase = phaseMenu
		// useTenant decides whether the new tenant owes a fetch; something has
		// to actually start it, or the cache line claims to be updating forever.
		if m.cacheState == cacheUpdating {
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

// openTenantForm adds a service account. A browser account is never typed in:
// signing in reveals its tenants, so this form exists only for the credential
// that cannot be discovered.
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

// openUserFormFor adds a device login to a named tenant. The tenant ID travels
// with it: a tenant the account can reach may have no record yet, and saving
// has to be able to create one rather than failing.
func (m tuiModel) openUserFormFor(t config.Tenant) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	return m.openForm(formUser, "Add ION user to "+t.Name, phaseAddUser, []formField{
		newField("ion user", "cisops", false),
		newField("ion password", "", true),
	})
}

func (m tuiModel) openEditPasswordForm(user string) (tea.Model, tea.Cmd) {
	return m.openEditPasswordFormFor(m.tenant, user)
}

// openEditPasswordFormFor changes a password for one named tenant's user.
func (m tuiModel) openEditPasswordFormFor(t config.Tenant, user string) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	m.formUserName = user
	return m.openForm(formEditPass, "New ION password for "+user+" on "+t.Name, phaseAddUser, []formField{
		newField("ion password", "", true),
	})
}

// openPasswordForm asks for a device login for one named tenant. The tenant is
// explicit because the element being opened may belong to a different tenant
// than the one being browsed, and its credentials are stored separately.
func (m tuiModel) openPasswordForm(t config.Tenant, user string) (tea.Model, tea.Cmd) {
	m.formTenantName = t.Name
	m.formTenantTSG = t.TSGID
	if user == "" {
		return m.openForm(formPassword, "ION credentials for "+t.Name, phasePassword, []formField{
			newField("ion user", "cisops", false),
			newField("ion password", "", true),
		})
	}
	m.formUserName = user
	return m.openForm(formPassword, "ION password for "+user+" on "+t.Name, phasePassword, []formField{
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
		// A macro is as many commands as it takes, so tab on the last line
		// adds the next one. Only with something on that line: growing on
		// every tab would leave empty lines behind to walk through.
		if m.formKind == formMacro && m.formCursor == len(m.form)-1 && m.formValue(m.formCursor) != "" {
			return m.addMacroCommand()
		}
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor + 1) % len(m.form)
		return m, m.form[m.formCursor].input.Focus()

	case "shift+tab", "up":
		m.form[m.formCursor].input.Blur()
		m.formCursor = (m.formCursor - 1 + len(m.form)) % len(m.form)
		return m, m.form[m.formCursor].input.Focus()

	case "enter":
		return m.submitForm()

	case "backspace":
		// On an empty command line, the key that would have emptied it takes
		// the line out instead. The last one stays: the form always has
		// somewhere to type.
		if m.formKind == formMacro && m.formCursor >= 1 && len(m.form) > 2 && m.formValue(m.formCursor) == "" {
			return m.removeMacroCommand()
		}
	}

	var cmd tea.Cmd
	m.form[m.formCursor].input, cmd = m.form[m.formCursor].input.Update(msg)
	return m, cmd
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
		dir, err := output.ExpandHome(m.formValue(0))
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.cfg.BatchDir = dir
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.status = ""
		m.form, m.formKind = nil, formNone
		m.phase = m.formReturn
		return m, nil

	case formTenant:
		name, tsg := m.formValue(0), m.formValue(1)
		if name == "" || tsg == "" {
			m.status = "name and tsg id are required"
			return m, nil
		}
		t := config.Tenant{Name: name, TSGID: tsg, ClientID: m.formValue(2), Auth: m.formAuth}
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
		m.cacheState, m.cacheCount, m.cacheAt = cacheNone, 0, time.Time{}
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
			// A discovered tenant has no record until it is worked in, and
			// giving it a login is exactly that.
			if m.formTenantTSG == "" {
				m.status = "tenant " + m.formTenantName + " is gone"
				return m, nil
			}
			t = config.Tenant{Name: m.formTenantName, TSGID: m.formTenantTSG, Auth: config.AuthBrowser}
		}
		if t.TSGID == "" {
			t.TSGID = m.formTenantTSG
		}
		t.Added = true
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
		// A tenant's first user becomes what it runs as, so adding one is
		// enough to make that tenant usable.
		if m.runAsFor(t.TSGID) == "" || len(t.Users) == 1 {
			m.setRunAs(t.TSGID, user)
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
			if v, err := m.store.Secrets.Get(config.RefreshTokenKey(m.formOldTSG)); err == nil && v != "" {
				_ = m.store.Secrets.Set(config.RefreshTokenKey(tsg), v)
				_ = m.store.Secrets.Delete(config.RefreshTokenKey(m.formOldTSG))
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
		user, pass := m.formUserName, m.formValue(0)
		if len(m.form) == 2 {
			user, pass = m.formValue(0), m.formValue(1)
		}
		if user == "" || pass == "" {
			m.status = "user and password are required"
			return m, nil
		}

		// The credential belongs to the tenant that was asked about, which is
		// not necessarily the one being browsed.
		t, ok := m.cfg.Tenant(m.formTenantName)
		if !ok {
			t = config.Tenant{Name: m.formTenantName, TSGID: m.formTenantTSG, Auth: config.AuthBrowser, Added: true}
		}
		if t.TSGID == "" {
			t.TSGID = m.formTenantTSG
		}
		if err := m.store.Secrets.Set(config.IONPassKey(t.TSGID, user), pass); err != nil {
			m.status = err.Error()
			return m, nil
		}
		t.AddUser(user, t.DefaultUser() == "")
		m.cfg.Upsert(t)
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
			return m, nil
		}
		// Only follow the change when it was the browsed tenant; otherwise the
		// current tenant and its user are left alone.
		if t.Name == m.tenant.Name {
			m.tenant, m.ionUser = t, user
		}
		m.form, m.formKind = nil, formNone
		m.formUserName = ""
		m.status = ""
		m.phase = phasePicking
		// Re-enter so any further tenant still missing a login is asked next.
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
			label = ui.StyleElement.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}

	b.WriteString("\n")
	hint := "↑↓ move · enter select · alt+m/alt+a service account or browser · q/alt+w quit"
	if m.sessions.DetachedCount() > 0 || m.sessions.Count() > 0 {
		hint = "↑↓ move · enter select · alt+m/alt+a service account or browser · alt+t reattach · q/alt+w quit"
	}
	b.WriteString(ui.Footer(m.status, hint))
	return b.String()
}

// reopenFromMenu brings back the state the menu was left from. A reattached
// session sits behind tab 2 onwards, so the elements list that owns tab 1 is
// restored from cache first; otherwise reattaching lands on an empty list.
func (m tuiModel) reopenFromMenu() (tea.Model, tea.Cmd) {
	if len(m.all) == 0 {
		m.loadFromCache()
	}
	return m.reopenLastTab()
}

func (m tuiModel) viewSwitch() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))

	choices := m.switchChoices()
	noun := "tenant"
	if m.authMode == authTenant {
		noun = "service account"
	}
	count := fmt.Sprintf("%d %s", len(choices), noun)
	if len(choices) != 1 {
		count += "s"
	}
	b.WriteString(ui.StyleCmd.Render(count) + "\n")

	if m.authMode == authTenant {
		// No provenance line: these were typed in, not discovered, and the
		// browser account that listed other tenants cannot reach them.
		b.WriteString(ui.StyleDim.Render("one tenant each, added in settings") + "\n")
	} else if c, err := m.store.TenantCache(); err == nil && c != nil && c.Account != "" {
		b.WriteString(ui.StyleDim.Render("from "+c.Account+", listed "+ui.Age(c.Age())+" ago") + "\n")
	}
	b.WriteString("\n")

	if len(choices) == 0 {
		empty := "none yet — add an account from the menu"
		if m.authMode == authTenant {
			empty = "none yet — add one in settings, or alt+m to sign in with a browser"
		}
		b.WriteString("  " + ui.StyleDim.Render(empty) + "\n")
	}
	for i, c := range choices {
		cursor := "  "
		marker := ""
		if c.tsg == m.tenant.TSGID && m.hasTenant {
			marker = ui.StyleOK.Render("  (current)")
		} else if !c.known && c.saved {
			// A tenant with no discovered counterpart is reachable only through
			// its own saved credential.
			marker = ui.StyleDim.Render("  (service account)")
		}
		// The whole row is padded as plain text and then coloured in one piece.
		// Styling the name first would make its escape codes count towards the
		// column width, which shifts the id from row to row.
		row := fmt.Sprintf("%-30s %s", ui.Truncate(c.name, 30), c.tsg)
		if i == m.switchCursor {
			cursor = ui.StyleCmd.Render("> ")
			row = ui.StyleElement.Render(row)
		} else {
			row = ui.StyleDim.Render(row)
		}
		b.WriteString(cursor + row + marker + "\n")
	}

	b.WriteString("\n")
	hint := "↑↓ move · enter use · alt+r re-list · esc back"
	if m.authMode == authTenant {
		hint = "↑↓ move · enter use · esc back"
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

	// The macro form grows, and the row that says so is the only thing that
	// tells you tab will add one.
	if m.formKind == formMacro {
		b.WriteString("  " + strings.Repeat(" ", 16) + ui.StyleGhost.Render("+ another command") + "\n")
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

// plaintextWarning is shown on any form that takes a secret, before it is
// typed, whenever there is no OS keyring to put it in.
func (m tuiModel) plaintextWarning() string {
	if m.store == nil || !config.Plaintext(m.store.Secrets) {
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
	for i, t := range m.cfg.Tenants {
		if t.Name == m.tenant.Name {
			return i
		}
	}
	return -1
}

func (m tuiModel) onUsersKey(key string) (tea.Model, tea.Cmd) {
	// The panel manages one tenant at a time, chosen with left/right, because a
	// browser account can reach tenants with completely different ION logins.
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
			t.AddUser(name, true)
			m.cfg.Upsert(t)
			if err := m.store.SaveConfig(m.cfg); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.setRunAs(t.TSGID, name)
			if t.Name == m.tenant.Name {
				m.tenant, m.ionUser = t, name
			}
			m.status = name + " is the default for " + t.Name
		}
	case "x":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			if t.RemoveUser(name) {
				_ = m.store.Secrets.Delete(config.IONPassKey(t.TSGID, name))
				m.cfg.Upsert(t)
				if err := m.store.SaveConfig(m.cfg); err != nil {
					m.status = err.Error()
					return m, nil
				}
				if m.runAs[t.TSGID] == name {
					m.setRunAs(t.TSGID, t.DefaultUser())
				}
				if t.Name == m.tenant.Name {
					m.tenant = t
					if m.ionUser == name {
						m.ionUser = t.DefaultUser()
					}
				}
				m.userCursor = max(0, min(m.userCursor, len(t.Users)-1))
			}
		}
	case "p":
		if m.userCursor < len(users) {
			return m.openEditPasswordFormFor(t, users[m.userCursor].Name)
		}
		return m, nil
	case "enter":
		if m.userCursor < len(users) {
			name := users[m.userCursor].Name
			m.setRunAs(t.TSGID, name)
			if t.Name == m.tenant.Name {
				m.ionUser = name
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
		if v, err := m.store.Secrets.Get(config.IONPassKey(t.TSGID, u.Name)); err == nil && v != "" {
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

// startSignIn shows the waiting screen and opens the browser.
func (m tuiModel) startSignIn() (tea.Model, tea.Cmd) {
	if !m.hasTenant {
		m.status = "no tenant to sign in to"
		m.phase = phaseMenu
		return m, nil
	}
	f := app.TenantFlags(m.store, m.tenant, m.ionUser)
	m.flags = f
	m.status = ""
	m.phase = phaseSignIn
	return m, tea.Batch(m.spinner.Tick, signIn(m.ctx, f, m.store))
}

func (m tuiModel) viewSignIn() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(fmt.Sprintf("  %s waiting for browser\n\n", m.spinner.View()))
	b.WriteString("  " + ui.StyleDim.Render("finish signing in to "+m.tenant.Name+" in the browser window") + "\n")
	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "esc stop waiting"))
	return b.String()
}

// viewSignedIn reports what a sign-in actually achieved. Each line is something
// that was checked, not something assumed: the identity comes from the tokens,
// the region and roles from a profile call that the API had to accept, and the
// session expiry from the refresh token itself.
func (m tuiModel) viewSignedIn() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in to "+m.tenant.Name) + "\n\n")

	row := func(k, v string) {
		b.WriteString("  " + ui.Label(k) + v + "\n")
	}

	row("account", ui.StyleElement.Render(m.ident.Display()))
	row("tenant", m.tenant.TSGID)
	if m.profile.Region != "" {
		row("region", m.profile.Region)
	}
	if len(m.profile.Roles) > 0 {
		row("roles", strings.Join(m.profile.Roles, ", "))
	}
	if m.tokenTTL > 0 {
		row("api token", ui.StyleDim.Render("renews every "+ui.Age(m.tokenTTL)))
	}
	if left, ok := m.ident.SessionLeft(); ok {
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
	switch m.cacheState {
	case cacheUpdating:
		b.WriteString("  " + m.spinner.View() + " " + ui.StyleDim.Render("fetching elements") + "\n")
	default:
		b.WriteString("  " + m.cacheLine() + "\n")
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "enter continue · esc menu"))
	return b.String()
}
