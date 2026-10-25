package app

import (
	"fmt"

	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/runtime"
)

type Testing struct {
	Runtime *runtime.Runtime
	real    *runtime.Runtime
	fixture *fixture.Set
}

func EnterTesting(real *runtime.Runtime) (*Testing, error) {
	_, set, err := FakeFleet(real.Store())
	if err != nil {
		return nil, err
	}
	t := &Testing{real: real, fixture: Fixture}
	Fixture = set
	st, err := Store()
	if err != nil {
		Fixture = t.fixture
		return nil, err
	}
	cfg, err := st.Config()
	if err != nil {
		Fixture = t.fixture
		return nil, err
	}
	if len(cfg.Tenants) == 0 {
		Fixture = t.fixture
		return nil, fmt.Errorf("the fake fleet has no tenants")
	}
	rt := NewRuntime(st, cfg)
	rt.SetViews(real.Views(), real.ViewsErr())
	rt.SetColumns(real.Columns())
	rt.SetAuthMode(runtime.AuthTenant)
	rt.UseTenant(cfg.Tenants[0])
	rt.SetIONUser(cfg.Tenants[0].DefaultUser())
	rt.ForgetSelection()
	rt.ResetFleet()
	t.Runtime = rt
	return t, nil
}

func (t *Testing) Leave() *runtime.Runtime {
	t.Runtime.Sessions().CloseAll()
	t.Runtime.Close()
	Fixture = t.fixture
	t.real.ResetHALive()
	return t.real
}

func (t *Testing) Real() *runtime.Runtime { return t.real }

func (t *Testing) Held() *fixture.Set { return t.fixture }
