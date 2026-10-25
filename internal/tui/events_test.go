package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/runtime"
)

func settle(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	msg := runtimeMsg{rt: m.runtime()}
	ch := m.runtime().Changes()
	for {
		select {
		case ev := <-ch:
			msg.events = append(msg.events, ev)
			continue
		default:
		}
		break
	}
	if len(msg.events) == 0 {
		return m
	}
	next, _ := m.Update(msg)
	return next.(tuiModel)
}

func TestTheRuntimeSaysWhatWentWrongOnTheStatusLine(t *testing.T) {
	m, _ := fixtureTUI(t)
	next, _ := m.Update(runtimeMsg{rt: m.runtime(), events: []runtime.Event{
		{Area: runtime.AreaStatus, Status: "HA state unread for 1 cluster(s): 403 Forbidden"},
		{Area: runtime.AreaStatus, Status: "a later note"},
	}})
	m = next.(tuiModel)
	if m.status != "HA state unread for 1 cluster(s): 403 Forbidden" {
		t.Errorf("status = %q, want the first note", m.status)
	}
}

func TestAnotherRuntimesEventsAreIgnored(t *testing.T) {
	m, _ := fixtureTUI(t)
	next, _ := m.Update(runtimeMsg{rt: runtime.New(), events: []runtime.Event{{Area: runtime.AreaStatus, Status: "stale"}}})
	if got := next.(tuiModel).status; got != "" {
		t.Errorf("status = %q from a runtime the TUI left", got)
	}
}

func TestTheFeedForwardsEachRuntimeOnce(t *testing.T) {
	got := make(chan tea.Msg, 8)
	rt := runtime.New()
	startFeed(func(msg tea.Msg) { got <- msg }, rt)
	t.Cleanup(func() { feed = nil })
	watchRuntime(rt)

	rt.SetStatus("hello")
	select {
	case msg := <-got:
		if r, ok := msg.(runtimeMsg); !ok || r.rt != rt || r.events[0].Status != "hello" {
			t.Errorf("forwarded %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was forwarded")
	}
	select {
	case msg := <-got:
		t.Errorf("a second copy came through: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func tenantCountOf(m tuiModel) int {
	n, _ := m.runtime().Scope()
	return n
}

func tabCountOf(m tuiModel) int {
	n, _ := m.runtime().Tabs(m.front())
	return n
}

func selectTabOf(m tuiModel, i int) tuiModel { return m.showFront(m.runtime().SelectTab(i)) }
