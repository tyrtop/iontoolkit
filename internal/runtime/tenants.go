package runtime

import (
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/config"
)

func (rt *Runtime) UseResolver(f func(*config.Config, string) (config.Tenant, bool)) {
	rt.mu.Lock()
	rt.ws.resolve = f
	rt.mu.Unlock()
}

func (rt *Runtime) saveConfig() error {
	st, cfg := rt.Store(), rt.Config()
	if st == nil || cfg == nil {
		return fmt.Errorf("no config to save to")
	}
	return st.SaveConfig(cfg)
}

func (rt *Runtime) setSecret(key, v string) error {
	st := rt.Store()
	if st == nil {
		return fmt.Errorf("nowhere to keep secrets")
	}
	return st.Secrets.Set(key, v)
}

func (rt *Runtime) moveSecret(from, to string) {
	st := rt.Store()
	if st == nil || from == to {
		return
	}
	if v, err := st.Secrets.Get(from); err == nil && v != "" {
		_ = st.Secrets.Set(to, v)
		_ = st.Secrets.Delete(from)
	}
}

func (rt *Runtime) HasPassword(tsg, user string) bool {
	st := rt.Store()
	if st == nil {
		return false
	}
	v, err := st.Secrets.Get(config.IONPassKey(tsg, user))
	return err == nil && v != ""
}

func (rt *Runtime) savedTenant(name string) (config.Tenant, error) {
	cfg := rt.Config()
	if cfg == nil {
		return config.Tenant{}, fmt.Errorf("no config")
	}
	t, ok := cfg.Tenant(name)
	if !ok {
		return t, fmt.Errorf("tenant %s is gone", name)
	}
	return t, nil
}

func (rt *Runtime) inUse(name string) bool {
	t, ok := rt.Tenant()
	return ok && strings.EqualFold(t.Name, name)
}

func (rt *Runtime) keep(t config.Tenant) error {
	rt.Config().Upsert(t)
	if err := rt.saveConfig(); err != nil {
		return err
	}
	if rt.inUse(t.Name) {
		rt.ReplaceTenant(t)
	}
	return nil
}

func (rt *Runtime) SwitchTenant(t config.Tenant) error {
	rt.UseTenant(t)
	rt.SetIONUser(t.DefaultUser())
	rt.Config().LastTenant = t.Name
	if err := rt.saveConfig(); err != nil {
		return err
	}
	rt.SetElements(nil)
	rt.ForgetSelection()
	rt.ResetFleet()
	return nil
}

func (rt *Runtime) AddTenant(t config.Tenant, secret string) error {
	if t.Name == "" || t.TSGID == "" {
		return fmt.Errorf("name and tsg id are required")
	}
	if rt.Config() == nil {
		return fmt.Errorf("no config")
	}
	rt.Config().Upsert(t)
	if secret != "" {
		if err := rt.setSecret(config.ClientSecretKey(t.TSGID), secret); err != nil {
			return err
		}
	}
	if err := rt.SwitchTenant(t); err != nil {
		return err
	}
	rt.SetIONUser("")
	return nil
}

func (rt *Runtime) EditTenant(was, name, tsg, clientID, secret string) (config.Tenant, error) {
	if name == "" || tsg == "" {
		return config.Tenant{}, fmt.Errorf("name and tsg id are required")
	}
	old, err := rt.savedTenant(was)
	if err != nil {
		return old, err
	}
	t := old
	t.Name, t.TSGID, t.ClientID = name, tsg, clientID
	if tsg != old.TSGID {
		rt.moveSecret(config.ClientSecretKey(old.TSGID), config.ClientSecretKey(tsg))
		rt.moveSecret(config.RefreshTokenKey(old.TSGID), config.RefreshTokenKey(tsg))
		for _, u := range old.Users {
			rt.moveSecret(config.IONPassKey(old.TSGID, u.Name), config.IONPassKey(tsg, u.Name))
		}
		if st := rt.Store(); st != nil {
			_ = st.DropCache(old.TSGID)
		}
	}
	if secret != "" {
		if err := rt.setSecret(config.ClientSecretKey(tsg), secret); err != nil {
			return t, err
		}
	}
	cfg := rt.Config()
	wasLast := strings.EqualFold(cfg.LastTenant, was)
	if !strings.EqualFold(name, was) {
		cfg.Remove(was)
	}
	cfg.Upsert(t)
	if wasLast {
		cfg.LastTenant = name
	}
	if err := rt.saveConfig(); err != nil {
		return t, err
	}
	if rt.inUse(was) {
		rt.ReplaceTenant(t)
	}
	return t, nil
}

func (rt *Runtime) RemoveTenant(name string) error {
	t, err := rt.savedTenant(name)
	if err != nil {
		return err
	}
	if st := rt.Store(); st != nil {
		for _, u := range t.Users {
			_ = st.Secrets.Delete(config.IONPassKey(t.TSGID, u.Name))
		}
		_ = st.Secrets.Delete(config.ClientSecretKey(t.TSGID))
		_ = st.Secrets.Delete(config.RefreshTokenKey(t.TSGID))
		_ = st.DropCache(t.TSGID)
	}
	wasInUse := rt.inUse(name)
	rt.Config().Remove(t.Name)
	if err := rt.saveConfig(); err != nil {
		return err
	}
	rt.mu.Lock()
	rt.ws.removed = append(rt.ws.removed, t)
	rt.mu.Unlock()
	if !wasInUse {
		return nil
	}
	rt.mu.RLock()
	resolve := rt.ws.resolve
	rt.mu.RUnlock()
	if resolve == nil {
		resolve = (*config.Config).Resolve
	}
	if next, ok := resolve(rt.Config(), ""); ok {
		rt.UseTenant(next)
		rt.SetIONUser(next.DefaultUser())
		return nil
	}
	rt.ClearTenant()
	rt.SetIONUser("")
	return nil
}

func (rt *Runtime) UndoRemoveTenant() (config.Tenant, error) {
	rt.mu.Lock()
	n := len(rt.ws.removed)
	if n == 0 {
		rt.mu.Unlock()
		return config.Tenant{}, fmt.Errorf("nothing to undo")
	}
	t := rt.ws.removed[n-1]
	rt.ws.removed = rt.ws.removed[:n-1]
	rt.mu.Unlock()
	rt.Config().Upsert(t)
	return t, rt.saveConfig()
}

func (rt *Runtime) IncludeTenant(name string, added bool) error {
	t, err := rt.savedTenant(name)
	if err != nil {
		return err
	}
	t.Added = added
	if err := rt.keep(t); err != nil {
		return err
	}
	if !added {
		var kept []Element
		for _, r := range rt.Elements() {
			if r.TSG != t.TSGID {
				kept = append(kept, r)
			}
		}
		rt.SetElements(kept)
		rt.SetFleetCount(len(kept))
	}
	return nil
}

func (rt *Runtime) AddUser(tenant, tsg, user, pass string) (config.Tenant, error) {
	if user == "" {
		return config.Tenant{}, fmt.Errorf("ion user is required")
	}
	if rt.Config() == nil {
		return config.Tenant{}, fmt.Errorf("no config")
	}
	t, ok := rt.Config().Tenant(tenant)
	if !ok {
		if tsg == "" {
			return t, fmt.Errorf("tenant %s is gone", tenant)
		}
		t = config.Tenant{Name: tenant, TSGID: tsg, Auth: config.AuthBrowser}
	}
	if t.TSGID == "" {
		t.TSGID = tsg
	}
	t.Added = true
	t.AddUser(user, t.DefaultUser() == "")
	if err := rt.keep(t); err != nil {
		return t, err
	}
	if pass != "" {
		if err := rt.setSecret(config.IONPassKey(t.TSGID, user), pass); err != nil {
			return t, err
		}
	}
	if rt.RunAs(t.TSGID) == "" || len(t.Users) == 1 {
		rt.SetRunAs(t.TSGID, user)
	}
	if rt.inUse(t.Name) && rt.IONUser() == "" {
		rt.SetIONUser(t.DefaultUser())
	}
	return t, nil
}

func (rt *Runtime) SaveLogin(tenant, tsg, user, pass string) (config.Tenant, error) {
	if user == "" || pass == "" {
		return config.Tenant{}, fmt.Errorf("user and password are required")
	}
	if rt.Config() == nil {
		return config.Tenant{}, fmt.Errorf("no config")
	}
	t, ok := rt.Config().Tenant(tenant)
	if !ok {
		t = config.Tenant{Name: tenant, TSGID: tsg, Auth: config.AuthBrowser, Added: true}
	}
	if t.TSGID == "" {
		t.TSGID = tsg
	}
	if err := rt.setSecret(config.IONPassKey(t.TSGID, user), pass); err != nil {
		return t, err
	}
	t.AddUser(user, t.DefaultUser() == "")
	if err := rt.keep(t); err != nil {
		return t, err
	}
	if rt.inUse(t.Name) {
		rt.SetIONUser(user)
	}
	return t, nil
}

func (rt *Runtime) EditUser(tenant, was, name, pass string) error {
	if name == "" {
		return fmt.Errorf("ion user is required")
	}
	t, err := rt.savedTenant(tenant)
	if err != nil {
		return err
	}
	if name != was {
		wasDefault := t.DefaultUser() == was
		rt.moveSecret(config.IONPassKey(t.TSGID, was), config.IONPassKey(t.TSGID, name))
		t.RemoveUser(was)
		t.AddUser(name, wasDefault)
		if rt.PickedRunAs(t.TSGID) == was {
			rt.SetRunAs(t.TSGID, name)
		}
	}
	if pass != "" {
		if err := rt.setSecret(config.IONPassKey(t.TSGID, name), pass); err != nil {
			return err
		}
	}
	if err := rt.keep(t); err != nil {
		return err
	}
	if rt.inUse(t.Name) && rt.IONUser() == was {
		rt.SetIONUser(name)
	}
	return nil
}

func (rt *Runtime) SetPassword(tenant, user, pass string) error {
	if pass == "" {
		return fmt.Errorf("password is required")
	}
	t, err := rt.savedTenant(tenant)
	if err != nil {
		return err
	}
	return rt.setSecret(config.IONPassKey(t.TSGID, user), pass)
}

func (rt *Runtime) SetDefaultUser(tenant, user string) error {
	t, err := rt.savedTenant(tenant)
	if err != nil {
		return err
	}
	if !hasUser(t, user) {
		return fmt.Errorf("%s has no user %s", t.Name, user)
	}
	t.AddUser(user, true)
	if err := rt.keep(t); err != nil {
		return err
	}
	rt.SetRunAs(t.TSGID, user)
	if rt.inUse(t.Name) {
		rt.SetIONUser(user)
	}
	return nil
}

func (rt *Runtime) RemoveUser(tenant, user string) (config.Tenant, error) {
	t, err := rt.savedTenant(tenant)
	if err != nil {
		return t, err
	}
	if !t.RemoveUser(user) {
		return t, fmt.Errorf("%s has no user %s", t.Name, user)
	}
	if st := rt.Store(); st != nil {
		_ = st.Secrets.Delete(config.IONPassKey(t.TSGID, user))
	}
	if err := rt.keep(t); err != nil {
		return t, err
	}
	if rt.PickedRunAs(t.TSGID) == user {
		rt.SetRunAs(t.TSGID, t.DefaultUser())
	}
	if rt.inUse(t.Name) && rt.IONUser() == user {
		rt.SetIONUser(t.DefaultUser())
	}
	return t, nil
}

func (rt *Runtime) RunAsUser(tenant, user string) error {
	t, err := rt.savedTenant(tenant)
	if err != nil {
		return err
	}
	rt.SetRunAs(t.TSGID, user)
	if rt.inUse(t.Name) {
		rt.SetIONUser(user)
	}
	return nil
}

func hasUser(t config.Tenant, user string) bool {
	for _, u := range t.Users {
		if u.Name == user {
			return true
		}
	}
	return false
}

func tenantCommands() []Command {
	named := func(rt *Runtime, c *Call) (string, error) {
		if n := c.Arg("tenant"); n != "" {
			return n, nil
		}
		if t, ok := rt.Tenant(); ok {
			return t.Name, nil
		}
		return "", fmt.Errorf("no tenant is in use; tenant= names one")
	}
	tenantArg := Arg{Name: "tenant", Help: "the tenant's name; the one in use when empty"}
	return []Command{
		{
			Name: "tenant.list", Help: "the saved tenants, the one in use marked",
			Run: func(rt *Runtime, c *Call) error {
				cfg := rt.Config()
				if cfg == nil {
					return fmt.Errorf("no config")
				}
				for _, t := range cfg.Tenants {
					mark := " "
					if rt.inUse(t.Name) {
						mark = ">"
					}
					auth := "service account"
					if t.UsesBrowser() {
						auth = "browser"
					}
					fleet := ""
					if t.Added {
						fleet = " · in the fleet"
					}
					c.Say("%s %s  tsg %s · %s · %d users%s", mark, t.Name, t.TSGID, auth, len(t.Users), fleet)
				}
				return nil
			},
		},
		{
			Name: "tenant.add", Help: "save a tenant and switch to it: name= tsg= client_id= secret= (auth=browser for a browser sign-in)",
			Args: []Arg{{Name: "name", Required: true}, {Name: "tsg", Required: true}, {Name: "client_id"}, {Name: "secret"}, {Name: "auth"}},
			Run: func(rt *Runtime, c *Call) error {
				auth := config.AuthServiceAccount
				if c.Arg("auth") == config.AuthBrowser {
					auth = config.AuthBrowser
				}
				t := config.Tenant{Name: c.Arg("name"), TSGID: c.Arg("tsg"), ClientID: c.Arg("client_id"), Auth: auth}
				return rt.AddTenant(t, c.Arg("secret"))
			},
		},
		{
			Name: "tenant.edit", Help: "change a saved tenant: tenant= name= tsg= client_id= secret= (blank keeps)",
			Args: []Arg{tenantArg, {Name: "name"}, {Name: "tsg"}, {Name: "client_id"}, {Name: "secret"}},
			Run: func(rt *Runtime, c *Call) error {
				was, err := named(rt, c)
				if err != nil {
					return err
				}
				old, err := rt.savedTenant(was)
				if err != nil {
					return err
				}
				or := func(v, keep string) string {
					if v == "" {
						return keep
					}
					return v
				}
				t, err := rt.EditTenant(old.Name, or(c.Arg("name"), old.Name), or(c.Arg("tsg"), old.TSGID),
					or(c.Arg("client_id"), old.ClientID), c.Arg("secret"))
				if err == nil {
					c.Say("saved %s", t.Name)
				}
				return err
			},
		},
		{
			Name: "tenant.remove", Help: "forget a tenant, its secrets and its cache: tenant=",
			Args: []Arg{{Name: "tenant", Required: true}},
			Run:  func(rt *Runtime, c *Call) error { return rt.RemoveTenant(c.Arg("tenant")) },
		},
		{
			Name: "tenant.undo", Help: "put back the record of the tenant removed last, not its secrets",
			Run: func(rt *Runtime, c *Call) error {
				t, err := rt.UndoRemoveTenant()
				if err == nil {
					c.Say("%s is back · its client secret needs entering again", t.Name)
				}
				return err
			},
		},
		{
			Name: "tenant.use", Help: "switch to a saved tenant: tenant=",
			Args: []Arg{{Name: "tenant", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				t, err := rt.savedTenant(c.Arg("tenant"))
				if err != nil {
					return err
				}
				if err := rt.SwitchTenant(t); err != nil {
					return err
				}
				c.Say("switched to %s", t.Name)
				return nil
			},
		},
		{
			Name: "tenant.include", Help: "put a tenant's elements in the fleet, or take them out with in=no: tenant=",
			Args: []Arg{tenantArg, {Name: "in", Default: "yes"}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				return rt.IncludeTenant(name, c.Arg("in") != "no")
			},
		},
		{
			Name: "user.list", Help: "a tenant's ION users: tenant=",
			Args: []Arg{tenantArg},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				t, err := rt.savedTenant(name)
				if err != nil {
					return err
				}
				runAs := rt.RunAs(t.TSGID)
				for _, u := range t.Users {
					var tags []string
					if u.Default {
						tags = append(tags, "default")
					}
					if u.Name == runAs {
						tags = append(tags, "active")
					}
					if rt.HasPassword(t.TSGID, u.Name) {
						tags = append(tags, "saved")
					} else {
						tags = append(tags, "no password")
					}
					c.Say("%s  (%s)", u.Name, strings.Join(tags, ", "))
				}
				return nil
			},
		},
		{
			Name: "user.add", Help: "save an ION user on a tenant: tenant= name= password=",
			Args: []Arg{tenantArg, {Name: "name", Required: true}, {Name: "password"}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				_, err = rt.AddUser(name, "", c.Arg("name"), c.Arg("password"))
				return err
			},
		},
		{
			Name: "user.edit", Help: "rename an ION user or change its password: tenant= user= name= password=",
			Args: []Arg{tenantArg, {Name: "user", Required: true}, {Name: "name"}, {Name: "password"}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				to := c.Arg("name")
				if to == "" {
					to = c.Arg("user")
				}
				return rt.EditUser(name, c.Arg("user"), to, c.Arg("password"))
			},
		},
		{
			Name: "user.password", Help: "save an ION user's password: tenant= user= password=",
			Args: []Arg{tenantArg, {Name: "user", Required: true}, {Name: "password", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				return rt.SetPassword(name, c.Arg("user"), c.Arg("password"))
			},
		},
		{
			Name: "user.remove", Help: "forget an ION user and its password: tenant= user=",
			Args: []Arg{tenantArg, {Name: "user", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				_, err = rt.RemoveUser(name, c.Arg("user"))
				return err
			},
		},
		{
			Name: "user.default", Help: "make an ION user the tenant's default: tenant= user=",
			Args: []Arg{tenantArg, {Name: "user", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				return rt.SetDefaultUser(name, c.Arg("user"))
			},
		},
		{
			Name: "user.run-as", Help: "log the tenant's runs and sessions in as an ION user: tenant= user=",
			Args: []Arg{tenantArg, {Name: "user", Required: true}},
			Run: func(rt *Runtime, c *Call) error {
				name, err := named(rt, c)
				if err != nil {
					return err
				}
				return rt.RunAsUser(name, c.Arg("user"))
			},
		},
	}
}
