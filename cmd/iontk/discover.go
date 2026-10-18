package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/saseauth"
)

func discoverTenants(ctx context.Context, timeout time.Duration, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		opt := browserOptions(&http.Client{Timeout: timeout}, st)
		d, err := saseauth.Discover(ctx, opt, "")
		return discoveredMsg{d: d, err: err}
	}
}

// startDiscover signs in and finds out which tenants the account can reach.
// Nothing is typed and nothing is added: the portal decides the list.
func (m tuiModel) startDiscover() (tea.Model, tea.Cmd) {
	m.status = ""
	m.phase = phaseDiscover
	return m, tea.Batch(m.spinner.Tick, discoverTenants(m.ctx, flagHTTPTimeout, m.store))
}

func (m tuiModel) viewDiscover() string {
	var b strings.Builder
	b.WriteString(banner(m.width, m.height))
	b.WriteString(fmt.Sprintf("  %s waiting for browser\n\n", m.spinner.View()))
	b.WriteString("  " + styleDim.Render("sign in, and your tenants will be listed here") + "\n")
	b.WriteString("\n")
	b.WriteString(footer(m.status, "esc cancel"))
	return b.String()
}

// tenantChoice is one row of the tenant list, which merges what the signed-in
// account can reach with whatever has a saved record of its own. A service
// account only ever appears through the saved side, since it carries exactly
// one tenant and cannot be discovered.
type tenantChoice struct {
	name  string
	tsg   string
	known bool // the browser account can reach it
	saved bool // has a config record, so it may hold ION users or a secret
}

// mergeKey identifies a tenant across the two sources. The tenant ID is the
// real identity, but a record saved without one must not collapse into every
// other such record, so its name stands in.
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
			// A saved record names the tenant, since that name is what the user
			// sees elsewhere and what the config is keyed by.
			cur.name = t.Name
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

// useTenant makes a tenant active, creating its record the first time it is
// used. Discovered tenants are browser-backed by definition.
func (m tuiModel) useTenant(c tenantChoice) (tuiModel, error) {
	t, ok := m.cfg.Tenant(c.name)
	if !ok || t.TSGID != c.tsg {
		t = config.Tenant{Name: c.name, TSGID: c.tsg, Auth: config.AuthBrowser}
	}
	// Working in a tenant puts it in the set, so the element list and its
	// search cover it from then on.
	t.Added = true
	m.cfg.Upsert(t)
	m.cfg.LastTenant = t.Name
	if err := m.store.SaveConfig(m.cfg); err != nil {
		return m, err
	}

	m.tenant, m.hasTenant = t, true
	m.ionUser = t.DefaultUser()
	m.all, m.matches, m.selected = nil, nil, map[string]bool{}
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

// browserTenants are the tenants reachable through the signed-in account, plus
// any that already have a browser-backed record. This is the list "sign in with
// browser" leads to.
func (m tuiModel) browserTenants() []tenantChoice {
	var out []tenantChoice
	for _, c := range m.tenantChoices() {
		if c.known || (c.saved && m.tenantAuth(c) == config.AuthBrowser) {
			out = append(out, c)
		}
	}
	return out
}

// serviceTenants are the tenants that exist only because a service account was
// configured for them. They cannot be discovered, so they are managed under
// settings rather than appearing in the browser account's list.
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

// pickedSet is which discovered tenants are ticked in the picker.
func (m tuiModel) startTenantPick() (tuiModel, tea.Cmd) {
	m.picked = map[string]bool{}
	for _, t := range m.discovered {
		// Anything already in the working set starts ticked, so re-listing is
		// not a way to lose it.
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

// saveTenantPick turns the ticks into the working set. Records are created for
// newly added tenants, since that is where their ION users will live.
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
			// Never added and never configured: nothing worth recording.
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
	m.discovered, m.picked = nil, nil
	m.menuCursor = 0
	m.status = ""
	m.phase = phaseMenu
	m.loadFromCache()
	if m.cacheState != cacheCached {
		return m.refreshActive()
	}
	return m, nil
}

func (m tuiModel) viewTenantPick() string {
	var b strings.Builder
	b.WriteString(banner(m.width, m.height))
	b.WriteString(styleOK.Render(markCheck.String()+" signed in as "+m.ident.Display()) + "\n")
	b.WriteString(styleDim.Render("pick the tenants to work with; searching spans all of them") + "\n\n")

	for i, t := range m.discovered {
		cursor := "  "
		box := "[ ] "
		if m.picked[t.ID] {
			box = "[x] "
		}
		// Pad the plain text before styling, or the escape codes count towards
		// the width and the id shifts between rows.
		row := box + fmt.Sprintf("%-32s %s", truncate(t.Name, 32), t.ID)
		if i == m.pickCursor {
			cursor = styleCmd.Render("> ")
			row = styleElement.Render(row)
		} else if m.picked[t.ID] {
			row = styleOK.Render(row)
		} else {
			row = styleDim.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}

	b.WriteString("\n")
	b.WriteString(footer(m.status,
		fmt.Sprintf("%d picked · ↑↓ move · space pick · ctrl+a all · enter add · esc cancel", len(m.picked))))
	return b.String()
}
