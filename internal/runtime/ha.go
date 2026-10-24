package runtime

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
)

const (
	HAActive  = "active"
	HAStandby = "standby"
	HAUnknown = "unknown"

	haAnswered = "ok"

	AreaHA = "ha"

	HABatch        = 16
	haFetchWorkers = 8
)

type HAWork struct {
	Pairs    [][2]string
	Elements []string
	NoSite   []string
}

func (w HAWork) Clusters() int { return len(w.Pairs) + len(w.NoSite) }

func HAClusters(els []api.Element) HAWork {
	var w HAWork
	seen := map[string]bool{}
	for _, el := range els {
		cid := el.ClusterID()
		if cid == "" {
			continue
		}
		w.Elements = append(w.Elements, el.ID)
		if seen[cid] {
			continue
		}
		seen[cid] = true
		if el.SiteID == "" {
			w.NoSite = append(w.NoSite, cid)
			continue
		}
		w.Pairs = append(w.Pairs, [2]string{el.SiteID, cid})
	}
	return w
}

type HAResult struct {
	TSG          string
	States       map[string]string
	Bulk         string
	Answered     []string
	Failed       map[string]string
	Rest         [][2]string
	RestElements []string
}

type haState struct {
	words    map[string]string
	clusters map[string]string
	cached   map[string]string
	failed   int
	err      string
	total    int
	done     int
	at       time.Time
	queue    map[string][]string
	inFlight map[string]bool
	bulk     string
	viewport int
}

type HA struct {
	Failed   int
	Err      string
	Total    int
	Done     int
	At       time.Time
	Bulk     string
	InFlight int
	Queued   int
}

func (rt *Runtime) HA() HA {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	h := rt.ha
	out := HA{Failed: h.failed, Err: h.err, Total: h.total, Done: h.done, At: h.at, Bulk: h.bulk, InFlight: len(h.inFlight)}
	for _, q := range h.queue {
		out.Queued += len(q)
	}
	return out
}

func (rt *Runtime) HAWorking() bool {
	h := rt.HA()
	return h.InFlight > 0 || h.Queued > 0
}

func (rt *Runtime) editHA(edit func(h *haState)) {
	rt.mu.Lock()
	h := &rt.ha
	if h.words == nil {
		h.words, h.clusters, h.cached = map[string]string{}, map[string]string{}, map[string]string{}
		h.queue, h.inFlight = map[string][]string{}, map[string]bool{}
	}
	edit(h)
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaHA})
}

func (rt *Runtime) HAState(r Element) (string, bool) {
	if r.ClusterID() == "" {
		return "", false
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	h := rt.ha
	if word := h.words[r.Key()]; word != "" {
		return word, false
	}
	if word := h.cached[r.Key()]; word != "" {
		return word, true
	}
	switch c := h.clusters[r.TSG+"/"+r.ClusterID()]; {
	case r.SiteID == "":
		return "ha?", false
	case c == haAnswered:
		return HAUnknown, false
	case c != "":
		return "ha?", false
	}
	return "loading", false
}

func (rt *Runtime) SetHA(words map[string]string) {
	rt.editHA(func(h *haState) {
		for k, v := range words {
			h.words[k] = v
		}
	})
}

func (rt *Runtime) SetCachedHA(words map[string]string) {
	rt.editHA(func(h *haState) {
		for k, v := range words {
			h.cached[k] = v
		}
	})
}

func (rt *Runtime) SetHAProgress(total, done int, at time.Time) {
	rt.editHA(func(h *haState) { h.total, h.done, h.at = total, done, at })
}

func (rt *Runtime) ResetHA() {
	rt.mu.Lock()
	rt.ha = haState{viewport: rt.ha.viewport}
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaHA})
}

func (rt *Runtime) SetViewport(rows int) {
	rt.mu.Lock()
	rt.ha.viewport = rows
	rt.mu.Unlock()
}

func (rt *Runtime) StartHA(tsg string, w HAWork) {
	rt.editHA(func(h *haState) {
		h.total += w.Clusters()
		h.inFlight[tsg] = true
	})
}

func (rt *Runtime) MarkHAOut(tsg string) {
	rt.editHA(func(h *haState) { h.inFlight[tsg] = true })
}

func (rt *Runtime) HAInFlight(tsg string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.ha.inFlight[tsg]
}

func (rt *Runtime) Queued(tsg string) []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return append([]string(nil), rt.ha.queue[tsg]...)
}

type HANext struct {
	Tick  bool
	Pairs [][2]string
	Save  map[string]string
}

func (rt *Runtime) ApplyHA(res HAResult) HANext {
	var next HANext
	rt.editHA(func(h *haState) {
		for k, v := range res.States {
			h.words[k] = v
		}
		if res.Bulk != "" {
			h.bulk = res.Bulk
		}
		delete(h.inFlight, res.TSG)
		h.done += len(res.Answered) + len(res.Failed)
		h.at = time.Now()
		for _, cid := range res.Answered {
			h.clusters[res.TSG+"/"+cid] = haAnswered
		}
		for cid, why := range res.Failed {
			h.clusters[res.TSG+"/"+cid] = why
			h.failed++
			if h.err == "" {
				h.err = why
			}
		}
		if len(res.RestElements) > 0 {
			h.queue[res.TSG] = res.RestElements
		} else {
			delete(h.queue, res.TSG)
		}
	})
	switch {
	case len(res.RestElements) > 0:
		next.Tick = true
		next.Save = res.States
	case len(res.Rest) > 0:
		rt.MarkHAOut(res.TSG)
		next.Pairs = rt.NearestPairs(res.TSG, res.Rest)
	default:
		next.Save = res.States
	}
	return next
}

func (rt *Runtime) SaveHA(tsg string, states map[string]string) {
	st := rt.Store()
	if st == nil || len(states) == 0 {
		return
	}
	bare := make(map[string]string, len(states))
	for k, w := range states {
		bare[strings.TrimPrefix(k, tsg+"/")] = w
	}
	_ = st.SaveHA(tsg, bare)
}

func bulkWord(status string) string {
	switch status {
	case HAActive:
		return HAActive
	case "backup":
		return HAStandby
	case "":
		return HAUnknown
	}
	return status
}

func memberWord(mem api.ClusterMember, hasActive bool) string {
	switch {
	case mem.Status == HAActive:
		return HAActive
	case hasActive:
		return HAStandby
	case mem.Status != "":
		return mem.Status
	}
	return HAUnknown
}

func (rt *Runtime) FetchHABulk(ctx context.Context, tsg string, w HAWork, ids []string) HAResult {
	c := rt.Clients().Get(tsg)
	if c == nil {
		return HAResult{TSG: tsg, Bulk: "no client for this tenant", Failed: allFailed(w, "tenant unreachable")}
	}
	ask, rest := ids, []string(nil)
	if len(ask) > api.ClusterQueryLimit {
		ask, rest = ids[:api.ClusterQueryLimit], ids[api.ClusterQueryLimit:]
	}
	sts, err := c.SpokeClusterStatuses(ctx, ask)
	if err != nil {
		logging.L().Warn("ha falling back to the per-cluster sweep", "tsg", tsg,
			"asked", len(ask), "clusters", len(w.Pairs), "err", err)
		res := HAResult{TSG: tsg, Bulk: rt.explain(err), Rest: w.Pairs}
		if len(w.NoSite) > 0 {
			res.Failed = map[string]string{}
			for _, cid := range w.NoSite {
				res.Failed[cid] = "no site id in the element record"
			}
		}
		return res
	}
	states := make(map[string]string, len(sts))
	answered := make([]string, 0, len(sts))
	seen := map[string]bool{}
	for _, st := range sts {
		states[tsg+"/"+st.ElementID] = bulkWord(st.Status)
		if st.ClusterID != "" && !seen[st.ClusterID] {
			seen[st.ClusterID] = true
			answered = append(answered, st.ClusterID)
		}
	}
	logging.L().Info("ha read in bulk", "tsg", tsg, "asked", len(ask),
		"rows", len(sts), "clusters", len(answered), "queued", len(rest))
	return HAResult{TSG: tsg, States: states, Answered: answered, Bulk: "used", RestElements: rest}
}

func (rt *Runtime) FetchHAPairs(ctx context.Context, tsg string, pairs [][2]string) HAResult {
	c := rt.Clients().Get(tsg)
	var rest [][2]string
	if len(pairs) > HABatch {
		pairs, rest = pairs[:HABatch], pairs[HABatch:]
	}
	res := HAResult{TSG: tsg, States: map[string]string{}, Failed: map[string]string{}, Rest: rest}
	if c == nil {
		for _, p := range pairs {
			res.Failed[p[1]] = "tenant unreachable"
		}
		return res
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan [2]string)
	for i := 0; i < min(haFetchWorkers, len(pairs)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				st, err := c.SpokeClusterStatus(ctx, p[0], p[1])
				if err != nil {
					mu.Lock()
					res.Failed[p[1]] = rt.explain(err)
					mu.Unlock()
					continue
				}
				_, hasActive := st.Active()
				mu.Lock()
				res.Answered = append(res.Answered, p[1])
				for _, mem := range st.Members {
					res.States[tsg+"/"+mem.ElementID] = memberWord(mem, hasActive)
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range pairs {
		work <- p
	}
	close(work)
	wg.Wait()
	return res
}

func allFailed(w HAWork, why string) map[string]string {
	out := make(map[string]string, w.Clusters())
	for _, p := range w.Pairs {
		out[p[1]] = why
	}
	for _, cid := range w.NoSite {
		out[cid] = why
	}
	return out
}

func (rt *Runtime) cursorDistance(tsg string, key func(Element) string) (map[string]int, int) {
	rows, matched, cursor := rt.Elements(), rt.MatchIndices(), rt.Cursor()
	at := make(map[string]int, len(matched))
	for pos, i := range matched {
		el := rows[i]
		if el.TSG != tsg {
			continue
		}
		if k := key(el); k != "" {
			if _, seen := at[k]; !seen {
				at[k] = pos
			}
		}
	}
	return at, cursor
}

func rankFrom(at map[string]int, cursor int) func(string) int {
	return func(k string) int {
		pos, ok := at[k]
		if !ok {
			return 1 << 30
		}
		if pos >= cursor {
			return pos - cursor
		}
		return cursor - pos
	}
}

func (rt *Runtime) NearestPairs(tsg string, rest [][2]string) [][2]string {
	if len(rest) < 2 {
		return rest
	}
	at, cursor := rt.cursorDistance(tsg, func(el Element) string { return el.ClusterID() })
	rank := rankFrom(at, cursor)
	out := append([][2]string(nil), rest...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i][1]) < rank(out[j][1]) })
	return out
}

func (rt *Runtime) NearestElements(tsg string, ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	at, cursor := rt.cursorDistance(tsg, func(el Element) string { return el.ID })
	rank := rankFrom(at, cursor)
	out := append([]string(nil), ids...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func (rt *Runtime) HAWanted() map[string][]string {
	rows, matched, cursor := rt.Elements(), rt.MatchIndices(), rt.Cursor()
	rt.mu.RLock()
	h := max(4, rt.ha.viewport)
	words, clusters := rt.ha.words, rt.ha.clusters
	out := map[string][]string{}
	from, to := max(0, cursor-h), min(len(matched), cursor+h)
	for _, i := range matched[from:to] {
		el := rows[i]
		if el.ClusterID() == "" || words[el.Key()] != "" || clusters[el.TSG+"/"+el.ClusterID()] != "" {
			continue
		}
		out[el.TSG] = append(out[el.TSG], el.ID)
	}
	rt.mu.RUnlock()
	return out
}

func haCommands() []Command {
	return []Command{{
		Name: "ha.sweep", Slow: true, Help: "read every clustered element's HA state, the tenant-wide query first",
		Run: func(rt *Runtime, c *Call) error {
			if err := rt.fleetLoaded(c); err != nil {
				return err
			}
			byTenant := map[string][]api.Element{}
			for _, r := range rt.Elements() {
				byTenant[r.TSG] = append(byTenant[r.TSG], r.Element)
			}
			if len(byTenant) == 0 {
				return errNoElements
			}
			tsgs := make([]string, 0, len(byTenant))
			for tsg := range byTenant {
				tsgs = append(tsgs, tsg)
			}
			sort.Strings(tsgs)
			for _, tsg := range tsgs {
				w := HAClusters(byTenant[tsg])
				if w.Clusters() == 0 {
					continue
				}
				rt.StartHA(tsg, w)
				res := rt.FetchHABulk(c.Ctx, tsg, w, w.Elements)
				for {
					next := rt.ApplyHA(res)
					if next.Save != nil {
						rt.SaveHA(tsg, next.Save)
					}
					switch {
					case next.Tick:
						res = rt.FetchHABulk(c.Ctx, tsg, w, rt.Queued(tsg))
						continue
					case len(next.Pairs) > 0:
						res = rt.FetchHAPairs(c.Ctx, tsg, next.Pairs)
						continue
					}
					break
				}
			}
			h := rt.HA()
			c.Say("%d/%d clusters read, %d failed", h.Done, h.Total, h.Failed)
			if h.Failed > 0 {
				c.Say("first failure: %s", h.Err)
			}
			return nil
		},
	}}
}

var errNoElements = errString("no elements: no tenant to load them from")

type errString string

func (e errString) Error() string { return string(e) }

func (rt *Runtime) ResetHAProgress() {
	rt.editHA(func(h *haState) { h.failed, h.err, h.total, h.done, h.bulk = 0, "", 0, 0, "" })
}

func (rt *Runtime) ResetHALive() {
	rt.editHA(func(h *haState) {
		h.words, h.clusters = map[string]string{}, map[string]string{}
		h.queue, h.inFlight = map[string][]string{}, map[string]bool{}
		h.failed, h.err, h.total, h.done, h.bulk = 0, "", 0, 0, ""
	})
}
