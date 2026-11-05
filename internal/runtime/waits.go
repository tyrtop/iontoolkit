package runtime

import (
	"sort"
	"strings"
	"time"
)

type Wait struct {
	What  string
	Since time.Time
	Until time.Time
}

func (w Wait) Line(now time.Time) string {
	line := w.What + " · " + short(now.Sub(w.Since))
	if !w.Until.IsZero() {
		line += ", up to " + short(w.Until.Sub(w.Since))
	}
	return line
}

func short(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

type waitsState struct {
	next int
	on   map[int]Wait
}

func (rt *Runtime) beginWait(w Wait) func() {
	rt.mu.Lock()
	if rt.waits.on == nil {
		rt.waits.on = map[int]Wait{}
	}
	id := rt.waits.next
	rt.waits.next++
	rt.waits.on[id] = w
	rt.mu.Unlock()
	return func() {
		rt.mu.Lock()
		delete(rt.waits.on, id)
		rt.mu.Unlock()
	}
}

func (rt *Runtime) Waits() []Wait {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]Wait, 0, len(rt.waits.on))
	for _, w := range rt.waits.on {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}
