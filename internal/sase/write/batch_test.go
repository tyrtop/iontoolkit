package write

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
)

type fakePortal struct {
	elements map[string]api.Element
	bodies   map[string]string
	sent     []string
}

func (f *fakePortal) rows(ids ...string) []api.Element {
	var out []api.Element
	for _, id := range ids {
		el, ok := f.elements[id]
		if !ok {
			el = api.Element{ID: id, SiteID: "gone"}
		}
		out = append(out, el)
	}
	return out
}

func (f *fakePortal) GetJSON(_ context.Context, path string) ([]byte, error) {
	if b, ok := f.bodies[path]; ok {
		return []byte(b), nil
	}
	return nil, errors.New("404 " + path)
}

func (f *fakePortal) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	f.sent = append(f.sent, method+" "+path+" "+string(body))
	return []byte(`{}`), nil
}

func twoStores() *fakePortal {
	return &fakePortal{
		elements: map[string]api.Element{
			"e1": {ID: "e1", Name: "S0224-RTR-01", SiteID: "s1"},
			"e2": {ID: "e2", Name: "S0272-RTR-01", SiteID: "s2"},
		},
		bodies: map[string]string{
			"/sdwan/v4.21/api/sites/s1/elements/e1/interfaces": `{"items":[
				{"id":"i1","name":"1","description":"old","used_for":"public"},
				{"id":"i2","name":"2","description":"","used_for":"lan"}]}`,
			"/sdwan/v4.21/api/sites/s2/elements/e2/interfaces": `{"items":[
				{"id":"i3","name":"1","description":"uplink","used_for":"public"}]}`,
		},
	}
}

func builtinSpec(t *testing.T) Spec {
	t.Helper()
	set, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := set.Spec("interface")
	return spec
}

func TestAWriteRunShowsEveryChangeBeforeSendingAny(t *testing.T) {
	p := twoStores()
	all := PlanElements(context.Background(), p, builtinSpec(t), p.rows("e1", "e2", "e9"),
		[]string{"1"}, map[string]string{"description": "uplink"})

	if n := Changes(all); n != 1 {
		t.Errorf("%d changes, want only S0224's interface 1: %+v", n, all)
	}
	if all[0].Element != "S0224-RTR-01" || all[0].Plans[0].Changes[0].From != "old" {
		t.Errorf("S0224 = %+v", all[0])
	}
	if all[1].Element != "S0272-RTR-01" || !all[1].Plans[0].Empty() {
		t.Errorf("S0272 = %+v, want its interface 1 planned and already so", all[1])
	}
	if !strings.Contains(all[2].Err, "404") {
		t.Errorf("e9 = %+v, want its read's 404", all[2])
	}
	if len(p.sent) != 0 {
		t.Errorf("planning sent %v", p.sent)
	}
	if err := Unplanned(all); err == nil {
		t.Error("an element that could not be read was not reported")
	}
}

func TestApplySendsOnlyWhatChanged(t *testing.T) {
	p := twoStores()
	spec := builtinSpec(t)
	spec.Verified = true
	all := PlanElements(context.Background(), p, spec, p.rows("e1", "e2"),
		[]string{"*"}, map[string]string{"description": "uplink"})

	log := filepath.Join(t.TempDir(), LogName)
	var told []string
	sent, failed, err := Guard{Log: log}.ApplyAll(context.Background(), p, spec, all,
		func(el string, pl Plan, err error) { told = append(told, el+" "+pl.Label) })
	if err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 2 {
		t.Fatalf("sent %d, want S0224's two interfaces and not S0272's unchanged one:\n%s", len(p.sent), strings.Join(p.sent, "\n"))
	}
	if !strings.HasPrefix(p.sent[0], "PUT /sdwan/v4.21/api/sites/s1/elements/e1/interfaces/i1 ") ||
		!strings.Contains(p.sent[0], `"description":"uplink"`) || !strings.Contains(p.sent[0], `"used_for":"public"`) {
		t.Errorf("first send = %s", p.sent[0])
	}
	if sent != 2 || failed != 0 || len(told) != 2 {
		t.Errorf("sent %d, failed %d, told %v", sent, failed, told)
	}
}

func TestMatchByAnyField(t *testing.T) {
	p := twoStores()
	all := PlanElements(context.Background(), p, builtinSpec(t), p.rows("e1"),
		[]string{"used_for=lan"}, map[string]string{"description": "lan side"})
	if len(all[0].Plans) != 1 || all[0].Plans[0].Before["name"] != "2" {
		t.Errorf("plans = %+v, want interface 2 only", all[0].Plans)
	}
}

func TestPlansReadAsLines(t *testing.T) {
	all := []ElementPlans{
		{Element: "S0224-RTR-01", Plans: []Plan{{Label: "1", Changes: []Change{{Field: "description", From: "old", To: "uplink"}}}}},
		{Element: "S0272-RTR-01", Plans: []Plan{{Label: "1"}}},
		{Element: "e9", Err: "element e9: 404"},
	}
	got := strings.Join(PlanLines(all), "\n")
	for _, want := range []string{"S0224-RTR-01", "description    old → uplink", "1  (already so)", "! element e9: 404"} {
		if !strings.Contains(got, want) {
			t.Errorf("lines lack %q:\n%s", want, got)
		}
	}
}
