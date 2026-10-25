package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/search"
)

type Connector struct {
	Check   func(st *config.Store, t config.Tenant) error
	Connect func(ctx context.Context, st *config.Store, t config.Tenant) (*api.Client, error)
	Explain func(err error) string
	Refused func(err error) bool
}

func (rt *Runtime) UseConnector(c Connector) {
	rt.mu.Lock()
	rt.conn = c
	rt.mu.Unlock()
}

func (rt *Runtime) SetListCalls(calls *endpoint.Set) {
	rt.mu.Lock()
	rt.listCalls = calls
	rt.mu.Unlock()
}

func (rt *Runtime) ListPath(name string, v map[string]string) (string, error) {
	rt.mu.RLock()
	calls := rt.listCalls
	rt.mu.RUnlock()
	if calls == nil {
		return "", fmt.Errorf("the element list declares no endpoints")
	}
	e, err := calls.SDWAN(name)
	if err != nil {
		return "", fmt.Errorf("the element list's file: %w", err)
	}
	return e.Fill(v)
}

func (rt *Runtime) PreferAdded(on bool) {
	rt.mu.Lock()
	rt.preferAdded = on
	rt.mu.Unlock()
}

func (rt *Runtime) FleetTenants() []config.Tenant {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var added []config.Tenant
	if rt.cfg != nil {
		added = rt.cfg.AddedTenants()
	}
	if rt.preferAdded && len(added) > 0 {
		return added
	}
	if rt.ws.auth == AuthTenant {
		if rt.ws.has {
			return []config.Tenant{rt.ws.tenant}
		}
		return nil
	}
	if len(added) > 0 {
		return added
	}
	if rt.ws.has {
		return []config.Tenant{rt.ws.tenant}
	}
	return nil
}

func (rt *Runtime) CheckTenant(t config.Tenant) error {
	rt.mu.RLock()
	check, st := rt.conn.Check, rt.store
	rt.mu.RUnlock()
	if check == nil {
		return nil
	}
	return check(st, t)
}

func (rt *Runtime) explain(err error) string {
	rt.mu.RLock()
	fn := rt.conn.Explain
	rt.mu.RUnlock()
	if fn == nil {
		return err.Error()
	}
	return fn(err)
}

const preflightTTL = 90 * time.Second

func (rt *Runtime) Connect(ctx context.Context, t config.Tenant) (*api.Client, error) {
	lock, _ := rt.connecting.LoadOrStore(t.TSGID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	if c := rt.Clients().Get(t.TSGID); c != nil {
		if c.TokenGood(preflightTTL) {
			return c, nil
		}
		if err := c.EnsureToken(ctx, preflightTTL); err != nil {
			return nil, err
		}
		return c, nil
	}
	rt.mu.RLock()
	conn, st := rt.conn, rt.store
	rt.mu.RUnlock()
	if conn.Connect == nil {
		return nil, fmt.Errorf("%s: no way to sign in", t.Name)
	}
	if err := rt.CheckTenant(t); err != nil {
		return nil, rt.connectFailed(t, err)
	}
	c, err := conn.Connect(context.WithoutCancel(ctx), st, t)
	if err != nil {
		return nil, rt.connectFailed(t, err)
	}
	rt.Clients().Set(t.TSGID, c)
	return c, nil
}

func (rt *Runtime) connectFailed(t config.Tenant, err error) error {
	rt.SetIssue(t.TSGID, rt.explain(err))
	if errors.Is(err, saseauth.ErrInteractiveRequired) {
		rt.signInLost(t.TSGID, "sign-in expired", false)
	}
	return err
}

func (rt *Runtime) Refused(tsg string, err error) bool {
	rt.mu.RLock()
	refused := rt.conn.Refused
	rt.mu.RUnlock()
	if refused == nil || !refused(err) {
		return false
	}
	rt.signInLost(tsg, "sign-in expired", false)
	return true
}

func (rt *Runtime) signInLost(tsg, why string, keep bool) {
	if cur, ok := rt.Tenant(); ok && cur.TSGID == tsg {
		rt.TokenDied(why, keep)
		return
	}
	rt.SetIssue(tsg, why)
}

func (rt *Runtime) TenantFor(tsg string) config.Tenant {
	if t, ok := rt.TenantOf(tsg); ok {
		return t
	}
	t := config.Tenant{Name: tsg, TSGID: tsg, Auth: config.AuthBrowser}
	for _, r := range rt.Elements() {
		if r.TSG == tsg {
			t.Name = r.Tenant
			break
		}
	}
	return t
}

func (rt *Runtime) NeedsConnect(tsg string) bool {
	if tsg == "" {
		return false
	}
	if c := rt.Clients().Get(tsg); c != nil {
		return !c.TokenGood(preflightTTL)
	}
	if rt.Fleet().Issues[tsg] != "" {
		return false
	}
	return rt.CheckTenant(rt.TenantFor(tsg)) == nil
}

type Load struct {
	TSG      string
	Tenant   string
	Elements []api.Element
	Sites    map[string]string
	SiteErr  error
	Err      error

	Note    string
	Stale   error
	Fetched bool
}

func (rt *Runtime) FetchTenant(ctx context.Context, t config.Tenant) Load {
	l := Load{TSG: t.TSGID, Tenant: t.Name}
	rt.mu.RLock()
	st := rt.store
	rt.mu.RUnlock()

	c, err := rt.Connect(ctx, t)
	if err != nil {
		l.Err = err
		return l
	}

	path, err := rt.ListPath(column.ElementsSrc, nil)
	if err != nil {
		l.Err = err
		return l
	}
	els, err := c.ListElementsFrom(ctx, path)
	if err != nil {
		l.Err = err
		return l
	}
	l.Elements, l.Sites, l.Note, l.Fetched = els, map[string]string{}, "refreshed", true
	var ss []api.Site
	path, siteErr := rt.ListPath(column.SitesSrc, nil)
	if siteErr == nil {
		ss, siteErr = c.ListSitesFrom(ctx, path)
	}
	l.SiteErr = siteErr
	for _, s := range ss {
		l.Sites[t.TSGID+"/"+s.ID] = s.Name
	}
	if siteErr != nil && st != nil {
		if cached, err := st.Cache(t.TSGID); err == nil && cached != nil {
			l.Sites = cached.Sites
		}
	}
	if st != nil {
		_ = st.SaveCache(&config.ElementCache{TSGID: t.TSGID, FetchedAt: time.Now(), Elements: els, Sites: l.Sites})
	}
	return l
}

func (rt *Runtime) LoadTenant(ctx context.Context, t config.Tenant, refresh bool) Load {
	st := rt.Store()
	var cached *config.ElementCache
	if st != nil {
		if c, err := st.Cache(t.TSGID); err == nil {
			cached = c
		}
	}
	if !refresh && cached.Usable() && !cached.Stale(config.CacheFresh) {
		return Load{TSG: t.TSGID, Tenant: t.Name, Elements: cached.Elements, Sites: cached.Sites,
			Note: "from the cache, " + age(cached.Age()) + " old"}
	}
	var l Load
	if err := rt.CheckTenant(t); err != nil {
		l = Load{TSG: t.TSGID, Tenant: t.Name, Err: err}
	}
	if l.Err == nil {
		l = rt.FetchTenant(ctx, t)
	}
	if l.Err != nil && cached.Usable() {
		return Load{TSG: t.TSGID, Tenant: t.Name, Elements: cached.Elements, Sites: cached.Sites, Stale: l.Err,
			Note: fmt.Sprintf("from a stale cache %s old (%s)", age(cached.Age()), rt.explain(l.Err))}
	}
	return l
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func (rt *Runtime) ApplyLoad(l Load) int {
	left := rt.DonePending()
	if l.Err != nil {
		rt.SetIssue(l.TSG, rt.explain(l.Err))
		rt.note(l.Tenant + ": " + rt.explain(l.Err))
		rt.Refused(l.TSG, l.Err)
	} else {
		rt.ClearIssue(l.TSG)
		if l.Stale != nil {
			rt.SetIssue(l.TSG, rt.explain(l.Stale))
		}
		rt.MergeSites(l.Sites)
		rt.MergeTenant(l.TSG, l.Tenant, l.Elements)
	}
	if left == 0 {
		n := len(rt.Elements())
		rt.editFleet(func(f *fleetState) {
			f.count, f.at, f.state = n, time.Now(), FleetUpdated
			if n == 0 {
				f.state = FleetFailed
			}
		})
	}
	return left
}

func (rt *Runtime) MergeTenant(tsg, tenant string, els []api.Element) {
	cur := rt.Elements()
	kept := make([]Element, 0, len(cur)+len(els))
	for _, r := range cur {
		if r.TSG != tsg {
			kept = append(kept, r)
		}
	}
	for _, el := range els {
		kept = append(kept, Element{Element: el, TSG: tsg, Tenant: tenant})
	}
	rt.SetElements(kept)
}

func (rt *Runtime) BeginRefresh() (usable []config.Tenant, skipped []string, err error) {
	tenants := rt.FleetTenants()
	if len(tenants) == 0 {
		rt.SetFleetState(FleetFailed)
		return nil, nil, fmt.Errorf("no tenants to load — sign in, or clear an exclusion in settings")
	}
	for _, t := range tenants {
		if err := rt.CheckTenant(t); err != nil {
			skipped = append(skipped, t.Name)
			continue
		}
		usable = append(usable, t)
	}
	if len(usable) == 0 {
		rt.SetFleetState(FleetFailed)
		return nil, skipped, fmt.Errorf("no usable credentials for %s", strings.Join(skipped, ", "))
	}
	rt.editFleet(func(f *fleetState) { f.pending, f.state = len(usable), FleetUpdating })
	return usable, skipped, nil
}

func (rt *Runtime) RefreshTenants(c *Call, tenants []config.Tenant, follow bool) (wait func(), err error) {
	ctx, stop, gen := rt.sweeping(c.Ctx)
	defer stop()
	done := make(chan Load, len(tenants))
	for _, t := range tenants {
		go func() { done <- rt.FetchTenant(ctx, t) }()
	}
	var waits []func()
	failed := 0
	for range tenants {
		l := <-done
		if rt.sweptSince(gen) {
			continue
		}
		rt.ApplyLoad(l)
		if follow {
			waits = append(waits, rt.follow(c.Ctx, l, gen))
		}
		if l.Err != nil {
			failed++
			c.Say("%s: %s", l.Tenant, rt.explain(l.Err))
			continue
		}
		c.Say("%s: %d elements", l.Tenant, len(l.Elements))
	}
	wait = func() {
		for _, w := range waits {
			w()
		}
	}
	if failed > 0 {
		return wait, fmt.Errorf("%d of %d tenants failed to load", failed, len(tenants))
	}
	return wait, nil
}

func (rt *Runtime) LoadTenants(c *Call, refresh, follow bool) (wait func(), err error) {
	tenants := rt.FleetTenants()
	if len(tenants) == 0 {
		return func() {}, fmt.Errorf("no tenants to load — sign in, or clear an exclusion in settings")
	}
	rt.editFleet(func(f *fleetState) { f.pending, f.state = len(tenants), FleetUpdating })
	ctx, stop, gen := rt.sweeping(c.Ctx)
	defer stop()
	done := make(chan Load, len(tenants))
	for _, t := range tenants {
		go func() { done <- rt.LoadTenant(ctx, t, refresh) }()
	}
	var loads []Load
	var failed []string
	var waits []func()
	for range tenants {
		l := <-done
		if rt.sweptSince(gen) {
			continue
		}
		rt.ApplyLoad(l)
		loads = append(loads, l)
		if follow && l.Fetched {
			waits = append(waits, rt.follow(c.Ctx, l, gen))
		}
		if l.Err != nil {
			failed = append(failed, l.Tenant+": "+rt.explain(l.Err))
			c.Say("%s: unreachable, %s", l.Tenant, rt.explain(l.Err))
			continue
		}
		c.Say("%s: %d elements %s", l.Tenant, len(l.Elements), l.Note)
	}
	c.Result = loads
	wait = func() {
		for _, w := range waits {
			w()
		}
	}
	if len(failed) == len(tenants) {
		return wait, fmt.Errorf("no tenant could be loaded:\n  %s", strings.Join(failed, "\n  "))
	}
	return wait, nil
}

func (rt *Runtime) PeekCache(tsg string) {
	state := FleetUpdating
	if st := rt.Store(); st != nil {
		if c, err := st.Cache(tsg); err == nil && c != nil && len(c.Elements) > 0 {
			rt.SetFleetCount(len(c.Elements))
			rt.SetFleetAt(time.Now().Add(-c.Age()))
			if c.Age() < config.CacheFresh {
				state = FleetCached
			}
		}
	}
	rt.SetFleetState(state)
}

func (rt *Runtime) LoadCache() (rows int, oldest time.Duration, missing bool) {
	st := rt.Store()
	if st == nil {
		return 0, 0, true
	}
	var all []Element
	for _, t := range rt.FleetTenants() {
		c, err := st.Cache(t.TSGID)
		if err != nil || !c.Usable() {
			missing = true
			continue
		}
		for _, el := range c.Elements {
			all = append(all, Element{Element: el, TSG: t.TSGID, Tenant: t.Name})
		}
		rt.MergeSites(c.Sites)
		rt.SetCachedCells(t.TSGID, c.Cells)
		if len(c.HA) > 0 {
			ha := make(map[string]string, len(c.HA))
			for eid, w := range c.HA {
				ha[t.TSGID+"/"+eid] = w
			}
			rt.SetCachedHA(ha)
		}
		if a := c.Age(); a > oldest {
			oldest = a
		}
	}
	if len(all) == 0 {
		return 0, oldest, missing
	}
	rt.SetElements(all)
	rt.editFleet(func(f *fleetState) {
		f.count, f.at, f.state = len(all), time.Now().Add(-oldest), FleetCached
		if missing || oldest >= config.CacheFresh {
			f.state = FleetUpdating
		}
	})
	return len(all), oldest, missing
}

func fleetCommands() []Command {
	return []Command{
		{
			Name: "fleet.refresh", Slow: true,
			Help: "reload every tenant's elements from the portal and save them to the cache: follow=yes goes on to read HA and the columns",
			Args: []Arg{{Name: "follow", Bool: true, Help: "read HA and the columns after each tenant loads"}},
			Run: func(rt *Runtime, c *Call) error {
				tenants, skipped, err := rt.BeginRefresh()
				if len(skipped) > 0 {
					c.Say("skipped %s: no usable credentials", strings.Join(skipped, ", "))
				}
				if err != nil {
					return err
				}
				follow := c.Arg("follow") == "yes"
				wait, err := rt.RefreshTenants(c, tenants, follow)
				if follow {
					c.Say("reading HA and the columns")
					wait()
				}
				return err
			},
		},
		{
			Name: "fleet.load", Slow: true,
			Help: "fill the list, each tenant from its cache while fresh, else the portal, a stale cache standing in when it refuses: refresh=yes asks the portal first, follow=yes goes on to read HA and the columns",
			Args: []Arg{{Name: "refresh", Bool: true, Help: "ask the portal first"},
				{Name: "follow", Bool: true, Help: "read HA and the columns of what the portal sent"}},
			Run: func(rt *Runtime, c *Call) error {
				follow := c.Arg("follow") == "yes"
				wait, err := rt.LoadTenants(c, c.Arg("refresh") == "yes", follow)
				if follow {
					wait()
				}
				return err
			},
		},
		{
			Name: "fleet.cache", Help: "fill the element list from what is saved, without asking the portal",
			Run: func(rt *Runtime, c *Call) error {
				n, oldest, missing := rt.LoadCache()
				if n == 0 {
					return fmt.Errorf("no cached elements for any tenant")
				}
				note := ""
				if missing {
					note = ", some tenants have none"
				}
				c.Say("%d elements, the oldest %s old%s", n, oldest.Round(time.Second), note)
				return nil
			},
		},
		{
			Name: "fleet.search", Slow: true, Help: "the elements a query matches across the working set: query= tenant= refresh=yes",
			Args: []Arg{{Name: "query", Required: true}, {Name: "tenant"}, {Name: "refresh", Bool: true}},
			Run: func(rt *Runtime, c *Call) error {
				tenants, err := rt.SearchScope(c.Arg("tenant"), "")
				if err != nil {
					return err
				}
				found := rt.Search(c.Ctx, tenants, search.Parse(c.Arg("query")), c.Arg("refresh") == "yes", nil)
				if err := found.Err(); err != nil {
					return err
				}
				for _, s := range found.Failed {
					c.Say("%s", s)
				}
				for _, h := range found.Hits {
					c.Say("%s  %s  %s", h.Record.Name, h.Element.ID, h.Tenant.Name)
				}
				c.Result = found.Rows()
				return nil
			},
		},
	}
}
