package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
)

func dataRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := deviceRuntime(t)
	set, err := dataview.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	rt.SetViews(set, "")
	return rt
}

func say(t *testing.T, rt *Runtime, name string, args map[string]string) ([]string, error) {
	t.Helper()
	var said []string
	err := rt.Exec(&Call{Ctx: context.Background(), Args: args, Out: func(s string) { said = append(said, s) }}, name)
	return said, err
}

func TestDataOpenReadsEveryViewOfEachElementAndPrintsThem(t *testing.T) {
	rt := dataRuntime(t)
	run(t, rt, "list.select-matches")
	said, err := say(t, rt, "data.open", nil)
	if err != nil {
		t.Fatal(err)
	}
	tabs := rt.DataTabs()
	if len(tabs) != 2 || rt.DataActive() != 1 {
		t.Fatalf("%d tabs, %d in front; want both elements, the last in front", len(tabs), rt.DataActive())
	}
	for _, tab := range tabs {
		if tab.Loading() || len(tab.Panes) != len(rt.ViewNames()) || tab.At.IsZero() {
			t.Errorf("%s = %+v, want every view read", tab.Name, tab.Panes)
		}
	}
	text := strings.Join(said, "\n")
	for _, want := range []string{"acme-rtr-01", "acme-rtr-02", "── Overview", "Name"} {
		if !strings.Contains(text, want) {
			t.Errorf("printed page has no %q:\n%s", want, text)
		}
	}
}

func TestReopeningATabKeepsWhereItWasScrolled(t *testing.T) {
	rt := dataRuntime(t)
	id := rt.Elements()[0].ID
	if _, err := say(t, rt, "data.open", map[string]string{"elements": id}); err != nil {
		t.Fatal(err)
	}
	rt.ScrollData(12)
	if _, err := say(t, rt, "data.open", map[string]string{"elements": id}); err != nil {
		t.Fatal(err)
	}
	if tabs := rt.DataTabs(); len(tabs) != 1 || tabs[0].Scroll != 12 {
		t.Errorf("tabs = %d, scroll %d; want the one tab where it was", len(tabs), tabs[0].Scroll)
	}
	tab, _ := rt.RefreshData()
	if !tab.Loading() || tab.Scroll != 12 {
		t.Errorf("refresh = loading %v scroll %d", tab.Loading(), tab.Scroll)
	}
}

func TestDataTabsStepAndClose(t *testing.T) {
	rt := dataRuntime(t)
	run(t, rt, "list.select-matches")
	if _, err := say(t, rt, "data.open", nil); err != nil {
		t.Fatal(err)
	}
	run(t, rt, "data.next")
	if rt.DataActive() != 0 {
		t.Errorf("next from the last = %d, want it to wrap to 0", rt.DataActive())
	}
	run(t, rt, "data.prev")
	run(t, rt, "data.close")
	if tabs := rt.DataTabs(); len(tabs) != 1 || rt.DataActive() != 0 {
		t.Errorf("after close: %d tabs, %d in front", len(tabs), rt.DataActive())
	}
}

func TestTheCLICheckOpensItsTabAndAsksTheDevice(t *testing.T) {
	rt := dataRuntime(t)
	said, err := say(t, rt, "data.check", map[string]string{"elements": rt.Elements()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	tab, ok := rt.ActiveData()
	if !ok || !tab.Check.Ran() || tab.Check.Loading || tab.Check.Err != "" || len(tab.Check.ByView) == 0 {
		t.Fatalf("check = %+v", tab.Check)
	}
	if text := strings.Join(said, "\n"); !strings.Contains(text, "dump device status") || !strings.Contains(text, "CLI checks") {
		t.Errorf("printed check:\n%s", text)
	}
}

func TestACheckWithNoLoginSaysSo(t *testing.T) {
	rt := dataRuntime(t)
	rt.UseDevice(Device{Login: func(_ *config.Store, _ config.Tenant, _ string) (string, string) { return "", "" }})
	if _, err := say(t, rt, "data.open", map[string]string{"elements": rt.Elements()[0].ID}); err != nil {
		t.Fatal(err)
	}
	_, err := rt.BeginCheck()
	if !errors.Is(err, ErrNoLogin) {
		t.Errorf("err = %v, want no login", err)
	}
}
