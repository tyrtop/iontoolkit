package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

type Connector struct {
	Check   func(st *config.Store, t config.Tenant) error
	Connect func(ctx context.Context, st *config.Store, t config.Tenant) (*api.Client, error)
	Explain func(err error) string
}

func (rt *Runtime) UseConnector(c Connector) {
	rt.mu.Lock()
	rt.conn = c
	rt.mu.Unlock()
}

type ListPaths struct {
	Elements string
	Sites    string
}

func (rt *Runtime) SetListPaths(p ListPaths) {
	rt.mu.Lock()
	rt.paths = p
	rt.mu.Unlock()
}

func (rt *Runtime) ListPaths() ListPaths {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.paths
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

func (rt *Runtime) explain(err error) string {
	rt.mu.RLock()
	fn := rt.conn.Explain
	rt.mu.RUnlock()
	if fn == nil {
		return err.Error()
	}
	return fn(err)
}

func (rt *Runtime) Connect(ctx context.Context, t config.Tenant) (*api.Client, error) {
	if c := rt.Clients().Get(t.TSGID); c != nil {
		return c, nil
	}
	rt.mu.RLock()
	conn, st := rt.conn, rt.store
	rt.mu.RUnlock()
	if conn.Connect == nil {
		return nil, fmt.Errorf("%s: no way to sign in", t.Name)
	}
	c, err := conn.Connect(ctx, st, t)
	if err != nil {
		return nil, err
	}
	rt.Clients().Set(t.TSGID, c)
	return c, nil
}

type Load struct {
	TSG      string
	Tenant   string
	Elements []api.Element
	Sites    map[string]string
	SiteErr  error
	Err      error

	Note  string
	Stale error
}

func (rt *Runtime) FetchTenant(ctx context.Context, t config.Tenant) Load {
	l := Load{TSG: t.TSGID, Tenant: t.Name}
	rt.mu.RLock()
	paths, st := rt.paths, rt.store
	rt.mu.RUnlock()

	c, err := rt.Connect(ctx, t)
	if err != nil {
		l.Err = err
		return l
	}

	els, err := c.ListElementsFrom(ctx, paths.Elements)
	if err != nil {
		l.Err = err
		return l
	}
	l.Elements, l.Sites, l.Note = els, map[string]string{}, "refreshed"
	ss, siteErr := c.ListSitesFrom(ctx, paths.Sites)
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
	rt.mu.RLock()
	check := rt.conn.Check
	rt.mu.RUnlock()
	var l Load
	if check != nil {
		if err := check(st, t); err != nil {
			l = Load{TSG: t.TSGID, Tenant: t.Name, Err: err}
		}
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
	rt.mu.RLock()
	check, st := rt.conn.Check, rt.store
	rt.mu.RUnlock()
	for _, t := range tenants {
		if check != nil {
			if err := check(st, t); err != nil {
				skipped = append(skipped, t.Name)
				continue
			}
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
			Name: "fleet.refresh", Slow: true, Help: "reload every tenant's elements from the portal and save them to the cache",
			Run: func(rt *Runtime, c *Call) error {
				tenants, skipped, err := rt.BeginRefresh()
				if len(skipped) > 0 {
					c.Say("skipped %s: no usable credentials", strings.Join(skipped, ", "))
				}
				if err != nil {
					return err
				}
				loads := make([]Load, len(tenants))
				var wg sync.WaitGroup
				for i, t := range tenants {
					wg.Add(1)
					go func() {
						defer wg.Done()
						loads[i] = rt.FetchTenant(c.Ctx, t)
					}()
				}
				wg.Wait()
				failed := 0
				for _, l := range loads {
					rt.ApplyLoad(l)
					if l.Err != nil {
						failed++
						c.Say("%s: %s", l.Tenant, rt.explain(l.Err))
						continue
					}
					c.Say("%s: %d elements", l.Tenant, len(l.Elements))
				}
				if failed > 0 {
					return fmt.Errorf("%d of %d tenants failed to load", failed, len(loads))
				}
				return nil
			},
		},
		{
			Name: "fleet.load", Slow: true,
			Help: "fill the list, each tenant from its cache while fresh, else the portal, a stale cache standing in when it refuses: refresh=yes asks the portal first",
			Args: []Arg{{Name: "refresh", Bool: true, Help: "ask the portal first"}},
			Run: func(rt *Runtime, c *Call) error {
				tenants := rt.FleetTenants()
				if len(tenants) == 0 {
					return fmt.Errorf("no tenants to load — sign in, or clear an exclusion in settings")
				}
				rt.editFleet(func(f *fleetState) { f.pending, f.state = len(tenants), FleetUpdating })
				done := make(chan Load, len(tenants))
				for _, t := range tenants {
					go func() { done <- rt.LoadTenant(c.Ctx, t, c.Arg("refresh") == "yes") }()
				}
				var loads []Load
				var failed []string
				for range tenants {
					l := <-done
					rt.ApplyLoad(l)
					loads = append(loads, l)
					if l.Err != nil {
						failed = append(failed, l.Tenant+": "+rt.explain(l.Err))
						c.Say("%s: unreachable, %s", l.Tenant, rt.explain(l.Err))
						continue
					}
					c.Say("%s: %d elements %s", l.Tenant, len(l.Elements), l.Note)
				}
				c.Result = loads
				if len(failed) == len(tenants) {
					return fmt.Errorf("no tenant could be loaded:\n  %s", strings.Join(failed, "\n  "))
				}
				return nil
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
	}
}
