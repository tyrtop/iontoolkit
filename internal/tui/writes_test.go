package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/sase/write"
)

func runUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case T:
			return msg
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	var zero T
	t.Fatalf("no %T came out", zero)
	return zero
}

func typeKey(m tuiModel, k string) (tuiModel, tea.Cmd) {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	default:
		msg = tea.KeyPressMsg(tea.Key{Code: rune(k[0]), Text: k})
	}
	next, cmd := m.Update(msg)
	return next.(tuiModel), cmd
}

func editableDataTab(t *testing.T, verified bool) tuiModel {
	t.Helper()
	m, set := fixtureTUIWith(t, compareFixtureYAML)
	path := app.WritesPath(m.store())
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path); _ = os.Remove(filepath.Join(m.store().Root, write.LogName)) })
	if verified {
		body := strings.Replace(write.BuiltinYAML(), "    title: Interface\n", "    title: Interface\n    verified: true\n", 1)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.ReloadWrites(m.runtime())
	if problem := m.writesErr(); problem != "" {
		t.Fatal(problem)
	}
	m.phase = phasePicking
	m.setMode(modeData)
	m.rebuildRows()

	el := set.Tenants[0].Elements[0]
	next, cmd := m.openDataTabs([]sessionTarget{{eid: el.ID, name: el.Name, tsg: m.tenant().TSGID}})
	return runDataLoads(t, next.(tuiModel), cmd)
}

func reviewDescription(t *testing.T, m tuiModel, to string) tuiModel {
	t.Helper()
	m, cmd := typeKey(m, "w")
	if m.wr.step != writeLoading {
		t.Fatalf("w did not start an edit, step %d, status %q", m.wr.step, m.status)
	}
	next, _ := m.Update(runUntil[writeRecsMsg](t, cmd))
	m = next.(tuiModel)
	if m.wr.step != writePick || len(m.wr.recs) == 0 {
		t.Fatalf("no interfaces to pick: %q", m.wr.err)
	}
	if got := plain(m.viewWrite()); !strings.Contains(got, "> 1") {
		t.Errorf("the pick list does not start on interface 1:\n%s", got)
	}

	m, _ = typeKey(m, "enter")
	if m.wr.step != writeForm {
		t.Fatalf("enter did not open the form, step %d", m.wr.step)
	}
	m.wr.inputs[0].SetValue(to)
	m, cmd = typeKey(m, "enter")
	next, _ = m.Update(runUntil[writePlannedMsg](t, cmd))
	m = next.(tuiModel)
	if m.wr.step != writeReview {
		t.Fatalf("enter did not reach the review: %q", m.wr.err)
	}
	return m
}

func TestAnEditIsShownBeforeAnythingIsSent(t *testing.T) {
	m := editableDataTab(t, true)
	m = reviewDescription(t, m, "uplink to core")

	page := plain(m.viewWrite())
	for _, want := range []string{"Description", "→", "uplink to core", "PUT /sdwan/v4.21/api/sites/"} {
		if !strings.Contains(page, want) {
			t.Errorf("review lacks %q:\n%s", want, page)
		}
	}
	if _, err := os.Stat(filepath.Join(m.store().Root, write.LogName)); !os.IsNotExist(err) {
		t.Error("something was logged as sent before y")
	}
}

func TestAnUnverifiedWriteIsNotSent(t *testing.T) {
	m := editableDataTab(t, false)
	m = reviewDescription(t, m, "uplink to core")

	if got := plain(m.viewWrite()); !strings.Contains(got, "not verified") {
		t.Errorf("review does not say why it will not send:\n%s", got)
	}
	if _, cmd := typeKey(m, "y"); cmd != nil {
		t.Error("y sent an unverified write")
	}
}

func TestASentEditIsLoggedAndReadBack(t *testing.T) {
	m := editableDataTab(t, true)
	m = reviewDescription(t, m, "uplink to core")

	m, cmd := typeKey(m, "y")
	if m.wr.step != writeSending {
		t.Fatalf("y did not send: %q", m.wr.err)
	}
	sent := runUntil[writeSentMsg](t, cmd)
	if sent.err != "" {
		t.Fatalf("send failed: %s", sent.err)
	}
	next, _ := m.Update(sent)
	m = next.(tuiModel)
	if m.wr.step != writeOff || !strings.HasPrefix(m.status, "saved 1") {
		t.Errorf("after the send: step %d, status %q", m.wr.step, m.status)
	}

	log, err := os.ReadFile(filepath.Join(m.store().Root, write.LogName))
	if err != nil || !strings.Contains(string(log), `"ok":true`) || !strings.Contains(string(log), "uplink to core") {
		t.Errorf("writes.log = %s, %v", log, err)
	}

	tab, _ := m.activeData()
	writes, _ := m.runtime().Writes()
	spec, _ := writes.Spec("interface")
	recs, err := spec.Records(context.Background(), m.pool().Get(tab.TSG), tab.Vars())
	if err != nil {
		t.Fatal(err)
	}
	if recs[0]["description"] != "uplink to core" {
		t.Errorf("interface 1 reads back as %v", recs[0])
	}
}

func TestEscBacksOutOfAnEdit(t *testing.T) {
	m := editableDataTab(t, true)
	m = reviewDescription(t, m, "x")
	for _, want := range []writeStep{writeForm, writePick, writeOff} {
		m, _ = typeKey(m, "esc")
		if m.wr.step != want {
			t.Fatalf("esc went to step %d, want %d", m.wr.step, want)
		}
	}
	if m.phase != phaseData {
		t.Error("backing out of the edit left the data tab")
	}
}
