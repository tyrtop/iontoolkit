package tui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

func discoverTenants(ctx context.Context, timeout time.Duration, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		opt := app.BrowserOptions(&http.Client{Timeout: timeout}, st)
		d, err := saseauth.Discover(ctx, opt, "")
		return discoveredMsg{d: d, err: err}
	}
}

func (m tuiModel) startDiscover() (tea.Model, tea.Cmd) {
	m.status = ""
	m.phase = phaseDiscover
	return m, tea.Batch(m.spinner.Tick, discoverTenants(m.ctx, app.Defaults.HTTPTimeout, m.store))
}

func (m tuiModel) viewDiscover() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(fmt.Sprintf("  %s waiting for browser\n\n", m.spinner.View()))
	b.WriteString("  " + ui.StyleDim.Render("sign in, and your tenants will be listed here") + "\n")
	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "esc cancel"))
	return b.String()
}

type tenantChoice struct {
	name           string
	tsg            string
	known          bool
	saved          bool
	serviceAccount bool
}

func mergeKey(tsg, name string) string {
	if tsg != "" {
		return "tsg:" + tsg
	}
	return "name:" + name
}

func (m tuiModel) tenantChoices() []tenantChoice {
	merged := map[string]tenantChoice{}

	if c, err := m.store.TenantCache(); err == nil && c != nil && len(c.Tenants) > 0 {
		for _, t := range c.Tenants {
			merged[mergeKey(t.TSGID, t.Name)] = tenantChoice{name: t.Name, tsg: t.TSGID, known: true}
		}
	}
	if m.cfg != nil {
		for _, t := range m.cfg.Tenants {
			k := mergeKey(t.TSGID, t.Name)
			cur := merged[k]
			cur.tsg = t.TSGID
			cur.saved = true
			cur.name = t.Name
			cur.serviceAccount = !t.UsesBrowser()
			merged[k] = cur
		}
	}

	out := make([]tenantChoice, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

func (m tuiModel) switchChoices() []tenantChoice {
	all := m.tenantChoices()
	if m.authMode != authTenant {
		return all
	}
	out := make([]tenantChoice, 0, len(all))
	for _, c := range all {
		if c.serviceAccount {
			out = append(out, c)
		}
	}

	if m.hasTenant && !m.tenant.UsesBrowser() {
		found := false
		for _, c := range out {
			if c.tsg == m.tenant.TSGID && c.name == m.tenant.Name {
				found = true
			}
		}
		if !found {
			out = append(out, tenantChoice{
				name: m.tenant.Name, tsg: m.tenant.TSGID,
				serviceAccount: true,
			})
		}
	}
	return out
}

func (m tuiModel) switchAt(cursor int) (tenantChoice, bool) {
	choices := m.switchChoices()
	if m.authMode == authTenant {
		cursor--
	}
	if cursor < 0 || cursor >= len(choices) {
		return tenantChoice{}, false
	}
	return choices[cursor], true
}

func (m tuiModel) switchRows() int {
	n := len(m.switchChoices())
	if m.authMode == authTenant {
		n++
	}
	return n
}

func (m tuiModel) useTenant(c tenantChoice) (tuiModel, error) {
	t, ok := m.cfg.Tenant(c.name)
	if !ok || t.TSGID != c.tsg {
		t = config.Tenant{Name: c.name, TSGID: c.tsg, Auth: config.AuthBrowser}
	}
	t.Added = true
	m.cfg.Upsert(t)
	m.cfg.LastTenant = t.Name
	if err := m.store.SaveConfig(m.cfg); err != nil {
		return m, err
	}

	m.tenant, m.hasTenant = t, true
	m.ionUser = t.DefaultUser()
	m.all, m.matches, m.selected = nil, nil, map[string]bool{}
	m.forgetRows()
	m.cacheState, m.cacheCount, m.cacheAt = cacheNone, 0, time.Time{}
	m.client = nil
	m.pool.drop(c.tsg)
	m.tokenDead = ""
	if cache, err := m.store.Cache(t.TSGID); err == nil && cache != nil && len(cache.Elements) > 0 {
		m.cacheCount = len(cache.Elements)
		m.cacheAt = time.Now().Add(-cache.Age())
		m.cacheState = cacheCached
		if cache.Age() >= cacheFresh {
			m.cacheState = cacheUpdating
		}
	} else {
		m.cacheState = cacheUpdating
	}
	return m, nil
}

func (m tuiModel) browserTenants() []tenantChoice {
	var out []tenantChoice
	for _, c := range m.tenantChoices() {
		if c.known || (c.saved && m.tenantAuth(c) == config.AuthBrowser) {
			out = append(out, c)
		}
	}
	return out
}

func (m tuiModel) serviceTenants() []tenantChoice {
	var out []tenantChoice
	for _, c := range m.tenantChoices() {
		if c.saved && m.tenantAuth(c) != config.AuthBrowser {
			out = append(out, c)
		}
	}
	return out
}

func (m tuiModel) tenantAuth(c tenantChoice) string {
	if m.cfg == nil {
		return ""
	}
	for _, t := range m.cfg.Tenants {
		if t.TSGID == c.tsg && t.Name == c.name {
			return t.Auth
		}
	}
	return ""
}

func (m tuiModel) startTenantPick() (tuiModel, tea.Cmd) {
	m.picked = map[string]bool{}
	for _, t := range m.discovered {
		if m.cfg.IsAdded(t.ID) {
			m.picked[t.ID] = true
		}
	}
	m.pickCursor = 0
	m.status = ""
	m.phase = phaseTenantPick
	return m, nil
}

func (m tuiModel) onTenantPickKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.status = ""
		m.phase = phaseMenu
		return m, nil
	case "up", "k":
		m.pickCursor = max(0, m.pickCursor-1)
		return m, nil
	case "down", "j":
		m.pickCursor = min(len(m.discovered)-1, m.pickCursor+1)
		return m, nil
	case " ", "space":
		if m.pickCursor < len(m.discovered) {
			id := m.discovered[m.pickCursor].ID
			if m.picked[id] {
				delete(m.picked, id)
			} else {
				m.picked[id] = true
			}
		}
		return m, nil
	case "ctrl+a":
		all := len(m.picked) == len(m.discovered)
		m.picked = map[string]bool{}
		if !all {
			for _, t := range m.discovered {
				m.picked[t.ID] = true
			}
		}
		return m, nil
	case "enter":
		return m.saveTenantPick()
	}
	return m, nil
}

func (m tuiModel) saveTenantPick() (tea.Model, tea.Cmd) {
	if len(m.picked) == 0 {
		m.status = "pick at least one tenant"
		return m, nil
	}

	byTSG := map[string]saseauth.Tenant{}
	for _, t := range m.discovered {
		byTSG[t.ID] = t
	}

	var first config.Tenant
	for _, d := range m.discovered {
		existing, has := m.cfg.Tenant(d.Name)
		rec := existing
		if !has || existing.TSGID != d.ID {
			rec = config.Tenant{Name: d.Name, TSGID: d.ID, Auth: config.AuthBrowser}
		}
		rec.Added = m.picked[d.ID]
		if !rec.Added && !has {
			continue
		}
		m.cfg.Upsert(rec)
		if rec.Added && first.TSGID == "" {
			first = rec
		}
	}

	if first.TSGID == "" {
		m.status = "pick at least one tenant"
		return m, nil
	}
	if !m.cfg.IsAdded(m.tenant.TSGID) {
		m.cfg.LastTenant = first.Name
		m.tenant, m.hasTenant = first, true
		m.ionUser = first.DefaultUser()
	}
	if err := m.store.SaveConfig(m.cfg); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.all, m.matches, m.selected = nil, nil, map[string]bool{}
	m.forgetRows()
	m.discovered, m.picked = nil, nil
	m.menuCursor = 0
	m.status = ""

	if warm := m.cfg.AddedTenants(); m.browserAuthDone && len(warm) > 0 {
		m.warming = len(warm)
		m.phase = phaseWarming
		return m, tea.Batch(m.spinner.Tick, warmTenants(m.ctx, m.store, warm))
	}

	m.phase = phaseMenu
	m.loadFromCache()
	if m.cacheState != cacheCached {
		return m.refreshActive()
	}
	return m, nil
}

type tenantsWarmedMsg struct {
	results []app.TenantWarm
}

func warmTenants(ctx context.Context, st *config.Store, tenants []config.Tenant) tea.Cmd {
	return func() tea.Msg {
		return tenantsWarmedMsg{results: app.WarmEach(ctx, st, tenants)}
	}
}

func (m tuiModel) viewWarming() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in as "+m.ident.Display()) + "\n")
	b.WriteString(fmt.Sprintf("  %s signing in to each tenant\n\n", m.spinner.View()))
	b.WriteString("  " + ui.StyleDim.Render(fmt.Sprintf(
		"%d tenants, one at a time -- this is what makes one sign-in last", m.warming)) + "\n")
	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "please wait"))
	return b.String()
}

func (m tuiModel) onTenantsWarmed(msg tenantsWarmedMsg) (tea.Model, tea.Cmd) {
	m.phase = phaseMenu
	m.warming = 0

	var failed []string
	for _, r := range msg.results {
		if r.Skipped {
			continue
		}
		if r.Err != nil {
			if m.tenantIssues == nil {
				m.tenantIssues = map[string]string{}
			}
			m.tenantIssues[r.Tenant.TSGID] = shortTenantErr(r.Err)
			failed = append(failed, r.Tenant.Name+": "+shortTenantErr(r.Err))
			continue
		}
		delete(m.tenantIssues, r.Tenant.TSGID)
		if r.Tenant.TSGID == m.tenant.TSGID {
			m.tokenDead = ""
		}
	}

	note := strings.Join(failed, "; ")
	m.status = note

	m.loadFromCache()
	if m.cacheState == cacheCached {
		return m, nil
	}

	next, cmd := m.refreshActive()
	mm := next.(tuiModel)
	if note != "" {
		mm.status = note
	}
	return mm, cmd
}

func (m tuiModel) viewTenantPick() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in as "+m.ident.Display()) + "\n")
	b.WriteString(ui.StyleDim.Render("pick the tenants to work with; searching spans all of them") + "\n\n")

	for i, t := range m.discovered {
		cursor := "  "
		box := "[ ] "
		if m.picked[t.ID] {
			box = "[x] "
		}
		row := box + fmt.Sprintf("%-32s %s", ui.Truncate(t.Name, 32), t.ID)
		if i == m.pickCursor {
			cursor = ui.StyleCmd.Render("> ")
			row = ui.StyleElement.Render(row)
		} else if m.picked[t.ID] {
			row = ui.StyleOK.Render(row)
		} else {
			row = ui.StyleDim.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status,
		fmt.Sprintf("%d picked · ↑↓ move · space pick · ctrl+a all · enter add · esc cancel", len(m.picked))))
	return b.String()
}
