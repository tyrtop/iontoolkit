package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

type elementRow = runtime.Element

type sessionTarget struct {
	eid  string
	name string
	tsg  string
}

type clientPool = runtime.Clients

func (m *tuiModel) pool() *runtime.Clients { return m.runtime().Clients() }

func (m *tuiModel) loadFromCache() {
	if n, _, _ := m.runtime().LoadCache(); n > 0 {
		m.listChanged()
	}
}

type fleetDoneMsg struct{ err error }

func (m tuiModel) loadFleet() (tea.Model, tea.Cmd) {
	if m.runtime().Fleet().Pending > 0 {
		return m, m.spinner.Tick
	}
	m.loadFromCache()
	return m, tea.Batch(m.spinner.Tick, fleetLoad(m.ctx, m.runtime()))
}

func fleetLoad(ctx context.Context, rt *runtime.Runtime) tea.Cmd {
	return func() tea.Msg {
		_, err := rt.LoadTenants(&runtime.Call{Ctx: ctx}, false, true)
		return fleetDoneMsg{err: err}
	}
}

func (m tuiModel) refreshActive() (tea.Model, tea.Cmd) {
	m.status = ""
	rt, ctx := m.runtime(), m.ctx
	tenants, skipped, err := rt.BeginRefresh()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	if len(skipped) > 0 {
		m.status = "skipped " + strings.Join(skipped, ", ") + ": no usable credentials"
	}
	run := func() tea.Msg {
		_, err := rt.RefreshTenants(&runtime.Call{Ctx: ctx}, tenants, true)
		return fleetDoneMsg{err: err}
	}
	return m, tea.Batch(m.spinner.Tick, run)
}

func (m tuiModel) onFleetDone(msg fleetDoneMsg) (tea.Model, tea.Cmd) {
	if dead := m.runtime().TokenDead(); dead != "" && m.authMode() == authBrowser {
		if m.phase == phaseMenu {
			m.status = "sign-in required — pick the browser sign-in below"
			return m, nil
		}
		m.signInResume = true
		return m.startSignInAuto()
	}
	if msg.err != nil && m.status == "" {
		m.status = msg.err.Error()
	}
	if len(m.rows()) == 0 && m.phase == phaseLoading {
		m.phase = phaseMenu
	}
	return m, nil
}

func (m tuiModel) credsFor(tsg string) ionterm.Credentials {
	user, pass := m.runtime().Login(tsg)
	return ionterm.Credentials{User: user, Pass: pass}
}

func shortTenantErr(err error) string {
	return ui.Truncate(app.ShortAuthError(err), 90)
}

func (m tuiModel) ionUserFor(t config.Tenant) string {
	return m.runAsFor(t.TSGID)
}

func (m tuiModel) elementByKey(key string) (elementRow, bool) { return m.runtime().RowByKey(key) }

func (m tuiModel) tsgForElement(eid string) string {
	if r, ok := m.runtime().Row(eid); ok {
		return r.TSG
	}
	return m.tenant().TSGID
}

func (m tuiModel) activeSessionCreds() ionterm.Credentials {
	s := m.sessions().Active()
	if s == nil {
		return m.credsFor(m.tenant().TSGID)
	}
	return m.credsFor(m.tsgForElement(s.ElementID))
}

func (m tuiModel) scopeLabel() string {
	if _, label := m.runtime().Scope(); label != "" {
		return label
	}
	if m.hasTenant() {
		return m.tenant().Name
	}
	return "no tenant"
}

func (m tuiModel) refreshNote() string {
	return m.elementsNote() + "\n" + m.haNote()
}

func (m tuiModel) elementsNote() string {
	switch m.cacheState() {
	case cacheUpdating:
		note := m.spinner.View() + " " + ui.StyleWarn.Render("refreshing elements")
		if m.pendingTenants() > 1 {
			note += ui.StyleDim.Render(fmt.Sprintf(" %d tenants", m.pendingTenants()))
		}
		return note
	case cacheFailed:
		return ui.StyleErr.Render("element refresh failed")
	case cacheNone:
		return ui.StyleDim.Render("no cached elements")
	}

	d := m.cacheAgeNow()
	if m.cacheState() == cacheUpdated && d < cacheFresh {
		return ui.StyleOK.Render("Elements refreshed!") + ago(d)
	}
	return ui.StyleDim.Render("Elements cached") + ago(d)
}

func (m tuiModel) haNote() string {
	note := m.haLine()
	cells := m.cellsNote()
	switch {
	case note == "":
		return cells
	case cells == "":
		return note
	}
	return note + ui.StyleDim.Render(" · ") + cells
}

func (m tuiModel) haLine() string {
	if m.haWorking() {
		note := m.spinner.View() + " " + ui.StyleWarn.Render("refreshing HA")
		if h := m.runtime().HA(); h.Total > 0 {
			note += ui.StyleDim.Render(fmt.Sprintf(" %d/%d clusters",
				min(h.Done, h.Total), h.Total))
		}
		return note
	}
	at := m.runtime().HA().At
	if at.IsZero() {
		return ""
	}

	d := time.Since(at)
	if d < cacheFresh {
		return ui.StyleOK.Render("HA refreshed!") + ago(d)
	}
	return ui.StyleDim.Render("HA cached") + ago(d)
}

func ago(d time.Duration) string {
	if d < time.Second {
		return ui.StyleDim.Render(" last updated just now")
	}
	return ui.StyleDim.Render(" last updated " + ui.Age(d) + " ago")
}

func (m tuiModel) runAsFor(tsg string) string { return m.runtime().RunAs(tsg) }

func (m tuiModel) usersPanelTenant() config.Tenant {
	if m.usersTSG == "" {
		return m.tenant()
	}
	return m.runtime().TenantFor(m.usersTSG)
}

func (m tuiModel) usersPanelTenants() []config.Tenant {
	var out []config.Tenant
	seen := map[string]bool{}
	add := func(t config.Tenant) {
		if t.TSGID == "" || seen[t.TSGID] {
			return
		}
		seen[t.TSGID] = true
		out = append(out, t)
	}
	for _, t := range m.runtime().RowTenants() {
		add(t)
	}
	if m.hasTenant() {
		add(m.tenant())
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (m tuiModel) cycleUsersTenant(delta int) tuiModel {
	list := m.usersPanelTenants()
	if len(list) < 2 {
		return m
	}
	at := 0
	for i, t := range list {
		if t.TSGID == m.usersTSG {
			at = i
		}
	}
	next := (at + delta + len(list)) % len(list)
	m.usersTSG = list[next].TSGID
	m.userCursor = 0
	m.status = ""
	return m
}

func (m tuiModel) countLabel() string {
	sel := fmt.Sprintf("%d selected", m.selectedCount())
	if len(m.matched()) != len(m.rows()) {
		return fmt.Sprintf("%s / %d of %d elements", sel, len(m.matched()), len(m.rows()))
	}
	return fmt.Sprintf("%s / %d elements", sel, len(m.rows()))
}

func targetsOf(rows []runtime.Element) []sessionTarget {
	out := make([]sessionTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, sessionTarget{eid: r.ID, name: r.Label(), tsg: r.TSG})
	}
	return out
}

func rowsOf(targets []sessionTarget) []runtime.Element {
	rows := make([]runtime.Element, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, runtime.Element{Element: api.Element{ID: t.eid, Name: t.name}, TSG: t.tsg})
	}
	return rows
}

func tsgsOf(targets []sessionTarget) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		if !seen[t.tsg] {
			seen[t.tsg] = true
			out = append(out, t.tsg)
		}
	}
	return out
}

func tsgsOfRows(rows []elementRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !seen[r.TSG] {
			seen[r.TSG] = true
			out = append(out, r.TSG)
		}
	}
	return out
}

func (m tuiModel) tenantName(tsg string) string { return m.runtime().TenantFor(tsg).Name }

func (m tuiModel) toggleCurrent() tuiModel {
	el, ok := m.current()
	if !ok {
		return m
	}
	m.runtime().ToggleKey(el.Key())
	m.rebuildRows()
	return m
}

func (m tuiModel) selectedNote() string {
	if !m.showingSelected() {
		return ""
	}
	return "  " + ui.StyleOK.Render("selected only")
}

func (m tuiModel) rows() []elementRow {
	if m.rt == nil {
		return nil
	}
	return m.rt.Elements()
}

func (m *tuiModel) runtime() *runtime.Runtime {
	if m.rt == nil {
		m.rt = app.NewRuntime(nil, nil)
	}
	return m.rt
}

func (m tuiModel) cfg() *config.Config {
	if m.rt == nil {
		return nil
	}
	return m.rt.Config()
}

func (m tuiModel) store() *config.Store {
	if m.rt == nil {
		return nil
	}
	return m.rt.Store()
}

func (m tuiModel) isSelected(key string) bool { return m.rt != nil && m.rt.IsSelected(key) }

func (m tuiModel) selectedCount() int {
	if m.rt == nil {
		return 0
	}
	return m.rt.SelectedCount()
}

func (m tuiModel) showingSelected() bool { return m.rt != nil && m.rt.ShowingSelected() }

func (m tuiModel) canUndoClear() bool { return m.rt != nil && m.rt.CanUndoClear() }

func (m tuiModel) matched() []int {
	if m.rt == nil {
		return nil
	}
	return m.rt.MatchIndices()
}

func (m tuiModel) tenant() config.Tenant {
	if m.rt == nil {
		return config.Tenant{}
	}
	t, _ := m.rt.Tenant()
	return t
}

func (m tuiModel) hasTenant() bool {
	if m.rt == nil {
		return false
	}
	_, ok := m.rt.Tenant()
	return ok
}

func (m tuiModel) ionUser() string {
	if m.rt == nil {
		return ""
	}
	return m.rt.IONUser()
}

func (m tuiModel) authMode() authMode {
	if m.rt != nil && m.rt.AuthMode() == runtime.AuthTenant {
		return authTenant
	}
	return authBrowser
}

func (m *tuiModel) setAuthMode(a authMode) {
	mode := runtime.AuthBrowser
	if a == authTenant {
		mode = runtime.AuthTenant
	}
	m.runtime().SetAuthMode(mode)
}

var cacheStates = map[string]cacheState{
	runtime.FleetNone: cacheNone, runtime.FleetUpdating: cacheUpdating, runtime.FleetUpdated: cacheUpdated,
	runtime.FleetCached: cacheCached, runtime.FleetFailed: cacheFailed,
}

func (m tuiModel) cacheState() cacheState {
	if m.rt == nil {
		return cacheNone
	}
	return cacheStates[m.rt.Fleet().State]
}

func (m tuiModel) cacheCount() int {
	if m.rt == nil {
		return 0
	}
	return m.rt.Fleet().Count
}

func (m tuiModel) cacheAt() time.Time {
	if m.rt == nil {
		return time.Time{}
	}
	return m.rt.Fleet().At
}

func (m tuiModel) pendingTenants() int {
	if m.rt == nil {
		return 0
	}
	return m.rt.Fleet().Pending
}

func (m tuiModel) tenantIssues() map[string]string {
	if m.rt == nil {
		return map[string]string{}
	}
	return m.rt.Fleet().Issues
}

func (m tuiModel) sessions() *ionterm.Manager {
	if m.rt == nil {
		return ionterm.NewManager()
	}
	return m.rt.Sessions()
}
