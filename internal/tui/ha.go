package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/ui"
)

const (
	haActive  = runtime.HAActive
	haStandby = runtime.HAStandby
	haUnknown = runtime.HAUnknown
	haBatch   = runtime.HABatch
)

type haWork struct {
	pairs    [][2]string
	elements []string
	noSite   []string
}

func (w haWork) clusters() int { return len(w.pairs) + len(w.noSite) }

func (w haWork) runtime() runtime.HAWork {
	return runtime.HAWork{Pairs: w.pairs, Elements: w.elements, NoSite: w.noSite}
}

func haClusters(els []api.Element) haWork {
	w := runtime.HAClusters(els)
	return haWork{pairs: w.Pairs, elements: w.Elements, noSite: w.NoSite}
}

const haPace = 400 * time.Millisecond

type haTickMsg struct{ tsg string }

func haTick(tsg string) tea.Cmd {
	return tea.Tick(haPace, func(time.Time) tea.Msg { return haTickMsg{tsg: tsg} })
}

type haStatesMsg struct {
	tsg          string
	states       map[string]string
	bulk         string
	answered     []string
	failed       map[string]string
	rest         [][2]string
	restElements []string
}

func haMsg(r runtime.HAResult) haStatesMsg {
	return haStatesMsg{tsg: r.TSG, states: r.States, bulk: r.Bulk, answered: r.Answered,
		failed: r.Failed, rest: r.Rest, restElements: r.RestElements}
}

func (msg haStatesMsg) result() runtime.HAResult {
	return runtime.HAResult{TSG: msg.tsg, States: msg.states, Bulk: msg.bulk, Answered: msg.answered,
		Failed: msg.failed, Rest: msg.rest, RestElements: msg.restElements}
}

func loadHABulk(ctx context.Context, rt *runtime.Runtime, tsg string, w haWork) tea.Cmd {
	return loadHAChunk(ctx, rt, tsg, w, w.elements)
}

func loadHAChunk(ctx context.Context, rt *runtime.Runtime, tsg string, w haWork, ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg { return haMsg(rt.FetchHABulk(ctx, tsg, w.runtime(), ids)) }
}

func loadHA(ctx context.Context, rt *runtime.Runtime, tsg string, pairs [][2]string) tea.Cmd {
	if len(pairs) == 0 || rt.Clients().Get(tsg) == nil {
		return nil
	}
	return func() tea.Msg { return haMsg(rt.FetchHAPairs(ctx, tsg, pairs)) }
}

func (m tuiModel) onHAStates(msg haStatesMsg) (tea.Model, tea.Cmd) {
	next := m.runtime().ApplyHA(msg.result())
	h := m.runtime().HA()
	m.forgetHARows(msg)
	if len(msg.rest) == 0 && h.Failed > 0 && m.status == "" {
		m.status = fmt.Sprintf("HA state unread for %d cluster(s): %s", h.Failed, h.Err)
	}
	if msg.bulk != "" && msg.bulk != "used" && m.status == "" {
		m.status = fmt.Sprintf("HA one cluster at a time (%s): the tenant-wide query was refused — %s",
			sweepETA(h.Total), msg.bulk)
	}
	m.rebuildRows()

	switch {
	case next.Tick:
		return m, tea.Batch(haTick(msg.tsg), saveHA(m.runtime(), msg.tsg, next.Save))
	case len(next.Pairs) > 0:
		return m, loadHA(m.ctx, m.runtime(), msg.tsg, next.Pairs)
	}
	return m, saveHA(m.runtime(), msg.tsg, next.Save)
}

func (m tuiModel) onHATick(tsg string) (tea.Model, tea.Cmd) {
	left := m.runtime().Queued(tsg)
	if len(left) == 0 {
		return m, nil
	}
	w := haWork{elements: m.visibleFirst(tsg, left)}
	m.markHAOut(tsg)
	return m, loadHAChunk(m.ctx, m.runtime(), tsg, w, w.elements)
}

func (m tuiModel) haWanted() map[string][]string {
	m.runtime().SetViewport(m.listHeight())
	return m.runtime().HAWanted()
}

func (m tuiModel) visibleMatches() []int {
	if len(m.matched()) == 0 {
		return nil
	}
	h := max(4, m.listHeight())
	at := m.cursor()
	from, to := max(0, at-h), min(len(m.matched()), at+h)
	return append([]int(nil), m.matched()[from:to]...)
}

func (m *tuiModel) markHAOut(tsg string) { m.runtime().MarkHAOut(tsg) }

func (m tuiModel) haWorking() bool { return m.rt != nil && m.rt.HAWorking() }

func (m *tuiModel) askVisibleHA() tea.Cmd {
	var cmds []tea.Cmd
	for tsg, ids := range m.haWanted() {
		if m.runtime().HAInFlight(tsg) {
			continue
		}
		m.markHAOut(tsg)
		if cmd := loadHAChunk(m.ctx, m.runtime(), tsg, haWork{elements: ids}, ids); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func saveHA(rt *runtime.Runtime, tsg string, states map[string]string) tea.Cmd {
	if len(states) == 0 {
		return nil
	}
	return func() tea.Msg {
		rt.SaveHA(tsg, states)
		return nil
	}
}

func (m *tuiModel) forgetHARows(msg haStatesMsg) {
	for k := range msg.states {
		m.forgetRow(k)
	}
	if len(msg.answered) == 0 && len(msg.failed) == 0 {
		return
	}
	clusters := make(map[string]bool, len(msg.answered)+len(msg.failed))
	for _, cid := range msg.answered {
		clusters[cid] = true
	}
	for cid := range msg.failed {
		clusters[cid] = true
	}
	for _, el := range m.rows() {
		if el.TSG == msg.tsg && clusters[el.ClusterID()] {
			m.forgetRow(el.Key())
		}
	}
}

func (m *tuiModel) haCell(r elementRow) string {
	word, cached := m.haState(r)
	if word == "" {
		return ""
	}
	return haWord(word, cached)
}

func (m *tuiModel) haState(r elementRow) (string, bool) { return m.runtime().HAState(r) }

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
		out = ui.StyleOK.Bold(true).Render(haActive)
	case haStandby:
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

func (m tuiModel) haPath() string {
	clustered, clusters := 0, map[string]bool{}
	for _, r := range m.rows() {
		if cid := r.ClusterID(); cid != "" {
			clustered++
			clusters[r.TSG+"/"+cid] = true
		}
	}
	h := m.runtime().HA()
	switch {
	case clustered == 0:
		return "no clustered elements in the list"
	case h.Total == 0:
		return fmt.Sprintf("%d clustered elements in %d clusters — nothing asked yet",
			clustered, len(clusters))
	case h.Bulk == "used":
		return fmt.Sprintf("%d clusters in one tenant-wide query", h.Total)
	case h.Bulk != "":
		return fmt.Sprintf("%d/%d clusters one at a time (%s) — the query was refused: %s",
			h.Done, h.Total, sweepETA(h.Total-h.Done), h.Bulk)
	}
	return fmt.Sprintf("%d/%d clusters", h.Done, h.Total)
}

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

func (m tuiModel) visibleFirst(tsg string, ids []string) []string {
	return m.runtime().NearestElements(tsg, ids)
}
