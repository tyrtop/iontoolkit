package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
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
	rt.changed(Event{Area: AreaHA, Rows: true})
}

func (rt *Runtime) SetCachedHA(words map[string]string) {
	rt.editHA(func(h *haState) {
		for k, v := range words {
			h.cached[k] = v
		}
	})
	rt.changed(Event{Area: AreaHA, Rows: true})
}

func (rt *Runtime) SetHAProgress(total, done int, at time.Time) {
	rt.editHA(func(h *haState) { h.total, h.done, h.at = total, done, at })
}

func (rt *Runtime) ResetHA() {
	rt.mu.Lock()
	rt.ha = haState{}
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaHA, Rows: true})
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
	rt.changed(Event{Area: AreaHA, Rows: true, Keys: rt.haKeys(res)})
	if h := rt.HA(); len(res.Rest) == 0 && h.Failed > 0 {
		rt.note(fmt.Sprintf("HA state unread for %d cluster(s): %s", h.Failed, h.Err))
	}
	if res.Bulk != "" && res.Bulk != "used" {
		rt.note("HA one cluster at a time (" + rt.SweepETA(res.TSG, rt.HA().Total) + "): the tenant-wide query was refused — " + res.Bulk)
	}
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

func (rt *Runtime) haKeys(res HAResult) []string {
	keys := make([]string, 0, len(res.States))
	for k := range res.States {
		keys = append(keys, k)
	}
	if len(res.Answered) == 0 && len(res.Failed) == 0 {
		return keys
	}
	clusters := make(map[string]bool, len(res.Answered)+len(res.Failed))
	for _, cid := range res.Answered {
		clusters[cid] = true
	}
	for cid := range res.Failed {
		clusters[cid] = true
	}
	for _, el := range rt.Elements() {
		if el.TSG == res.TSG && clusters[el.ClusterID()] {
			keys = append(keys, el.Key())
		}
	}
	return keys
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
	path, err := rt.ListPath(column.HAQuery, nil)
	var sts []api.ClusterMemberStatus
	if err == nil {
		sts, err = c.SpokeClusterStatuses(ctx, path, ask)
	}
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

func (rt *Runtime) haWork(tsg string, ids []string) HAWork {
	byID := rt.elementsByID(tsg, ids)
	els := make([]api.Element, 0, len(ids))
	for _, id := range ids {
		if el, ok := byID[id]; ok {
			els = append(els, el.Element)
		}
	}
	return HAClusters(els)
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
				path, err := rt.ListPath(column.HACluster, map[string]string{"site_id": p[0], "cluster_id": p[1]})
				var st api.SpokeClusterStatus
				if err == nil {
					st, err = c.SpokeClusterStatus(ctx, path, p[1])
				}
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
				rt.SweepHA(c.Ctx, tsg, w, 0)
			}
			h := rt.HA()
			c.Say("%d/%d clusters read, %d failed", h.Done, h.Total, h.Failed)
			if h.Failed > 0 {
				c.Say("first failure: %s", h.Err)
			}
			return nil
		},
	}, {
		Name: "ha.list", Slow: true,
		Help: "each clustered element's HA state in one tenant, by element id: what has been read already, else the tenant-wide query",
		Args: []Arg{{Name: "tenant", Help: "by name or tsg; the current tenant when empty"},
			{Name: "refresh", Bool: true, Help: "ask the portal again, even for what has been read"}},
		Run: func(rt *Runtime, c *Call) error {
			t, ok := rt.Tenant()
			if v := c.Arg("tenant"); v != "" {
				found, err := rt.FindTenant(v)
				if err != nil {
					return err
				}
				t, ok = found, true
			}
			if !ok {
				return fmt.Errorf("no tenant: say which with tenant=")
			}
			states, asked, err := rt.HAStates(c.Ctx, t, c.Arg("refresh") == "yes")
			if err != nil {
				return err
			}
			n := map[string]int{}
			out := make(map[string]any, len(states))
			for id, w := range states {
				n[w]++
				out[id] = w
			}
			how := "read already"
			if asked > 0 {
				how = fmt.Sprintf("%d asked of the portal", asked)
			}
			c.Say("%s: %d clustered elements, %d active, %d standby, %d unknown (%s)",
				t.Name, len(states), n[HAActive], n[HAStandby], len(states)-n[HAActive]-n[HAStandby], how)
			c.Result = out
			return nil
		},
	}}
}

func (rt *Runtime) HAStates(ctx context.Context, t config.Tenant, refresh bool) (map[string]string, int, error) {
	els := rt.tenantElements(t.TSGID)
	if len(els) == 0 {
		l := rt.LoadTenant(ctx, t, false)
		if l.Err != nil {
			return nil, 0, fmt.Errorf("%s: %s", t.Name, rt.explain(l.Err))
		}
		rt.MergeSites(l.Sites)
		rt.MergeTenant(l.TSG, l.Tenant, l.Elements)
		els = rt.tenantElements(t.TSGID)
	}
	var ask []api.Element
	rt.mu.RLock()
	for _, el := range els {
		if el.ClusterID() != "" && (refresh || rt.ha.words[el.Key()] == "") {
			ask = append(ask, el.Element)
		}
	}
	rt.mu.RUnlock()
	if w := HAClusters(ask); w.Clusters() > 0 {
		if _, err := rt.Connect(ctx, t); err != nil {
			return nil, 0, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
		}
		rt.SweepHA(ctx, t.TSGID, w, 0)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	out := map[string]string{}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, el := range els {
		if el.ClusterID() == "" {
			continue
		}
		w := rt.ha.words[el.Key()]
		if w == "" {
			w = HAUnknown
		}
		out[el.ID] = w
	}
	return out, len(ask), nil
}

func (rt *Runtime) tenantElements(tsg string) []Element {
	var out []Element
	for _, el := range rt.Elements() {
		if el.TSG == tsg {
			out = append(out, el)
		}
	}
	return out
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
	rt.changed(Event{Area: AreaHA, Rows: true})
}
