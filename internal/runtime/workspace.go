package runtime

import "tyrtop.com/iontk/internal/config"

const (
	AuthBrowser = "browser"
	AuthTenant  = "tenant"

	AreaWorkspace = "workspace"
)

type workspace struct {
	tenant  config.Tenant
	has     bool
	ionUser string
	auth    string
	runAs   map[string]string

	removed []config.Tenant

	resolve func(*config.Config, string) (config.Tenant, bool)
}

func (rt *Runtime) Tenant() (config.Tenant, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.ws.tenant, rt.ws.has
}

func (rt *Runtime) UseTenant(t config.Tenant) {
	rt.editWorkspace(func(w *workspace) { w.tenant, w.has = t, true })
}

func (rt *Runtime) ReplaceTenant(t config.Tenant) {
	rt.editWorkspace(func(w *workspace) { w.tenant = t })
}

func (rt *Runtime) ClearTenant() {
	rt.editWorkspace(func(w *workspace) { w.tenant, w.has, w.ionUser = config.Tenant{}, false, "" })
}

func (rt *Runtime) IONUser() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.ws.ionUser
}

func (rt *Runtime) SetIONUser(u string) {
	rt.editWorkspace(func(w *workspace) { w.ionUser = u })
}

func (rt *Runtime) AuthMode() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.ws.auth == "" {
		return AuthBrowser
	}
	return rt.ws.auth
}

func (rt *Runtime) SetAuthMode(mode string) {
	rt.editWorkspace(func(w *workspace) { w.auth = mode })
}

func (rt *Runtime) editWorkspace(edit func(w *workspace)) {
	rt.mu.Lock()
	edit(&rt.ws)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaWorkspace})
}
