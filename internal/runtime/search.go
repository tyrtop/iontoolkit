package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/search"
)

type TenantLoad struct {
	Tenant string
	Count  int
	Note   string
	Err    error
	Warn   bool
	Done   bool
}

type Hit struct {
	Element api.Element
	Tenant  config.Tenant
	Record  search.Record
}

type Found struct {
	Hits   []Hit
	SignIn []config.Tenant
	Failed []string
	asked  int
}

func (rt *Runtime) SearchScope(name, tsg string) ([]config.Tenant, error) {
	cfg := rt.Config()
	if cfg == nil {
		return nil, fmt.Errorf("no config to search from")
	}
	if name != "" {
		t, ok := cfg.Tenant(name)
		if !ok {
			return nil, fmt.Errorf("no saved tenant named %q", name)
		}
		return []config.Tenant{t}, nil
	}
	scope := cfg.AddedTenants()
	if len(scope) == 0 {
		t, ok := cfg.Resolve("")
		if !ok {
			t, ok = rt.Tenant()
		}
		if !ok {
			return nil, fmt.Errorf("no tenants to search")
		}
		scope = []config.Tenant{t}
	}
	if tsg == "" {
		return scope, nil
	}
	var kept []config.Tenant
	for _, t := range scope {
		if t.TSGID == tsg {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("--tsg %s is not in the working set (%s)", tsg, TenantNames(scope))
	}
	return kept, nil
}

func TenantNames(ts []config.Tenant) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

func (rt *Runtime) Search(ctx context.Context, tenants []config.Tenant, q search.Query, refresh bool, progress chan<- TenantLoad) Found {
	loads := make([]Load, len(tenants))
	var wg sync.WaitGroup
	for i, t := range tenants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if progress != nil {
				progress <- TenantLoad{Tenant: t.Name}
			}
			l := rt.LoadTenant(ctx, t, refresh)
			loads[i] = l
			if progress != nil {
				progress <- TenantLoad{Tenant: t.Name, Count: len(l.Elements), Note: l.Note,
					Err: l.Err, Warn: l.Stale != nil, Done: true}
			}
		}()
	}
	wg.Wait()

	f := Found{asked: len(tenants)}
	for i, l := range loads {
		t := tenants[i]
		if l.WantsSignIn() {
			f.SignIn = append(f.SignIn, t)
		}
		if l.Err != nil {
			f.Failed = append(f.Failed, t.Name+": "+rt.explain(l.Err))
			continue
		}
		for _, el := range l.Elements {
			rec := search.Of(el, t.TSGID, t.Name, search.SiteMap(l.Sites))
			if q.Match(rec) {
				f.Hits = append(f.Hits, Hit{Element: el, Tenant: t, Record: rec})
			}
		}
	}
	f.sort()
	return f
}

func (l Load) WantsSignIn() bool {
	return errors.Is(l.Err, saseauth.ErrInteractiveRequired) || errors.Is(l.Stale, saseauth.ErrInteractiveRequired)
}

func (f Found) Err() error {
	if f.asked > 0 && len(f.Failed) == f.asked {
		return fmt.Errorf("no tenant could be searched:\n  %s", strings.Join(f.Failed, "\n  "))
	}
	return nil
}

func (f Found) Rows() []Element {
	rows := make([]Element, 0, len(f.Hits))
	for _, h := range f.Hits {
		rows = append(rows, Element{Element: h.Element, TSG: h.Tenant.TSGID, Tenant: h.Tenant.Name})
	}
	return rows
}

func (f *Found) sort() {
	sort.Slice(f.Hits, func(i, j int) bool {
		a, b := f.Hits[i], f.Hits[j]
		if a.Record.Name != b.Record.Name {
			return a.Record.Name < b.Record.Name
		}
		return a.Tenant.TSGID < b.Tenant.TSGID
	})
}
