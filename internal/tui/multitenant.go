package tui

import (
	"context"
	"errors"
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
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/ui"
)

// elementRow is an element together with the tenant it belongs to. A browser
// account can reach many tenants at once, so an element is only identified by
// the pair: nothing else says which token to dial it with.
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

// sessionTarget is an element to open, carrying its tenant so the right client
// and the right device credentials are used.
type sessionTarget struct {
	eid  string
	name string
	tsg  string
}

// clientPool holds one authenticated client per tenant, built on demand. The
// model is copied by value all over the place, so this is deliberately a
// pointer: every copy shares the same clients.
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

// tenantElementsMsg is one tenant's share of the aggregated element list.
type tenantElementsMsg struct {
	tsg      string
	tenant   string
	elements []api.Element
	sites    map[string]string
	// siteErr is reported separately: elements are still usable without their
	// site names.
	siteErr error
	err     error
}

// loadTenant authenticates a tenant if needed and fetches its elements. One of
// these runs per active tenant, so the list fills in as each answers rather
// than waiting for the slowest.
func loadTenant(ctx context.Context, f app.Flags, t config.Tenant, pool *clientPool, st *config.Store) tea.Cmd {
	return func() tea.Msg {
		fail := func(err error) tea.Msg {
			return tenantElementsMsg{tsg: t.TSGID, tenant: t.Name, err: err}
		}

		c := pool.get(t.TSGID)
		if c == nil {
			built, err := app.NewClient(ctx, f, io.Discard)
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

		// A failed site lookup is not fatal, but it must not be silent either:
		// swallowing it is why every site showed as a raw id for so long.
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
			})
		}
		return tenantElementsMsg{
			tsg: t.TSGID, tenant: t.Name,
			elements: els, sites: sites, siteErr: siteErr,
		}
	}
}

// mergeTenantElements replaces one tenant's share of the list, leaving the
// other tenants' rows alone.
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

// loadFromCache fills the list from disk so the element page is usable before
// any network call returns.
func (m *tuiModel) loadFromCache() {
	var rows []elementRow
	oldest := time.Duration(0)
	for _, t := range m.aggregateTenants() {
		c, err := m.store.Cache(t.TSGID)
		if err != nil || c == nil || len(c.Elements) == 0 {
			continue
		}
		for _, el := range c.Elements {
			rows = append(rows, elementRow{Element: el, TSG: t.TSGID, Tenant: t.Name})
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
	if oldest >= cacheFresh {
		m.cacheState = cacheUpdating
	}
}

// refreshActive fetches every active tenant. Tenants with no usable credential
// are reported rather than silently skipped.
func (m tuiModel) refreshActive() (tea.Model, tea.Cmd) {
	// Start from a clean slate: a message left from an earlier action must not
	// have this run's failures appended to it.
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

// credsFor returns the device login for a tenant. ION users are stored per
// tenant, so a session in one tenant must not be opened with another's login.
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

// targetsFor pairs selected elements with their own tenant's client, which is
// what lets one batch run span tenants.
func (m tuiModel) targetsFor(rows []elementRow) []sase.Target {
	out := make([]sase.Target, 0, len(rows))
	for _, r := range rows {
		creds := m.credsFor(r.TSG)
		t := sase.Target{ElementID: r.ID, User: creds.User, Pass: creds.Pass}
		// Assigning a nil *api.Client to the interface would produce a non-nil
		// interface holding a nil pointer, which defeats every nil check
		// downstream and panics on first use. Leave the field zero instead.
		if c := m.pool.get(r.TSG); c != nil {
			t.Client = c
		}
		out = append(out, t)
	}
	return out
}

// tenantClient hands back a tenant's client, building nothing: an element can
// only be dialled once its tenant has been loaded.
func (m tuiModel) tenantClient(tsg string) *api.Client {
	return m.pool.get(tsg)
}

func httpClientFor(f app.Flags) *http.Client {
	return &http.Client{Timeout: f.HTTPTimeout}
}

// tenantErr records a per-tenant failure without losing the other tenants'
// results. A credential problem routes to the browser the same way a single
// tenant's would.
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

// shortTenantErr trims the layers a failed sign-in accumulates. The full chain
// reads "interactive sign-in required: no existing browser session:
// authorization failed: interaction_required: ..." which buries the one fact
// that matters.
//
// A tenant the account cannot act in fails the same way a dead session does:
// the authorization server answers interaction_required either way. A dead
// session would fail every tenant at once, so the likely cause of one tenant
// failing is access, and the wording says so without claiming to be sure.
func shortTenantErr(err error) string {
	text := err.Error()
	switch {
	// A sign-in that ran out of time proves nothing about access, and it
	// arrives wrapped in ErrInteractiveRequired like a refusal does, so it has
	// to be read before the access wording below claims it.
	case strings.Contains(text, "did not complete within"):
		return "sign-in timed out"
	case errors.Is(err, saseauth.ErrInteractiveRequired),
		strings.Contains(text, "interaction_required"),
		strings.Contains(text, "login_required"):
		return "not accessible with this sign-in"
	case app.IsAuthFailure(text):
		return "credential rejected"
	}
	if i := strings.Index(text, "\n"); i > 0 {
		text = text[:i]
	}
	return ui.Truncate(text, 90)
}

// tenantOf finds the configured record for a tenant ID.
func (m tuiModel) tenantOf(tsg string) (config.Tenant, bool) {
	for _, t := range m.cfg.Tenants {
		if t.TSGID == tsg {
			return t, true
		}
	}
	return config.Tenant{}, false
}

// ionUserFor is the device login to use in a tenant.
func (m tuiModel) ionUserFor(t config.Tenant) string {
	return m.runAsFor(t.TSGID)
}

// tsgForElement finds which tenant an element belongs to. Sessions and the
// reattach list only carry an element ID, so the tenant has to be recovered
// from the loaded list; the active tenant is the fallback.
func (m tuiModel) tsgForElement(eid string) string {
	for _, r := range m.all {
		if r.ID == eid {
			return r.TSG
		}
	}
	return m.tenant.TSGID
}

// activeSessionCreds are the device credentials for whichever session is in
// front, which may belong to a different tenant than the one being browsed.
func (m tuiModel) activeSessionCreds() ionterm.Credentials {
	s := m.sessions.Active()
	if s == nil {
		return m.credsFor(m.tenant.TSGID)
	}
	return m.credsFor(m.tsgForElement(s.ElementID))
}

// scopeLabel names what the element list is showing. Naming a single tenant
// would be a lie once several are loaded.
// scopeLabel names what the element list is showing. It is computed when the
// list changes rather than per render: walking every element on each keystroke
// made holding a movement key lag badly once several tenants were loaded.
func (m tuiModel) scopeLabel() string {
	if m.scope != "" {
		return m.scope
	}
	if m.hasTenant {
		return m.tenant.Name
	}
	return "no tenant"
}

// computeScope derives the label and the tenant count from the loaded elements.
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

// aggregateTenants is the set of tenants whose elements make up the list.
//
// Browser mode spans the whole working set, which is what makes searching
// across tenants possible: the set is chosen from the discovered tenants when
// signing in, and changed in settings. Tenant mode is a single service
// account, so it spans that alone.
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
	// Nothing picked yet: the current tenant is the list, so a fresh setup
	// still works before visiting the picker.
	if m.hasTenant {
		return []config.Tenant{m.tenant}
	}
	return nil
}

// refreshNote is the moving "refreshing" marker beside the element count. An
// element refresh had no visible feedback at all before: the list simply sat
// there until it changed.
func (m tuiModel) refreshNote() string {
	switch m.cacheState {
	case cacheUpdating:
		note := "  " + m.spinner.View() + " " + ui.StyleWarn.Render("refreshing")
		if m.pendingTenants > 1 {
			note += ui.StyleDim.Render(fmt.Sprintf(" %d tenants", m.pendingTenants))
		}
		return note
	case cacheFailed:
		return "  " + ui.StyleErr.Render("refresh failed")
	}
	return ""
}

// runAsFor is the device login chosen for a tenant: an explicit choice made in
// the users panel, otherwise that tenant's default. Each tenant keeps its own,
// because one account can reach tenants with entirely different ION logins.
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

// usersPanelTenant is the tenant whose logins the users panel is managing.
func (m tuiModel) usersPanelTenant() config.Tenant {
	if t, ok := m.tenantOf(m.usersTSG); ok {
		return t
	}
	// A tenant in the list but not yet recorded still needs naming, so the
	// panel can offer to add its first user.
	for _, r := range m.all {
		if r.TSG == m.usersTSG {
			return config.Tenant{Name: r.Tenant, TSGID: r.TSG, Auth: config.AuthBrowser}
		}
	}
	return m.tenant
}

// usersPanelTenants are the tenants the panel can cycle through: everything
// loaded in the list, plus the current one.
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

// cycleUsersTenant moves the panel to the next or previous tenant.
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

// countLabel says how many elements are in view. With a filter running it also
// says how many there are in total, so a narrow search does not look like a
// short list.
func (m tuiModel) countLabel() string {
	sel := fmt.Sprintf("%d selected", len(m.selected))
	if len(m.matches) != len(m.all) {
		return fmt.Sprintf("%s / %d of %d elements", sel, len(m.matches), len(m.all))
	}
	return fmt.Sprintf("%s / %d elements", sel, len(m.all))
}

// tenantConnectedMsg is a tenant that has just authenticated.
type tenantConnectedMsg struct {
	tsg    string
	client *api.Client
	err    error
}

// connectTenant authenticates one tenant without fetching anything.
func connectTenant(ctx context.Context, f app.Flags, tsg string) tea.Cmd {
	return func() tea.Msg {
		c, err := app.NewClient(ctx, f, io.Discard)
		return tenantConnectedMsg{tsg: tsg, client: c, err: err}
	}
}

// ensureClients returns the commands needed to authenticate any of these
// tenants that has no client yet.
//
// A tenant only had a client as a side effect of fetching its elements, so a
// launch that found a fresh cache had none at all: every element then looked
// like it belonged to a disconnected tenant. Connecting on demand is what makes
// a cached list usable.
func (m tuiModel) ensureClients(tsgs []string) []tea.Cmd {
	seen := map[string]bool{}
	var cmds []tea.Cmd
	for _, tsg := range tsgs {
		if tsg == "" || seen[tsg] || m.pool.get(tsg) != nil {
			continue
		}
		// A tenant that already failed this round is not tried again: the
		// replay would otherwise connect, fail, replay, forever.
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
		cmds = append(cmds, connectTenant(m.ctx, f, tsg))
	}
	return cmds
}

// tsgsOf is the distinct tenants a set of targets touches.
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

// tsgsOfRows is the distinct tenants a set of elements touches.
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

// tenantName is a tenant's display name, falling back to its ID.
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

// toggleCurrent selects or deselects the highlighted element.
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

// selectedNote marks the list when it has been narrowed to the selection, so
// that view is never mistaken for the whole list.
func (m tuiModel) selectedNote() string {
	if !m.showSelected {
		return ""
	}
	return "  " + ui.StyleOK.Render("selected only")
}
