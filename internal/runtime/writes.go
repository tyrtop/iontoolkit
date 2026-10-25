package runtime

import "tyrtop.com/iontk/internal/sase/write"

func (rt *Runtime) SetWrites(set *write.Set, problem string) {
	rt.mu.Lock()
	rt.writes, rt.writesErr = set, problem
	rt.mu.Unlock()
}

func (rt *Runtime) Writes() (*write.Set, string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.writes, rt.writesErr
}
