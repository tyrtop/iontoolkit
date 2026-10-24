package app

import (
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type sayWriter struct{ c *runtime.Call }

func (w sayWriter) Write(b []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		w.c.Say("%s", l)
	}
	return len(b), nil
}

func WarmInto(rt *runtime.Runtime, results []TenantWarm) {
	cur, _ := rt.Tenant()
	for _, r := range results {
		if r.Skipped {
			continue
		}
		if r.Err != nil {
			rt.SetIssue(r.Tenant.TSGID, ShortAuthError(r.Err))
			continue
		}
		rt.ClearIssue(r.Tenant.TSGID)
		if r.Tenant.TSGID == cur.TSGID {
			rt.TokenAlive()
		}
	}
}

func authCommands() []runtime.Command {
	return []runtime.Command{
		{
			Name: "auth.signin", Slow: true,
			Help: "sign in to the account with a browser and list its tenants: add=all or add=ID,ID puts those in the fleet",
			Args: []runtime.Arg{{Name: "add"}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				st := rt.Store()
				if st == nil {
					return fmt.Errorf("signing in needs the config store")
				}
				d, err := Discover(c.Ctx, st)
				if err != nil {
					return fmt.Errorf("browser sign-in: %w", err)
				}
				if err := SaveDiscovery(st, d); err != nil {
					return err
				}
				rt.SetIdentity(d.Identity)
				rt.MarkBrowserSignedIn()
				rt.SetDiscovered(d.Tenants)
				c.Say("signed in as %s — %d tenants visible", d.Identity.Display(), len(d.Tenants))
				if add := c.Arg("add"); add != "" {
					if err := pick(rt, add); err != nil {
						return err
					}
					if err := rt.AddPicked(); err != nil {
						return err
					}
				}
				cfg := rt.Config()
				for _, t := range d.Tenants {
					mark := " "
					if cfg != nil && cfg.IsAdded(t.ID) {
						mark = "+"
					}
					c.Say("%s %s  tsg %s", mark, t.Name, t.ID)
				}
				return nil
			},
		},
		{
			Name: "auth.add", Help: "put tenants the last sign-in found in the fleet: tenants=all or tenants=ID,ID",
			Args: []runtime.Arg{{Name: "tenants", Required: true}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				if len(rt.Discovered()) == 0 {
					return fmt.Errorf("nothing found to add yet: auth.signin first, or auth.signin add=…")
				}
				if err := pick(rt, c.Arg("tenants")); err != nil {
					return err
				}
				return rt.AddPicked()
			},
		},
		{
			Name: "auth.warm", Slow: true,
			Help: "give each browser tenant a credential of its own, which is what makes one sign-in last: tenants=name,name (default: the fleet)",
			Args: []runtime.Arg{{Name: "tenants"}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				st, cfg := rt.Store(), rt.Config()
				if st == nil || cfg == nil {
					return fmt.Errorf("warming needs the config store")
				}
				tenants := cfg.AddedTenants()
				if names := c.Arg("tenants"); names != "" {
					tenants = nil
					cur, inUse := rt.Tenant()
					for _, n := range strings.Split(names, ",") {
						n = strings.TrimSpace(n)
						t, ok := cfg.Tenant(n)
						if !ok && inUse && strings.EqualFold(cur.Name, n) {
							t, ok = cur, true
						}
						if !ok {
							return fmt.Errorf("no tenant is called %q", n)
						}
						tenants = append(tenants, t)
					}
				}
				rt.SetWarming(len(tenants))
				defer rt.SetWarming(0)
				results := WarmEach(c.Ctx, st, tenants)
				WarmInto(rt, results)
				return WarmReport(results, sayWriter{c})
			},
		},
		{
			Name: "auth.login", Slow: true,
			Help: "sign in with a browser, then give each tenant a credential of its own: auth.signin and auth.warm",
			Long: `The per-tenant credentials are what make a sign-in stick: each one is
renewed and re-stored every time it is used, so ordinary use keeps them
alive. The account credential alone cannot do that -- the server rotates it
as it is redeemed, so it only ever covers one tenant.`,
			Args: []runtime.Arg{{Name: "tenants", Help: "tenants to warm, by name (default: the fleet)"}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				if err := rt.Exec(&runtime.Call{Ctx: c.Ctx, Out: c.Out}, "auth.signin"); err != nil {
					return err
				}
				return rt.Exec(&runtime.Call{Ctx: c.Ctx, Out: c.Out, Args: map[string]string{"tenants": c.Arg("tenants")}}, "auth.warm")
			},
		},
		{
			Name: "auth.status", Help: "who is signed in, and how long the sign-in in use has left",
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				if id := rt.Identity(); id.Name != "" || id.Email != "" || id.Subject != "" {
					c.Say("account   %s", id.Display())
				}
				t, ok := rt.Tenant()
				if !ok {
					c.Say("no tenant in use")
					return nil
				}
				c.Say("tenant    %s (tsg %s)", t.Name, t.TSGID)
				if p := rt.Profile(); p.Region != "" {
					c.Say("region    %s", p.Region)
				}
				if ttl := rt.TokenTTL(); ttl > 0 {
					c.Say("api token renews every %s", ui.Age(ttl))
				}
				switch left, ok := SessionLeft(rt.Store(), t.TSGID); {
				case !t.UsesBrowser():
					c.Say("sign-in   service account")
				case ok && left > 0:
					c.Say("sign-in   %s left", ui.Age(left))
				case ok:
					c.Say("sign-in   expired")
				default:
					c.Say("sign-in   none saved")
				}
				if dead := rt.TokenDead(); dead != "" {
					c.Say("!         %s", dead)
				}
				return nil
			},
		},
	}
}

func pick(rt *runtime.Runtime, which string) error {
	found := rt.Discovered()
	if which == "all" {
		for _, t := range found {
			if !rt.Picked(t.ID) {
				rt.TogglePick(t.ID)
			}
		}
		return nil
	}
	for _, id := range strings.Split(which, ",") {
		id = strings.TrimSpace(id)
		hit := false
		for _, t := range found {
			if t.ID == id || strings.EqualFold(t.Name, id) {
				hit = true
				if !rt.Picked(t.ID) {
					rt.TogglePick(t.ID)
				}
			}
		}
		if !hit {
			return fmt.Errorf("the sign-in found no tenant %q", id)
		}
	}
	return nil
}
