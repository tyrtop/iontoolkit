package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

func discoverTenants(ctx context.Context, rt *runtime.Runtime) tea.Cmd {
	return func() tea.Msg {
		return discoveredMsg{err: rt.Exec(&runtime.Call{Ctx: ctx}, "auth.signin")}
	}
}

func (m tuiModel) startDiscover() (tea.Model, tea.Cmd) {
	m.status = ""
	m.phase = phaseDiscover
	return m, tea.Batch(m.spinner.Tick, discoverTenants(m.ctx, m.runtime()))
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
	auth           string
}

func (m tuiModel) tenantChoices() []tenantChoice {
	known := m.runtime().KnownTenants()
	out := make([]tenantChoice, 0, len(known))
	for _, k := range known {
		out = append(out, tenantChoice{name: k.Name, tsg: k.TSG, known: k.Known, saved: k.Saved,
			serviceAccount: k.ServiceAccount, auth: k.Auth})
	}
	return out
}

func (m tuiModel) switchChoices() []tenantChoice {
	all := m.tenantChoices()
	if m.authMode() != authTenant {
		return all
	}
	out := make([]tenantChoice, 0, len(all))
	for _, c := range all {
		if c.serviceAccount {
			out = append(out, c)
		}
	}

	if m.hasTenant() && !m.tenant().UsesBrowser() {
		found := false
		for _, c := range out {
			if c.tsg == m.tenant().TSGID && c.name == m.tenant().Name {
				found = true
			}
		}
		if !found {
			out = append(out, tenantChoice{
				name: m.tenant().Name, tsg: m.tenant().TSGID,
				serviceAccount: true,
			})
		}
	}
	return out
}

func (m tuiModel) switchAt(cursor int) (tenantChoice, bool) {
	choices := m.switchChoices()
	if m.authMode() == authTenant {
		cursor--
	}
	if cursor < 0 || cursor >= len(choices) {
		return tenantChoice{}, false
	}
	return choices[cursor], true
}

func (m tuiModel) switchRows() int {
	n := len(m.switchChoices())
	if m.authMode() == authTenant {
		n++
	}
	return n
}

func (m tuiModel) useTenant(c tenantChoice) (tuiModel, error) {
	t, err := m.runtime().UseBrowserTenant(c.name, c.tsg)
	if err != nil {
		return m, err
	}
	m.forgetRows()
	m.runtime().PeekCache(t.TSGID)
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

func (m tuiModel) tenantAuth(c tenantChoice) string { return c.auth }

func (m tuiModel) startTenantPick() (tuiModel, tea.Cmd) {
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
		m.pickCursor = min(len(m.runtime().Discovered())-1, m.pickCursor+1)
		return m, nil
	case " ", "space":
		if found := m.runtime().Discovered(); m.pickCursor < len(found) {
			m.runtime().TogglePick(found[m.pickCursor].ID)
		}
		return m, nil
	case "ctrl+a":
		m.runtime().PickAll()
		return m, nil
	case "enter":
		return m.saveTenantPick()
	}
	return m, nil
}

func (m tuiModel) saveTenantPick() (tea.Model, tea.Cmd) {
	if err := m.runtime().AddPicked(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.forgetRows()
	m.menuCursor = 0
	m.status = ""

	if warm := m.cfg().AddedTenants(); m.runtime().BrowserSignedIn() && len(warm) > 0 {
		m.runtime().SetWarming(len(warm))
		m.phase = phaseWarming
		return m, tea.Batch(m.spinner.Tick, warmTenants(m.ctx, m.runtime()))
	}

	m.phase = phaseMenu
	return m.loadFleet()
}

type tenantsWarmedMsg struct {
	results []app.TenantWarm
}

func warmTenants(ctx context.Context, rt *runtime.Runtime) tea.Cmd {
	return func() tea.Msg {
		c := &runtime.Call{Ctx: ctx}
		_ = rt.Exec(c, "auth.warm")
		results, _ := c.Result.([]app.TenantWarm)
		return tenantsWarmedMsg{results: results}
	}
}

func (m tuiModel) viewWarming() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in as "+m.runtime().Identity().Display()) + "\n")
	b.WriteString(fmt.Sprintf("  %s signing in to each tenant\n\n", m.spinner.View()))
	b.WriteString("  " + ui.StyleDim.Render(fmt.Sprintf(
		"%d tenants, one at a time -- this is what makes one sign-in last", m.runtime().Warming())) + "\n")
	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status, "please wait"))
	return b.String()
}

func (m tuiModel) onTenantsWarmed(msg tenantsWarmedMsg) (tea.Model, tea.Cmd) {
	m.phase = phaseMenu

	var failed []string
	for _, r := range msg.results {
		if !r.Skipped && r.Err != nil {
			failed = append(failed, r.Tenant.Name+": "+shortTenantErr(r.Err))
		}
	}

	note := strings.Join(failed, "; ")
	m.status = note

	next, cmd := m.loadFleet()
	mm := next.(tuiModel)
	if note != "" {
		mm.status = note
	}
	return mm, cmd
}

func (m tuiModel) viewTenantPick() string {
	var b strings.Builder
	b.WriteString(ui.Banner(m.width, m.height))
	b.WriteString(ui.StyleOK.Render(ui.MarkCheck.String()+" signed in as "+m.runtime().Identity().Display()) + "\n")
	b.WriteString(ui.StyleDim.Render("pick the tenants to work with; searching spans all of them") + "\n\n")

	for i, t := range m.runtime().Discovered() {
		cursor := "  "
		box := "[ ] "
		if m.runtime().Picked(t.ID) {
			box = "[x] "
		}
		row := box + fmt.Sprintf("%-32s %s", ui.Truncate(t.Name, 32), t.ID)
		if i == m.pickCursor {
			cursor = ui.StyleCmd.Render("> ")
			row = ui.StyleElement.Render(row)
		} else if m.runtime().Picked(t.ID) {
			row = ui.StyleOK.Render(row)
		} else {
			row = ui.StyleDim.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}

	b.WriteString("\n")
	b.WriteString(ui.Footer(m.status,
		fmt.Sprintf("%d picked · ↑↓ move · space pick · ctrl+a all · enter add · esc cancel", m.runtime().PickedCount())))
	return b.String()
}
