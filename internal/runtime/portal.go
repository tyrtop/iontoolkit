package runtime

import (
	"context"
	"fmt"

	"tyrtop.com/iontk/internal/sase/api"
)

type Portal interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
	Send(ctx context.Context, method, path string, body []byte) ([]byte, error)
	LookupElement(ctx context.Context, eid string) (api.Element, error)
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

func (rt *Runtime) Portal(ctx context.Context) (Portal, Tenant, error) {
	rt.mu.RLock()
	fn := rt.portal
	rt.mu.RUnlock()
	if fn == nil {
		return nil, Tenant{}, fmt.Errorf("no tenant to reach the portal with")
	}
	return fn(ctx)
}
