package tui

import (
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type tabKind int

const (
	tabList tabKind = iota
	tabShell
	tabDialing
	tabData
)

type tab struct {
	kind tabKind
	name string
	eid  string
	at   int
	sess *ionterm.Session
	data runtime.DataTab
}

func (m tuiModel) tabs() []tab {
	out := []tab{{kind: tabList}}
	for i, s := range m.sessions().All() {
		out = append(out, tab{kind: tabShell, name: s.Name, eid: s.ElementID, at: i, sess: s})
	}
	for _, d := range m.runtime().Opening() {
		name := d.Name
		if name == "" {
			name = d.EID
		}
		out = append(out, tab{kind: tabDialing, name: name, eid: d.EID})
	}
	for i, d := range m.dataTabs() {
		out = append(out, tab{kind: tabData, name: d.Name, at: i, data: d})
	}
	return out
}

func (m tuiModel) frontTab(tabs []tab) int {
	switch m.phase {
	case phaseSession:
		shell, active := 0, m.sessions().ActiveIndex()
		for i, t := range tabs {
			switch {
			case t.kind == tabDialing && t.eid == m.frontDial:
				return i
			case t.kind == tabShell && t.at == active:
				shell = i
			}
		}
		return shell
	case phaseData:
		active := m.runtime().DataActive()
		for i, t := range tabs {
			if t.kind == tabData && t.at == active {
				return i
			}
		}
	}
	return 0
}

func (m tuiModel) dialingFront() (tab, bool) {
	tabs := m.tabs()
	t := tabs[m.frontTab(tabs)]
	return t, t.kind == tabDialing
}

func (m tuiModel) frontSession() *ionterm.Session {
	if _, dialing := m.dialingFront(); dialing {
		return nil
	}
	return m.sessions().Active()
}

func (m tuiModel) detachedCount() int { return m.runtime().DetachedCount() }

func (m tuiModel) hasTabs() bool {
	return len(m.tabs()) > 1 || m.detachedCount() > 0
}

func (m tuiModel) stepTab(by int) tuiModel {
	tabs := m.tabs()
	n := len(tabs)
	return m.showTab(tabs[((m.frontTab(tabs)+by)%n+n)%n])
}

func (m tuiModel) showTab(t tab) tuiModel {
	m.frontDial = ""
	switch t.kind {
	case tabShell:
		m.sessions().Select(t.at)
		m.phase, m.sesScroll = phaseSession, 0
	case tabDialing:
		m.frontDial = t.eid
		m.phase, m.sesScroll = phaseSession, 0
	case tabData:
		m.runtime().SelectData(t.at)
		m.phase = phaseData
	default:
		m.phase = phasePicking
	}
	return m
}

func (m tuiModel) tabBar() string {
	tabs := m.tabs()
	cur := m.frontTab(tabs)
	parts := make([]string, 0, len(tabs))
	shells, data := 0, 0
	for i, t := range tabs {
		switch t.kind {
		case tabList:
			parts = append(parts, ui.StyleTab(ui.TabList, i, 0, i == cur).Render(fmt.Sprintf(" elements (%s) ", m.mode())))
		case tabShell, tabDialing:
			mark := ""
			switch {
			case t.sess != nil && t.sess.Err() != nil:
				mark = ui.MarkCross.String() + " "
			case t.sess != nil && t.sess.Hung():
				mark = "~ "
			}
			parts = append(parts, ui.StyleTab(ui.TabShell, i, shells, i == cur).Render(fmt.Sprintf(" %s%s ", mark, t.name)))
			shells++
		case tabData:
			mark := ""
			if t.data.Failed() {
				mark = ui.MarkCross.String() + " "
			}
			parts = append(parts, ui.StyleTab(ui.TabData, i, data, i == cur).Render(fmt.Sprintf(" %s%s · data ", mark, t.name)))
			data++
		}
	}
	bar := strings.Join(parts, " ")
	if n := m.detachedCount(); n > 0 {
		bar += ui.StyleDim.Render(fmt.Sprintf("  +%d detached", n))
	}
	return bar
}
