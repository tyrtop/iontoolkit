package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/search"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase"
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

type tenantElementsMsg struct {
	tsg      string
	tenant   string
	elements []api.Element
	sites    map[string]string
	siteErr  error
	err      error
}

func fetchTenant(ctx context.Context, rt *runtime.Runtime, t config.Tenant) tea.Cmd {
	return func() tea.Msg {
		l := rt.FetchTenant(ctx, t)
		if l.Err != nil {
			logging.L().Warn("element list failed", "tsg", t.TSGID, "tenant", t.Name, "err", l.Err)
		} else {
			logging.L().Info("elements loaded", "tsg", t.TSGID, "tenant", t.Name,
				"elements", len(l.Elements), "sites", len(l.Sites), "site_err", l.SiteErr)
		}
		return tenantElementsMsg{tsg: l.TSG, tenant: l.Tenant, elements: l.Elements,
			sites: l.Sites, siteErr: l.SiteErr, err: l.Err}
	}
}

func (m *tuiModel) mergeTenantElements(tsg, tenant string, els []api.Element) {
	m.runtime().MergeTenant(tsg, tenant, els)
	m.setElements(m.rows())
}

func (m *tuiModel) loadFromCache() {
	if n, _, _ := m.runtime().LoadCache(); n == 0 {
		return
	}
	for _, t := range m.aggregateTenants() {
		c, err := m.store().Cache(t.TSGID)
		if err != nil || !c.Usable() {
			continue
		}
		m.loadCachedCells(t.TSGID, c.Cells)
		cached := make(map[string]string, len(c.HA))
		for eid, w := range c.HA {
			cached[t.TSGID+"/"+eid] = w
		}
		m.runtime().SetCachedHA(cached)
	}
	m.setElements(m.rows())
}

func (m tuiModel) refreshActive() (tea.Model, tea.Cmd) {
	m.status = ""
	tenants, skipped, err := m.runtime().BeginRefresh()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	if len(skipped) > 0 {
		m.status = "skipped " + strings.Join(skipped, ", ") + ": no usable credentials"
	}
	cmds := []tea.Cmd{m.spinner.Tick}
	for _, t := range tenants {
		cmds = append(cmds, fetchTenant(m.ctx, m.runtime(), t))
	}
	return m, tea.Batch(cmds...)
}

func (m tuiModel) credsFor(tsg string) ionterm.Credentials {
	user, pass := m.runtime().Login(tsg)
	return ionterm.Credentials{User: user, Pass: pass}
}

func (m tuiModel) targetsFor(rows []elementRow) []sase.Target {
	out := make([]sase.Target, 0, len(rows))
	for _, r := range rows {
		out = append(out, m.runtime().TargetFor(r))
	}
	return out
}

func (m tuiModel) tenantClient(tsg string) *api.Client {
	return m.pool().Get(tsg)
}

func httpClientFor(f app.Flags) *http.Client {
	return &http.Client{Timeout: f.HTTPTimeout}
}

func (m *tuiModel) tenantErr(tsg, tenant string, err error) {
	reason := shortTenantErr(err)
	m.runtime().SetIssue(tsg, reason)

	msg := tenant + ": " + reason
	if m.status == "" {
		m.status = msg
	} else if !strings.Contains(m.status, tenant) {
		m.status += "; " + msg
	}
	if app.IsAuthFailure(err.Error()) {
		m.runtime().TokenDied("sign-in expired", false)
	}
}

func shortTenantErr(err error) string {
	return ui.Truncate(app.ShortAuthError(err), 90)
}

func (m tuiModel) tenantOf(tsg string) (config.Tenant, bool) {
	for _, t := range m.cfg().Tenants {
		if t.TSGID == tsg {
			return t, true
		}
	}
	return config.Tenant{}, false
}

func (m tuiModel) ionUserFor(t config.Tenant) string {
	return m.runAsFor(t.TSGID)
}

func (m tuiModel) elementByKey(key string) (elementRow, bool) {
	for _, r := range m.rows() {
		if r.Key() == key {
			return r, true
		}
	}
	return elementRow{}, false
}

func (m tuiModel) tsgForElement(eid string) string {
	for _, r := range m.rows() {
		if r.ID == eid {
			return r.TSG
		}
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
	if m.scope != "" {
		return m.scope
	}
	if m.hasTenant() {
		return m.tenant().Name
	}
	return "no tenant"
}

func (m *tuiModel) computeScope() {
	seen := map[string]string{}
	for _, r := range m.rows() {
		seen[r.TSG] = r.Tenant
	}
	m.tenantCount = len(seen)
	switch len(seen) {
	case 0:
		m.scope = ""
	case 1:
		for _, name := range seen {
			m.scope = name
		}
	default:
		m.scope = fmt.Sprintf("%d tenants", len(seen))
	}
}

func (m tuiModel) aggregateTenants() []config.Tenant {
	if m.rt == nil {
		return nil
	}
	return m.rt.FleetTenants()
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

func (m *tuiModel) setRunAs(tsg, user string) { m.runtime().SetRunAs(tsg, user) }

func (m tuiModel) usersPanelTenant() config.Tenant {
	if t, ok := m.tenantOf(m.usersTSG); ok {
		return t
	}
	for _, r := range m.rows() {
		if r.TSG == m.usersTSG {
			return config.Tenant{Name: r.Tenant, TSGID: r.TSG, Auth: config.AuthBrowser}
		}
	}
	return m.tenant()
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
	for _, r := range m.rows() {
		if t, ok := m.tenantOf(r.TSG); ok {
			add(t)
			continue
		}
		add(config.Tenant{Name: r.Tenant, TSGID: r.TSG, Auth: config.AuthBrowser})
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

type tenantConnectedMsg struct {
	tsg    string
	client *api.Client
	fresh  bool
	err    error
}

func connectTenant(ctx context.Context, st *config.Store, f app.Flags, tsg string) tea.Cmd {
	return func() tea.Msg {
		c, err := app.NewClient(ctx, st, f, io.Discard)
		return tenantConnectedMsg{tsg: tsg, client: c, fresh: true, err: err}
	}
}

func recheckToken(ctx context.Context, c *api.Client, tsg string) tea.Cmd {
	return func() tea.Msg {
		if err := c.EnsureToken(ctx, preflightTTL); err != nil {
			return tenantConnectedMsg{tsg: tsg, err: err}
		}
		return tenantConnectedMsg{tsg: tsg, client: c}
	}
}

const preflightTTL = 90 * time.Second

func (m tuiModel) ensureClients(tsgs []string) []tea.Cmd {
	seen := map[string]bool{}
	var cmds []tea.Cmd
	for _, tsg := range tsgs {
		if tsg == "" || seen[tsg] {
			continue
		}
		if c := m.pool().Get(tsg); c != nil {
			if c.TokenGood(preflightTTL) {
				continue
			}
			seen[tsg] = true
			cmds = append(cmds, recheckToken(m.ctx, c, tsg))
			continue
		}
		if m.tenantIssues()[tsg] != "" {
			continue
		}
		seen[tsg] = true
		t, ok := m.tenantOf(tsg)
		if !ok {
			t = config.Tenant{Name: tsg, TSGID: tsg, Auth: config.AuthBrowser}
			for _, r := range m.rows() {
				if r.TSG == tsg {
					t.Name = r.Tenant
					break
				}
			}
		}
		f := app.TenantFlags(m.store(), t, m.runAsFor(tsg))
		if err := f.ValidateCreds(); err != nil {
			continue
		}
		cmds = append(cmds, connectTenant(m.ctx, m.store(), f, tsg))
	}
	return cmds
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

func (m tuiModel) tenantName(tsg string) string {
	if t, ok := m.tenantOf(tsg); ok {
		return t.Name
	}
	for _, r := range m.rows() {
		if r.TSG == tsg {
			return r.Tenant
		}
	}
	return tsg
}

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

func (m tuiModel) siteMap() search.SiteMap {
	if m.rt == nil {
		return nil
	}
	return m.rt.Sites()
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

func (m tuiModel) selection() map[string]bool {
	if m.rt == nil {
		return map[string]bool{}
	}
	return m.rt.Selection()
}

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

func (m *tuiModel) useResolved(t config.Tenant, ok bool) {
	if ok {
		m.runtime().UseTenant(t)
		return
	}
	m.runtime().ClearTenant()
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

func (m *tuiModel) setCacheState(c cacheState) {
	for name, s := range cacheStates {
		if s == c {
			m.runtime().SetFleetState(name)
		}
	}
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
