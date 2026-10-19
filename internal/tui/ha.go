package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

// The per-cluster endpoint says only "active" or "unknown", so standby is
// derived. No active member keeps "unknown": that cluster is in trouble.
const (
	haActive  = "active"
	haStandby = "standby"
	haUnknown = "unknown"
)

const haAnswered = "ok"

// The aggregate names elements; the fallback needs a site in the path, so an
// element with no site id can only be read by the aggregate.
type haWork struct {
	// one request answers for every member, so a pair is one call
	pairs    [][2]string
	elements []string
	noSite   []string
}

func (w haWork) clusters() int { return len(w.pairs) + len(w.noSite) }

func haClusters(els []api.Element) haWork {
	var w haWork
	seen := map[string]bool{}
	for _, el := range els {
		cid := el.ClusterID()
		if cid == "" {
			continue
		}
		w.elements = append(w.elements, el.ID)
		if seen[cid] {
			continue
		}
		seen[cid] = true
		if el.SiteID == "" {
			w.noSite = append(w.noSite, cid)
			continue
		}
		w.pairs = append(w.pairs, [2]string{el.SiteID, cid})
	}
	return w
}

// slower than the limiter allows, so a search can jump the queue
const haPace = 400 * time.Millisecond

type haTickMsg struct{ tsg string }

func haTick(tsg string) tea.Cmd {
	return tea.Tick(haPace, func(time.Time) tea.Msg { return haTickMsg{tsg: tsg} })
}

// was a hard 64 with the rest dropped, so nothing past it ever resolved
const haBatch = 16

// the client rate-limits anyway; this keeps the limiter saturated
const haFetchWorkers = 8

type haStatesMsg struct {
	tsg    string
	states map[string]string
	// "" not tried, "used" answered, otherwise why it did not
	bulk string
	// an element the reply did not name is not one still waiting
	answered []string
	// cluster -> why, for the cells that cannot be filled
	failed map[string]string
	// what this pass did not reach; the aggregate's half asks by element
	rest         [][2]string
	restElements []string
}

// anything unexpected falls back, rather than showing a fleet with no HA
func loadHABulk(ctx context.Context, c *api.Client, tsg string, w haWork) tea.Cmd {
	return loadHAChunk(ctx, c, tsg, w, w.elements)
}

func loadHAChunk(ctx context.Context, c *api.Client, tsg string, w haWork, ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	// saying so beats a column stuck on "loading"
	if c == nil {
		return func() tea.Msg {
			return haStatesMsg{tsg: tsg, bulk: "no client for this tenant", failed: allFailed(w, "tenant unreachable")}
		}
	}

	ask, rest := ids, []string(nil)
	if len(ask) > api.ClusterQueryLimit {
		ask, rest = ids[:api.ClusterQueryLimit], ids[api.ClusterQueryLimit:]
	}

	return func() tea.Msg {
		sts, err := c.SpokeClusterStatuses(ctx, ask)
		if err != nil {
			// clusters the fallback cannot reach either are marked, not left
			msg := haStatesMsg{tsg: tsg, bulk: shortTenantErr(err), rest: w.pairs}
			if len(w.noSite) > 0 {
				msg.failed = map[string]string{}
				for _, cid := range w.noSite {
					msg.failed[cid] = "no site id in the element record"
				}
			}
			return msg
		}

		// this endpoint names the second node itself
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
		return haStatesMsg{
			tsg: tsg, states: states, answered: answered,
			bulk: "used", restElements: rest,
		}
	}
}

// "backup" is the portal's own word; the per-cluster endpoint has none
func bulkWord(status string) string {
	switch status {
	case haActive:
		return haActive
	case "backup":
		return haStandby
	case "":
		return haUnknown
	}
	return status
}

func memberWord(mem api.ClusterMember, hasActive bool) string {
	switch {
	case mem.Status == haActive:
		return haActive
	case hasActive:
		return haStandby
	case mem.Status != "":
		return mem.Status
	}
	return haUnknown
}

// runs behind the list: the elements are what someone is looking for
func loadHA(ctx context.Context, c *api.Client, tsg string, pairs [][2]string) tea.Cmd {
	if c == nil || len(pairs) == 0 {
		return nil
	}
	var rest [][2]string
	if len(pairs) > haBatch {
		pairs, rest = pairs[:haBatch], pairs[haBatch:]
	}

	return func() tea.Msg {
		var mu sync.Mutex
		states := map[string]string{}
		answered := make([]string, 0, len(pairs))
		failed := map[string]string{}

		var wg sync.WaitGroup
		work := make(chan [2]string)
		for i := 0; i < min(haFetchWorkers, len(pairs)); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range work {
					st, err := c.SpokeClusterStatus(ctx, p[0], p[1])
					if err != nil {
						// swallowing this left the column loading forever
						mu.Lock()
						failed[p[1]] = shortTenantErr(err)
						mu.Unlock()
						continue
					}
					_, hasActive := st.Active()

					words := make(map[string]string, len(st.Members))
					for _, mem := range st.Members {
						words[mem.ElementID] = memberWord(mem, hasActive)
					}

					mu.Lock()
					answered = append(answered, p[1])
					for eid, w := range words {
						states[tsg+"/"+eid] = w
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

		return haStatesMsg{tsg: tsg, states: states, answered: answered, failed: failed, rest: rest}
	}
}

func (m tuiModel) onHAStates(msg haStatesMsg) (tea.Model, tea.Cmd) {
	if m.ha == nil {
		m.ha = map[string]string{}
	}
	if m.haCluster == nil {
		m.haCluster = map[string]string{}
	}
	for k, v := range msg.states {
		m.ha[k] = v
	}
	if msg.bulk != "" {
		m.haBulk = msg.bulk
	}
	delete(m.haInFlight, msg.tsg)
	m.haDone += len(msg.answered) + len(msg.failed)
	for _, cid := range msg.answered {
		m.haCluster[msg.tsg+"/"+cid] = haAnswered
	}
	for cid, why := range msg.failed {
		m.haCluster[msg.tsg+"/"+cid] = why
		m.haFailed++
		if m.haErr == "" {
			m.haErr = why
		}
	}
	// once the sweep is done, so the count is the whole of it
	if len(msg.rest) == 0 && m.haFailed > 0 && m.status == "" {
		m.status = fmt.Sprintf("HA state unread for %d cluster(s): %s", m.haFailed, m.haErr)
	}
	// a fleet one at a time is minutes, so say so up front
	if msg.bulk != "" && msg.bulk != "used" && m.status == "" {
		m.status = fmt.Sprintf("HA one cluster at a time (%s): the tenant-wide query was refused — %s",
			sweepETA(m.haTotal), msg.bulk)
	}
	m.rebuildRows()

	// queued, not chased: the rate budget stays free for the user
	if len(msg.restElements) > 0 {
		if m.haQueue == nil {
			m.haQueue = map[string][]string{}
		}
		m.haQueue[msg.tsg] = msg.restElements
		return m, tea.Batch(haTick(msg.tsg), saveHA(m.store, msg.tsg, msg.states))
	}
	delete(m.haQueue, msg.tsg)

	if len(msg.rest) > 0 {
		return m, loadHA(m.ctx, m.pool.get(msg.tsg), msg.tsg, m.nextPairs(msg.tsg, msg.rest))
	}
	return m, saveHA(m.store, msg.tsg, msg.states)
}

func (m tuiModel) onHATick(tsg string) (tea.Model, tea.Cmd) {
	left := m.haQueue[tsg]
	if len(left) == 0 {
		return m, nil
	}
	w := haWork{elements: m.visibleFirst(tsg, left)}
	return m, loadHAChunk(m.ctx, m.pool.get(tsg), tsg, w, w.elements)
}

// the ask that must not wait behind the fleet
func (m tuiModel) haWanted() map[string][]string {
	out := map[string][]string{}
	for _, pos := range m.visibleMatches() {
		el := m.all[pos]
		if el.ClusterID() == "" {
			continue
		}
		if m.ha[el.key()] != "" || m.haCluster[el.TSG+"/"+el.ClusterID()] != "" {
			continue
		}
		out[el.TSG] = append(out[el.TSG], el.ID)
	}
	return out
}

func (m tuiModel) visibleMatches() []int {
	if len(m.matches) == 0 {
		return nil
	}
	h := max(4, m.listHeight())
	at := m.table.Cursor()
	from, to := max(0, at-h), min(len(m.matches), at+h)
	out := make([]int, 0, to-from)
	for _, i := range m.matches[from:to] {
		out = append(out, i)
	}
	return out
}

// ahead of the paced fill; one per tenant, so a search cannot double up
func (m *tuiModel) askVisibleHA() tea.Cmd {
	if m.haInFlight == nil {
		m.haInFlight = map[string]bool{}
	}
	var cmds []tea.Cmd
	for tsg, ids := range m.haWanted() {
		if m.haInFlight[tsg] {
			continue
		}
		m.haInFlight[tsg] = true
		w := haWork{elements: ids}
		if cmd := loadHAChunk(m.ctx, m.pool.get(tsg), tsg, w, ids); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// so the next launch opens with something while it reads again
func saveHA(st *config.Store, tsg string, states map[string]string) tea.Cmd {
	if st == nil || len(states) == 0 {
		return nil
	}
	// the cache keys on element id; the model scopes by tenant
	bare := make(map[string]string, len(states))
	for k, w := range states {
		bare[strings.TrimPrefix(k, tsg+"/")] = w
	}
	return func() tea.Msg {
		_ = st.SaveHA(tsg, bare)
		return nil
	}
}

// blank off a cluster: a dash on every standalone row is noise
func (m *tuiModel) haCell(r elementRow) string {
	word, cached := m.haState(r)
	if word == "" {
		return ""
	}
	return haWord(word, cached)
}

// unstyled: the list builds every row per keystroke, paints a few
func (m *tuiModel) haState(r elementRow) (string, bool) {
	if r.ClusterID() == "" {
		return "", false
	}
	if word := m.ha[r.key()]; word != "" {
		return word, false
	}
	// the asterisk says last sweep; stale and labelled beats empty
	if word := m.haCached[r.key()]; word != "" {
		return word, true
	}

	// three different nothings; "loading" has to mean only the first
	switch {
	case r.SiteID == "":
		// the status path is per site, so this one cannot be asked
		return "ha?", false
	case m.haCluster[r.TSG+"/"+r.ClusterID()] == haAnswered:
		// answered and did not name it: the API's word for unseen
		return haUnknown, false
	case m.haCluster[r.TSG+"/"+r.ClusterID()] != "":
		return "ha?", false
	}
	return "loading", false
}

// the asterisk is dim so the word still reads at a glance
func haWord(word string, cached bool) string {
	var out string
	switch word {
	case "":
		return ""
	case "loading":
		out = ui.StyleDim.Render(word)
	case "ha?":
		out = ui.StyleWarn.Render(word)
	case haActive:
		// a connected row is already green
		out = ui.StyleOK.Bold(true).Render(haActive)
	case haStandby:
		// dim against green read as the same colour
		out = ui.StyleElement.Render(haStandby)
	case haUnknown:
		out = ui.StyleWarn.Render(haUnknown)
	default:
		out = ui.StyleDim.Render(word)
	}
	if cached {
		out += ui.StyleDim.Render("*")
	}
	return out
}

// counts the list, not the sweep: a refresh resets haTotal
func (m tuiModel) haPath() string {
	clustered, clusters := 0, map[string]bool{}
	for _, r := range m.all {
		if cid := r.ClusterID(); cid != "" {
			clustered++
			clusters[r.TSG+"/"+cid] = true
		}
	}

	switch {
	case clustered == 0:
		return "no clustered elements in the list"
	case m.haTotal == 0:
		return fmt.Sprintf("%d clustered elements in %d clusters — nothing asked yet",
			clustered, len(clusters))
	case m.haBulk == "used":
		return fmt.Sprintf("%d clusters in one tenant-wide query", m.haTotal)
	case m.haBulk != "":
		return fmt.Sprintf("%d/%d clusters one at a time (%s) — the query was refused: %s",
			m.haDone, m.haTotal, sweepETA(m.haTotal-m.haDone), m.haBulk)
	}
	return fmt.Sprintf("%d/%d clusters", m.haDone, m.haTotal)
}

// one request per cluster through the limiter
func sweepETA(left int) string {
	if left <= 0 {
		return "done"
	}
	rps := app.Defaults.RPS
	if rps <= 0 {
		rps = 1
	}
	return ui.Age(time.Duration(float64(left)/rps*float64(time.Second))) + " to go"
}

// on screen first, outside the filter last: the order is what feels fast
func (m tuiModel) nextPairs(tsg string, rest [][2]string) [][2]string {
	if len(rest) < 2 {
		return rest
	}

	at := make(map[string]int, len(rest))
	for pos, i := range m.matches {
		el := m.all[i]
		if el.TSG != tsg {
			continue
		}
		if cid := el.ClusterID(); cid != "" {
			if _, seen := at[cid]; !seen {
				at[cid] = pos
			}
		}
	}

	cursor := m.table.Cursor()
	rank := func(p [2]string) int {
		pos, ok := at[p[1]]
		if !ok {
			return 1 << 30
		}
		// the window is around the cursor, not at the top
		if pos >= cursor {
			return pos - cursor
		}
		return cursor - pos
	}

	out := append([][2]string(nil), rest...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func clusteredElements(els []api.Element) []string {
	out := make([]string, 0, len(els))
	for _, el := range els {
		if el.ClusterID() != "" {
			out = append(out, el.ID)
		}
	}
	return out
}

func clusterIDs(pairs [][2]string) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p[1])
	}
	return out
}

func allFailed(w haWork, why string) map[string]string {
	out := make(map[string]string, w.clusters())
	for _, cid := range append(clusterIDs(w.pairs), w.noSite...) {
		out[cid] = why
	}
	return out
}

// the portal asks about the 25 it shows, not the 5600 it has
func (m tuiModel) visibleFirst(tsg string, ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	at := make(map[string]int, len(m.matches))
	for pos, i := range m.matches {
		if el := m.all[i]; el.TSG == tsg {
			at[el.ID] = pos
		}
	}

	cursor := m.table.Cursor()
	rank := func(id string) int {
		pos, ok := at[id]
		if !ok {
			return 1 << 30
		}
		if pos >= cursor {
			return pos - cursor
		}
		return cursor - pos
	}

	out := append([]string(nil), ids...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}
