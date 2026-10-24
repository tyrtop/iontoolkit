package runtime

import "time"

const (
	FleetNone     = "none"
	FleetUpdating = "updating"
	FleetUpdated  = "updated"
	FleetCached   = "cached"
	FleetFailed   = "failed"

	AreaFleet = "fleet"
)

type fleetState struct {
	state   string
	at      time.Time
	count   int
	pending int
	issues  map[string]string
}

type Fleet struct {
	State   string
	At      time.Time
	Count   int
	Pending int
	Issues  map[string]string
}

func (rt *Runtime) Fleet() Fleet {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	f := rt.fleet
	out := Fleet{State: f.state, At: f.at, Count: f.count, Pending: f.pending, Issues: map[string]string{}}
	if out.State == "" {
		out.State = FleetNone
	}
	for k, v := range f.issues {
		out.Issues[k] = v
	}
	return out
}

func (rt *Runtime) editFleet(edit func(f *fleetState)) {
	rt.mu.Lock()
	edit(&rt.fleet)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaFleet})
}

func (rt *Runtime) SetFleetState(state string) {
	rt.editFleet(func(f *fleetState) { f.state = state })
}

func (rt *Runtime) SetFleetCount(n int) {
	rt.editFleet(func(f *fleetState) { f.count = n })
}

func (rt *Runtime) SetFleetAt(at time.Time) {
	rt.editFleet(func(f *fleetState) { f.at = at })
}

func (rt *Runtime) ResetFleet() {
	rt.editFleet(func(f *fleetState) { f.state, f.count, f.at = FleetNone, 0, time.Time{} })
}

func (rt *Runtime) SetPending(n int) {
	rt.editFleet(func(f *fleetState) { f.pending = n })
}

func (rt *Runtime) DonePending() int {
	var left int
	rt.editFleet(func(f *fleetState) {
		if f.pending > 0 {
			f.pending--
		}
		left = f.pending
	})
	return left
}

func (rt *Runtime) SetIssue(tsg, reason string) {
	rt.editFleet(func(f *fleetState) {
		if f.issues == nil {
			f.issues = map[string]string{}
		}
		f.issues[tsg] = reason
	})
}

func (rt *Runtime) ClearIssue(tsg string) {
	rt.editFleet(func(f *fleetState) { delete(f.issues, tsg) })
}

func (rt *Runtime) ClearIssues() {
	rt.editFleet(func(f *fleetState) { f.issues = nil })
}
