package runtime

import (
	"context"
	"sync"
	"time"
)

const SweepPace = 400 * time.Millisecond

func (rt *Runtime) note(s string) { rt.changed(Event{Area: AreaStatus, Status: s}) }

func pause(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (rt *Runtime) sweeping(ctx context.Context) (_ context.Context, done func(), gen int) {
	return rt.sweepingAt(ctx, -1)
}

func (rt *Runtime) sweepingAt(ctx context.Context, want int) (_ context.Context, done func(), gen int) {
	rt.sweepMu.Lock()
	if want >= 0 && want != rt.sweepGen {
		rt.sweepMu.Unlock()
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, func() {}, want
	}
	if rt.sweeps == nil {
		rt.sweeps, rt.stopSweeps = context.WithCancel(context.Background())
		rt.sweepWG = &sync.WaitGroup{}
		if rt.sweepsClosed {
			rt.stopSweeps()
		}
	}
	all, wg, gen := rt.sweeps, rt.sweepWG, rt.sweepGen
	wg.Add(1)
	rt.sweepMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(all, cancel)
	return ctx, func() {
		stop()
		cancel()
		wg.Done()
	}, gen
}

func (rt *Runtime) sweptSince(gen int) bool {
	rt.sweepMu.Lock()
	defer rt.sweepMu.Unlock()
	return rt.sweepGen != gen
}

func (rt *Runtime) StopSweeps() {
	rt.sweepMu.Lock()
	stop, wg := rt.stopSweeps, rt.sweepWG
	rt.sweeps, rt.stopSweeps, rt.sweepWG = nil, nil, nil
	rt.sweepGen++
	rt.sweepMu.Unlock()
	if stop != nil {
		stop()
		wg.Wait()
	}
}

func (rt *Runtime) follow(ctx context.Context, l Load, gen int) (wait func()) {
	var wg sync.WaitGroup
	if l.Err != nil {
		return wg.Wait
	}
	if l.SiteErr != nil {
		rt.note(l.Tenant + ": site names unavailable: " + rt.explain(l.SiteErr))
	}
	ctx, done, _ := rt.sweepingAt(ctx, gen)
	if ctx.Err() != nil {
		done()
		return wg.Wait
	}
	run := func(sweep func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sweep()
		}()
	}
	if w := HAClusters(l.Elements); len(w.Elements) > 0 {
		run(func() { rt.SweepHA(ctx, l.TSG, w, SweepPace) })
	}
	run(func() { rt.SweepCells(ctx, l.TSG, SweepPace) })
	run(func() { rt.ReadTables(ctx, l.TSG) })
	go func() {
		wg.Wait()
		done()
	}()
	return wg.Wait
}

func (rt *Runtime) SweepHA(ctx context.Context, tsg string, w HAWork, pace time.Duration) {
	rt.StartHA(tsg, w)
	res := rt.FetchHABulk(ctx, tsg, w, rt.NearestElements(tsg, w.Elements))
	for {
		next := rt.ApplyHA(res)
		rt.SaveHA(tsg, next.Save)
		switch {
		case next.Tick:
			if !pause(ctx, pace) {
				rt.editHA(func(h *haState) {
					delete(h.queue, tsg)
					delete(h.inFlight, tsg)
				})
				return
			}
			rt.MarkHAOut(tsg)
			ids := rt.NearestElements(tsg, rt.Queued(tsg))
			res = rt.FetchHABulk(ctx, tsg, rt.haWork(tsg, ids), ids)
		case len(next.Pairs) > 0 && ctx.Err() == nil:
			res = rt.FetchHAPairs(ctx, tsg, next.Pairs)
		default:
			if ctx.Err() != nil {
				rt.editHA(func(h *haState) {
					delete(h.queue, tsg)
					delete(h.inFlight, tsg)
				})
			}
			return
		}
	}
}

func (rt *Runtime) SweepCells(ctx context.Context, tsg string, pace time.Duration) {
	var wg sync.WaitGroup
	for _, job := range rt.StartCells(tsg) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.sweepColumn(ctx, job, pace)
		}()
	}
	wg.Wait()
}

func (rt *Runtime) sweepColumn(ctx context.Context, job CellJob, pace time.Duration) {
	for {
		res := rt.FetchCells(ctx, job)
		next := rt.ApplyCells(res)
		rt.SaveCells(job.TSG, job.Col, next.Save)
		if next.Tick && !pause(ctx, pace) {
			at := cellKey(job.Col, job.TSG)
			rt.editCells(func(s *cellsState) {
				delete(s.queue, at)
				delete(s.out, at)
			})
			return
		}
		if !next.Tick {
			return
		}
		var ok bool
		if job, ok = rt.NextCells(job.Col, job.TSG); !ok {
			return
		}
	}
}

func (rt *Runtime) ReadTables(ctx context.Context, tsg string) {
	var wg sync.WaitGroup
	for _, job := range rt.TablesWanted(tsg) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.ApplyTable(rt.ReadTable(ctx, job))
		}()
	}
	wg.Wait()
}

func (rt *Runtime) SweepETA(tsg string, left int) string {
	if left <= 0 {
		return "done"
	}
	rps := 1.0
	if c := rt.Clients().Get(tsg); c != nil && c.Rate() > 0 {
		rps = c.Rate()
	}
	return age(time.Duration(float64(left)/rps*float64(time.Second))) + " to go"
}
