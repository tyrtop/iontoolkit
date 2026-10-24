package runtime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

const AreaAuth = "auth"

type authState struct {
	ident   saseauth.Identity
	profile api.Profile
	ttl     time.Duration
	dead    string
	browser bool

	discovered []saseauth.Tenant
	picked     map[string]bool
	warming    int
}

func (rt *Runtime) editAuth(edit func(a *authState)) {
	rt.mu.Lock()
	edit(&rt.auth)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaAuth})
}

func (rt *Runtime) readAuth() authState {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.auth
}

func (rt *Runtime) Identity() saseauth.Identity { return rt.readAuth().ident }
func (rt *Runtime) Profile() api.Profile        { return rt.readAuth().profile }
func (rt *Runtime) TokenTTL() time.Duration     { return rt.readAuth().ttl }

func (rt *Runtime) TokenDead() string { return rt.readAuth().dead }

func (rt *Runtime) BrowserSignedIn() bool { return rt.readAuth().browser }

func (rt *Runtime) SetIdentity(i saseauth.Identity) {
	rt.editAuth(func(a *authState) { a.ident = i })
}

func (rt *Runtime) SignedIn(i saseauth.Identity, p api.Profile, ttl time.Duration) {
	rt.editAuth(func(a *authState) { a.ident, a.profile, a.ttl, a.dead = i, p, ttl, "" })
}

func (rt *Runtime) TokenRenewed(i saseauth.Identity, ttl time.Duration) {
	rt.editAuth(func(a *authState) { a.ident, a.ttl, a.dead = i, ttl, "" })
}

func (rt *Runtime) MarkBrowserSignedIn() {
	rt.editAuth(func(a *authState) { a.browser = true })
}

func (rt *Runtime) TokenDied(why string, keep bool) {
	rt.editAuth(func(a *authState) {
		if !keep || a.dead == "" {
			a.dead = why
		}
	})
}

func (rt *Runtime) TokenAlive() {
	rt.editAuth(func(a *authState) { a.dead = "" })
}

func (rt *Runtime) ForgetAuth() {
	rt.editAuth(func(a *authState) { *a = authState{} })
}

func (rt *Runtime) Discovered() []saseauth.Tenant {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return append([]saseauth.Tenant(nil), rt.auth.discovered...)
}

func (rt *Runtime) SetDiscovered(ts []saseauth.Tenant) {
	cfg := rt.Config()
	rt.editAuth(func(a *authState) {
		a.discovered, a.picked = ts, map[string]bool{}
		for _, t := range ts {
			if cfg != nil && cfg.IsAdded(t.ID) {
				a.picked[t.ID] = true
			}
		}
	})
}

func (rt *Runtime) Picked(tsg string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.auth.picked[tsg]
}

func (rt *Runtime) PickedCount() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.auth.picked)
}

func (rt *Runtime) TogglePick(tsg string) {
	rt.editAuth(func(a *authState) {
		if a.picked == nil {
			a.picked = map[string]bool{}
		}
		if a.picked[tsg] {
			delete(a.picked, tsg)
		} else {
			a.picked[tsg] = true
		}
	})
}

func (rt *Runtime) PickAll() {
	rt.editAuth(func(a *authState) {
		all := len(a.picked) == len(a.discovered)
		a.picked = map[string]bool{}
		if !all {
			for _, t := range a.discovered {
				a.picked[t.ID] = true
			}
		}
	})
}

func (rt *Runtime) AddPicked() error {
	cfg := rt.Config()
	if cfg == nil {
		return fmt.Errorf("no config")
	}
	if rt.PickedCount() == 0 {
		return fmt.Errorf("pick at least one tenant")
	}
	found := rt.Discovered()
	var first config.Tenant
	for _, d := range found {
		existing, has := cfg.Tenant(d.Name)
		rec := existing
		if !has || existing.TSGID != d.ID {
			rec = config.Tenant{Name: d.Name, TSGID: d.ID, Auth: config.AuthBrowser}
		}
		rec.Added = rt.Picked(d.ID)
		if !rec.Added && !has {
			continue
		}
		cfg.Upsert(rec)
		if rec.Added && first.TSGID == "" {
			first = rec
		}
	}
	if first.TSGID == "" {
		return fmt.Errorf("pick at least one tenant")
	}
	if cur, _ := rt.Tenant(); !cfg.IsAdded(cur.TSGID) {
		cfg.LastTenant = first.Name
		rt.UseTenant(first)
		rt.SetIONUser(first.DefaultUser())
	}
	if err := rt.saveConfig(); err != nil {
		return err
	}
	rt.SetElements(nil)
	rt.ForgetSelection()
	rt.editAuth(func(a *authState) { a.discovered, a.picked = nil, nil })
	return nil
}

func (rt *Runtime) UseBrowserTenant(name, tsg string) (config.Tenant, error) {
	cfg := rt.Config()
	if cfg == nil {
		return config.Tenant{}, fmt.Errorf("no config")
	}
	t, ok := cfg.Tenant(name)
	if !ok || t.TSGID != tsg {
		t = config.Tenant{Name: name, TSGID: tsg, Auth: config.AuthBrowser}
	}
	t.Added = true
	cfg.Upsert(t)
	if err := rt.SwitchTenant(t); err != nil {
		return t, err
	}
	rt.Clients().Drop(tsg)
	rt.TokenAlive()
	return t, nil
}

func (rt *Runtime) Warming() int { return rt.readAuth().warming }

func (rt *Runtime) SetWarming(n int) {
	rt.editAuth(func(a *authState) { a.warming = n })
}

type KnownTenant struct {
	Name           string
	TSG            string
	Known          bool
	Saved          bool
	ServiceAccount bool
	Auth           string
}

func (rt *Runtime) KnownTenants() []KnownTenant {
	key := func(tsg, name string) string {
		if tsg != "" {
			return "tsg:" + tsg
		}
		return "name:" + name
	}
	merged := map[string]KnownTenant{}
	if st := rt.Store(); st != nil {
		if c, err := st.TenantCache(); err == nil && c != nil {
			for _, t := range c.Tenants {
				merged[key(t.TSGID, t.Name)] = KnownTenant{Name: t.Name, TSG: t.TSGID, Known: true}
			}
		}
	}
	if cfg := rt.Config(); cfg != nil {
		for _, t := range cfg.Tenants {
			k := key(t.TSGID, t.Name)
			cur := merged[k]
			cur.Name, cur.TSG, cur.Saved, cur.Auth = t.Name, t.TSGID, true, t.Auth
			cur.ServiceAccount = !t.UsesBrowser()
			merged[k] = cur
		}
	}
	out := make([]KnownTenant, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
