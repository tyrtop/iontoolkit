package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/runtime"
)

type runtimeMsg struct {
	rt     *runtime.Runtime
	events []runtime.Event
}

type runtimeFeed struct {
	mu      sync.Mutex
	send    func(tea.Msg)
	watched map[*runtime.Runtime]bool
}

var feed *runtimeFeed

func startFeed(send func(tea.Msg), rt *runtime.Runtime) {
	feed = &runtimeFeed{send: send, watched: map[*runtime.Runtime]bool{}}
	watchRuntime(rt)
}

func watchRuntime(rt *runtime.Runtime) {
	f := feed
	if f == nil || rt == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.watched[rt] {
		return
	}
	f.watched[rt] = true
	go f.forward(rt)
}

func (f *runtimeFeed) forward(rt *runtime.Runtime) {
	defer func() {
		f.mu.Lock()
		delete(f.watched, rt)
		f.mu.Unlock()
	}()
	ch := rt.Changes()
	for ev := range ch {
		msg := runtimeMsg{rt: rt, events: []runtime.Event{ev}}
	more:
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					break more
				}
				msg.events = append(msg.events, ev)
			default:
				break more
			}
		}
		f.send(msg)
	}
}

func (m tuiModel) onRuntime(msg runtimeMsg) (tea.Model, tea.Cmd) {
	if msg.rt != m.rt {
		return m, nil
	}
	list, all := false, false
	var keys []string
	for _, ev := range msg.events {
		switch {
		case ev.Area == runtime.AreaStatus && ev.Status != "" && m.status == "":
			m.status = ev.Status
		case !ev.Rows:
		case ev.Area == runtime.AreaList:
			list = true
		case ev.Keys == nil:
			all = true
		default:
			keys = append(keys, ev.Keys...)
		}
	}
	switch {
	case list:
		m.listChanged()
	case all:
		m.forgetRows()
		m.rebuildRows()
	case len(keys) > 0:
		for _, k := range keys {
			m.forgetRow(k)
		}
		m.rebuildRows()
	}
	if m.phase == phaseLoading && len(m.rows()) > 0 {
		m.phase = phasePicking
	}
	return m, nil
}

func (m *tuiModel) listChanged() {
	names := make([]string, 0, len(m.rows()))
	for _, el := range m.rows() {
		if el.Name != "" {
			names = append(names, el.Name)
		}
	}
	m.filter.SetSuggestions(names)
	m.forgetRows()
	m.rebuildRows()
}
