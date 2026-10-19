package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

// HA state as the element list shows it. The portal has no "standby": a
// cluster's members read "active" on the one carrying traffic and "unknown"
// on anything the controller cannot see. So standby is derived -- a member
// that is not active, in a cluster that has an active member, is the standby
// by elimination -- and a cluster with nobody active keeps the API's own word,
// because that is a cluster in trouble rather than a quiet second node.
const (
	haActive  = "active"
	haStandby = "standby"
	haUnknown = "unknown"
)

// haClusters is the distinct (site, cluster) pairs among these elements. One
// request answers for every member of a cluster, so a pair costs one call and
// not two.
func haClusters(els []api.Element) [][2]string {
	seen := map[string]bool{}
	var out [][2]string
	for _, el := range els {
		cid := el.ClusterID()
		if cid == "" || el.SiteID == "" || seen[cid] {
			continue
		}
		seen[cid] = true
		out = append(out, [2]string{el.SiteID, cid})
	}
	return out
}

// haFetchLimit bounds one refresh. A fleet of HA pairs is a request per pair,
// and the client's rate limit is five a second, so an unbounded sweep would
// still be filling the column minutes later. Past the limit the cells stay
// empty rather than wrong.
const haFetchLimit = 64

// haFetchWorkers is how many run at once. The client rate-limits anyway; this
// only stops a thousand goroutines queueing behind it.
const haFetchWorkers = 4

type haStatesMsg struct {
	tsg    string
	states map[string]string
	// capped is set when there were more clusters than one refresh will read.
	capped bool
}

// loadHA reads each cluster's status and reduces it to one word per element.
// It runs after the list is on screen: the elements are what someone is
// looking for, and HA is what they want to know about the one they found.
func loadHA(ctx context.Context, c *api.Client, tsg string, pairs [][2]string) tea.Cmd {
	if c == nil || len(pairs) == 0 {
		return nil
	}
	capped := false
	if len(pairs) > haFetchLimit {
		pairs, capped = pairs[:haFetchLimit], true
	}

	return func() tea.Msg {
		var mu sync.Mutex
		states := map[string]string{}

		var wg sync.WaitGroup
		work := make(chan [2]string)
		for i := 0; i < min(haFetchWorkers, len(pairs)); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range work {
					st, err := c.SpokeClusterStatus(ctx, p[0], p[1])
					if err != nil {
						continue
					}
					_, hasActive := st.Active()

					words := make(map[string]string, len(st.Members))
					for _, mem := range st.Members {
						switch {
						case mem.Status == haActive:
							words[mem.ElementID] = haActive
						case hasActive:
							words[mem.ElementID] = haStandby
						case mem.Status != "":
							words[mem.ElementID] = mem.Status
						default:
							words[mem.ElementID] = haUnknown
						}
					}

					mu.Lock()
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

		return haStatesMsg{tsg: tsg, states: states, capped: capped}
	}
}

func (m tuiModel) onHAStates(msg haStatesMsg) (tea.Model, tea.Cmd) {
	if m.ha == nil {
		m.ha = map[string]string{}
	}
	for k, v := range msg.states {
		m.ha[k] = v
	}
	if msg.capped && m.status == "" {
		m.status = "HA read for the first 64 clusters; alt+r for another pass"
	}
	m.rebuildRows()
	return m, nil
}

// haCell is the column. Blank for an element that is not in a cluster, which
// is most of a fleet: a dash on every standalone row would be noise.
func (m tuiModel) haCell(r elementRow) string {
	if r.ClusterID() == "" {
		return ""
	}
	switch m.ha[r.key()] {
	case haActive:
		return ui.StyleOK.Render(haActive)
	case haStandby:
		return ui.StyleDim.Render(haStandby)
	case haUnknown:
		return ui.StyleWarn.Render(haUnknown)
	}
	// In a cluster, but its status has not come back yet.
	return ui.StyleDim.Render("ha")
}
