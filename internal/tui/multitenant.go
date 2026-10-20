package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

type elementRow struct {
	api.Element
	TSG    string
	Tenant string
}

func (e elementRow) key() string { return e.TSG + "/" + e.ID }

func (e elementRow) label() string {
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}

type sessionTarget struct {
	eid  string
	name string
	tsg  string
}

type clientPool struct {
	mu      sync.Mutex
	clients map[string]*api.Client
}

func newClientPool() *clientPool {
	return &clientPool{clients: map[string]*api.Client{}}
}

func (p *clientPool) get(tsg string) *api.Client {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients[tsg]
}

func (p *clientPool) set(tsg string, c *api.Client) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[tsg] = c
}

func (p *clientPool) drop(tsg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, tsg)
}

func (p *clientPool) count() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

type tenantElementsMsg struct {
	tsg      string
	tenant   string
	elements []api.Element
	sites    map[string]string
	siteErr  error
	err      error
}

func loadTenant(ctx context.Context, f app.Flags, t config.Tenant, pool *clientPool, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		fail := func(err error) tea.Msg {
			return tenantElementsMsg{tsg: t.TSGID, tenant: t.Name, err: err}
		}

		c := pool.get(t.TSGID)
		if c == nil {
			built, err := app.NewClient(ctx, st, f, io.Discard)
			if err != nil {
				return fail(err)
			}
			pool.set(t.TSGID, built)
			c = built
		}

		els, err := c.ListElements(ctx)
		if err != nil {
			return fail(err)
		}

		sites := map[string]string{}
		ss, siteErr := c.ListSites(ctx)
		for _, s := range ss {
			sites[t.TSGID+"/"+s.ID] = s.Name
		}

		if st != nil {
			_ = st.SaveCache(&config.ElementCache{
				TSGID:     t.TSGID,
				FetchedAt: time.Now(),
				Elements:  els,
				Sites:     sites,
			})
		}
		return tenantElementsMsg{
			tsg: t.TSGID, tenant: t.Name,
			elements: els, sites: sites, siteErr: siteErr,
		}
	}
}

func (m *tuiModel) mergeTenantElements(tsg, tenant string, els []api.Element) {
	kept := make([]elementRow, 0, len(m.all)+len(els))
	for _, r := range m.all {
		if r.TSG != tsg {
			kept = append(kept, r)
		}
	}
	for _, el := range els {
		kept = append(kept, elementRow{Element: el, TSG: tsg, Tenant: tenant})
	}
	m.setElements(kept)
}

func (m *tuiModel) loadFromCache() {
	var rows []elementRow
	oldest := time.Duration(0)
	missing := false
	for _, t := range m.aggregateTenants() {
		c, err := m.store.Cache(t.TSGID)
		if err != nil || !c.Usable() {
			missing = true
			continue
		}
		for _, el := range c.Elements {
			rows = append(rows, elementRow{Element: el, TSG: t.TSGID, Tenant: t.Name})
		}
		for k, v := range c.Sites {
			if m.sites == nil {
				m.sites = map[string]string{}
			}
			m.sites[k] = v
		}
		for eid, w := range c.HA {
			if m.haCached == nil {
				m.haCached = map[string]string{}
			}
			m.haCached[t.TSGID+"/"+eid] = w
		}
		if a := c.Age(); a > oldest {
			oldest = a
		}
	}
	if len(rows) == 0 {
		return
	}
	m.setElements(rows)
	m.cacheCount = len(rows)
	m.cacheAt = time.Now().Add(-oldest)
	m.cacheState = cacheCached
	if missing || oldest >= cacheFresh {
		m.cacheState = cacheUpdating
	}
}

func (m tuiModel) refreshActive() (tea.Model, tea.Cmd) {
	m.status = ""
	active := m.aggregateTenants()
	if len(active) == 0 {
		m.status = "no tenants to load — sign in, or clear an exclusion in settings"
		m.cacheState = cacheFailed
		return m, nil
	}

	cmds := []tea.Cmd{m.spinner.Tick}
	var skipped []string
	pending := 0

	for _, t := range active {
		f := app.TenantFlags(m.store, t, "")
		if err := f.ValidateCreds(); err != nil {
			skipped = append(skipped, t.Name)
			continue
		}
		pending++
		cmds = append(cmds, loadTenant(m.ctx, f, t, m.pool, m.store))
	}

	if pending == 0 {
		m.status = "no usable credentials for " + strings.Join(skipped, ", ")
		m.cacheState = cacheFailed
		return m, nil
	}
	if len(skipped) > 0 {
		m.status = "skipped " + strings.Join(skipped, ", ") + ": no usable credentials"
	}

	m.pendingTenants = pending
	m.cacheState = cacheUpdating
	return m, tea.Batch(cmds...)
}

func (m tuiModel) credsFor(tsg string) ionterm.Credentials {
	for _, t := range m.cfg.Tenants {
		if t.TSGID != tsg {
			continue
		}
		f := app.TenantFlags(m.store, t, m.runAsFor(tsg))
		return ionterm.Credentials{User: f.IONUsername, Pass: f.IONPassword}
	}
	return ionterm.Credentials{}
}

func (m tuiModel) targetsFor(rows []elementRow) []sase.Target {
	out := make([]sase.Target, 0, len(rows))
	for _, r := range rows {
		creds := m.credsFor(r.TSG)
		t := sase.Target{ElementID: r.ID, User: creds.User, Pass: creds.Pass}
		if c := m.pool.get(r.TSG); c != nil {
			t.Client = c
		}
		out = append(out, t)
	}
	return out
}

func (m tuiModel) tenantClient(tsg string) *api.Client {
	return m.pool.get(tsg)
}

func httpClientFor(f app.Flags) *http.Client {
	return &http.Client{Timeout: f.HTTPTimeout}
}

func (m *tuiModel) tenantErr(tsg, tenant string, err error) {
	reason := shortTenantErr(err)
	if m.tenantIssues == nil {
		m.tenantIssues = map[string]string{}
	}
	m.tenantIssues[tsg] = reason

	msg := tenant + ": " + reason
	if m.status == "" {
		m.status = msg
	} else if !strings.Contains(m.status, tenant) {
		m.status += "; " + msg
	}
	if app.IsAuthFailure(err.Error()) {
		m.tokenDead = "sign-in expired"
	}
}

func shortTenantErr(err error) string {
	return ui.Truncate(app.ShortAuthError(err), 90)
}

func (m tuiModel) tenantOf(tsg string) (config.Tenant, bool) {
	for _, t := range m.cfg.Tenants {
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
	for _, r := range m.all {
		if r.key() == key {
			return r, true
		}
	}
	return elementRow{}, false
}

func (m tuiModel) tsgForElement(eid string) string {
	for _, r := range m.all {
		if r.ID == eid {
			return r.TSG
		}
	}
	return m.tenant.TSGID
}

func (m tuiModel) activeSessionCreds() ionterm.Credentials {
	s := m.sessions.Active()
	if s == nil {
		return m.credsFor(m.tenant.TSGID)
	}
	return m.credsFor(m.tsgForElement(s.ElementID))
}

func (m tuiModel) scopeLabel() string {
	if m.scope != "" {
		return m.scope
	}
	if m.hasTenant {
		return m.tenant.Name
	}
	return "no tenant"
}

func (m *tuiModel) computeScope() {
	seen := map[string]string{}
	for _, r := range m.all {
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
	if m.authMode == authTenant {
		if m.hasTenant {
			return []config.Tenant{m.tenant}
		}
		return nil
	}

	added := m.cfg.AddedTenants()
	if len(added) > 0 {
		return added
	}
	if m.hasTenant {
		return []config.Tenant{m.tenant}
	}
	return nil
}

func (m tuiModel) refreshNote() string {
	return m.elementsNote() + "\n" + m.haNote()
}

func (m tuiModel) elementsNote() string {
	switch m.cacheState {
	case cacheUpdating:
		note := m.spinner.View() + " " + ui.StyleWarn.Render("refreshing elements")
		if m.pendingTenants > 1 {
			note += ui.StyleDim.Render(fmt.Sprintf(" %d tenants", m.pendingTenants))
		}
		return note
	case cacheFailed:
		return ui.StyleErr.Render("element refresh failed")
	case cacheNone:
		return ui.StyleDim.Render("no cached elements")
	}

	d := m.cacheAgeNow()
	if m.cacheState == cacheUpdated && d < cacheFresh {
		return ui.StyleOK.Render("Elements refreshed!") + ago(d)
	}
	return ui.StyleDim.Render("Elements cached") + ago(d)
}

func (m tuiModel) haNote() string {
	if m.haWorking() {
		note := m.spinner.View() + " " + ui.StyleWarn.Render("refreshing HA")
		if m.haTotal > 0 {
			note += ui.StyleDim.Render(fmt.Sprintf(" %d/%d clusters",
				min(m.haDone, m.haTotal), m.haTotal))
		}
		return note
	}
	if m.haAt.IsZero() {
		return ""
	}

	d := time.Since(m.haAt)
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

func (m tuiModel) runAsFor(tsg string) string {
	if u := m.runAs[tsg]; u != "" {
		return u
	}
	if t, ok := m.tenantOf(tsg); ok {
		return t.DefaultUser()
	}
	return ""
}

func (m *tuiModel) setRunAs(tsg, user string) {
	if m.runAs == nil {
		m.runAs = map[string]string{}
	}
	if user == "" {
		delete(m.runAs, tsg)
		return
	}
	m.runAs[tsg] = user
}

func (m tuiModel) usersPanelTenant() config.Tenant {
	if t, ok := m.tenantOf(m.usersTSG); ok {
		return t
	}
	for _, r := range m.all {
		if r.TSG == m.usersTSG {
			return config.Tenant{Name: r.Tenant, TSGID: r.TSG, Auth: config.AuthBrowser}
		}
	}
	return m.tenant
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
	for _, r := range m.all {
		if t, ok := m.tenantOf(r.TSG); ok {
			add(t)
			continue
		}
		add(config.Tenant{Name: r.Tenant, TSGID: r.TSG, Auth: config.AuthBrowser})
	}
	if m.hasTenant {
		add(m.tenant)
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
	sel := fmt.Sprintf("%d selected", len(m.selected))
	if len(m.matches) != len(m.all) {
		return fmt.Sprintf("%s / %d of %d elements", sel, len(m.matches), len(m.all))
	}
	return fmt.Sprintf("%s / %d elements", sel, len(m.all))
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
		if c := m.pool.get(tsg); c != nil {
			if c.TokenGood(preflightTTL) {
				continue
			}
			seen[tsg] = true
			cmds = append(cmds, recheckToken(m.ctx, c, tsg))
			continue
		}
		if m.tenantIssues[tsg] != "" {
			continue
		}
		seen[tsg] = true
		t, ok := m.tenantOf(tsg)
		if !ok {
			t = config.Tenant{Name: tsg, TSGID: tsg, Auth: config.AuthBrowser}
			for _, r := range m.all {
				if r.TSG == tsg {
					t.Name = r.Tenant
					break
				}
			}
		}
		f := app.TenantFlags(m.store, t, m.runAsFor(tsg))
		if err := f.ValidateCreds(); err != nil {
			continue
		}
		cmds = append(cmds, connectTenant(m.ctx, m.store, f, tsg))
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
	for _, r := range m.all {
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
	if m.selected[el.key()] {
		delete(m.selected, el.key())
	} else {
		m.selected[el.key()] = true
	}
	m.rebuildRows()
	return m
}

func (m tuiModel) selectedNote() string {
	if !m.showSelected {
		return ""
	}
	return "  " + ui.StyleOK.Render("selected only")
}
