package runtime

import (
	"context"
	"fmt"

	"tyrtop.com/iontk/internal/config"
)

type Portal interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
	Send(ctx context.Context, method, path string, body []byte) ([]byte, error)
}

type Tenant struct {
	TSG  string
	Name string
}

type PortalFunc func(ctx context.Context) (Portal, Tenant, error)

func (rt *Runtime) UsePortal(fn PortalFunc) {
	rt.mu.Lock()
	rt.portal = fn
	rt.mu.Unlock()
}

func (rt *Runtime) Portal(ctx context.Context) (Portal, Tenant, error) { return rt.PortalFor(ctx, "") }

func (rt *Runtime) PortalFor(ctx context.Context, tsg string) (Portal, Tenant, error) {
	rt.mu.RLock()
	fn := rt.portal
	rt.mu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	if tsg == "" {
		cur, ok := rt.Tenant()
		if !ok {
			return nil, Tenant{}, fmt.Errorf("no tenant to reach the portal with")
		}
		tsg = cur.TSGID
	}
	return rt.reach(ctx, rt.TenantFor(tsg))
}

func (rt *Runtime) PortalAs(ctx context.Context, t config.Tenant) (Portal, Tenant, error) {
	rt.mu.RLock()
	fn := rt.portal
	rt.mu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	if t.TSGID == "" {
		return rt.PortalFor(ctx, "")
	}
	return rt.reach(ctx, t)
}

func (rt *Runtime) reach(ctx context.Context, t config.Tenant) (Portal, Tenant, error) {
	c, err := rt.Connect(ctx, t)
	if err != nil {
		return nil, Tenant{}, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
	}
	return c, Tenant{TSG: t.TSGID, Name: t.Name}, nil
}
