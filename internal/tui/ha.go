package tui

import (
	"fmt"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

const (
	haActive  = runtime.HAActive
	haStandby = runtime.HAStandby
	haUnknown = runtime.HAUnknown
	haBatch   = runtime.HABatch
)

func (m tuiModel) haWorking() bool { return m.rt != nil && m.rt.HAWorking() }

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
			h.Done, h.Total, m.runtime().SweepETA(m.tenant().TSGID, h.Total-h.Done), h.Bulk)
	}
	return fmt.Sprintf("%d/%d clusters", h.Done, h.Total)
}
