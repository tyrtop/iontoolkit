package runtime

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/sase/api"
)

const haFleet = `
tenants:
  - name: Acme Co
    tsg_id: "111"
    fleet: 12
`

func haRuntime(t *testing.T) *Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	os.WriteFile(path, []byte(haFleet), 0o600)
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := withConfig(t)
	rt.Config().Tenants = []config.Tenant{{Name: "Acme Co", TSGID: "111", ClientID: "a", Added: true}}
	rt.SetElements(nil)
	rt.UseConnector(Connector{
		Connect: func(_ context.Context, _ *config.Store, t config.Tenant) (*api.Client, error) {
			c := &http.Client{Transport: set.Transport(t.TSGID)}
			return api.NewClient(c, c, "fixture", false, 100, 100), nil
		},
		Explain: func(err error) string { return err.Error() },
	})
	if err := rt.Exec(&Call{}, "fleet.refresh"); err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestASweepReadsEveryCluster(t *testing.T) {
	rt := haRuntime(t)
	w := HAClusters(elementsOf(rt))
	if w.Clusters() == 0 {
		t.Fatal("the fake fleet has no clusters to read")
	}
	var said []string
	if err := rt.Exec(&Call{Out: func(s string) { said = append(said, s) }}, "ha.sweep"); err != nil {
		t.Fatal(err)
	}
	h := rt.HA()
	if h.Total != w.Clusters() || h.Done != h.Total || h.Failed != 0 || h.Bulk != "used" || rt.HAWorking() {
		t.Errorf("after the sweep: %+v (clusters %d)\n%s", h, w.Clusters(), strings.Join(said, "\n"))
	}
	known := 0
	for _, r := range rt.Elements() {
		if word, cached := rt.HAState(r); word == HAActive || word == HAStandby {
			known++
			if cached {
				t.Errorf("%s reads as cached after a live sweep", r.Name)
			}
		}
	}
	if known != len(w.Elements) {
		t.Errorf("%d of %d clustered elements have a word", known, len(w.Elements))
	}
}

func elementsOf(rt *Runtime) []api.Element {
	var out []api.Element
	for _, r := range rt.Elements() {
		out = append(out, r.Element)
	}
	return out
}

func TestApplySaysWhatComesNext(t *testing.T) {
	rt, _ := withConfig(t)
	rt.StartHA("1", HAWork{Pairs: [][2]string{{"s", "c1"}, {"s", "c2"}}})

	next := rt.ApplyHA(HAResult{TSG: "1", States: map[string]string{"1/e1": HAActive}, Answered: []string{"c1"},
		Bulk: "used", RestElements: []string{"e9"}})
	if !next.Tick || len(next.Save) != 1 || len(rt.Queued("1")) != 1 {
		t.Errorf("a partial bulk read: %+v, queued %v", next, rt.Queued("1"))
	}

	next = rt.ApplyHA(HAResult{TSG: "1", Bulk: "403 forbidden", Rest: [][2]string{{"s", "c2"}}})
	if len(next.Pairs) != 1 || !rt.HAInFlight("1") || rt.HA().Bulk != "403 forbidden" {
		t.Errorf("a refused bulk read: %+v %+v", next, rt.HA())
	}

	next = rt.ApplyHA(HAResult{TSG: "1", Failed: map[string]string{"c2": "timeout"}})
	h := rt.HA()
	if next.Tick || len(next.Pairs) != 0 || h.Done != 2 || h.Failed != 1 || h.Err != "timeout" || rt.HAWorking() {
		t.Errorf("the last read: %+v %+v", next, h)
	}
}

func TestWorkNearestTheCursorGoesFirst(t *testing.T) {
	rt := New()
	var rows []Element
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		rows = append(rows, Element{Element: api.Element{ID: id, Name: id,
			SpokeHA: &api.SpokeHA{ClusterID: "c" + id, Enable: true}, SiteID: "s"}, TSG: "1"})
	}
	rt.SetElements(rows)
	rt.SetCursor(6)
	got := rt.NearestElements("1", []string{"a", "j", "g", "e", "h"})
	if strings.Join(got, "") != "gheja" {
		t.Errorf("order = %v", got)
	}
	pairs := rt.NearestPairs("1", [][2]string{{"s", "ca"}, {"s", "cg"}, {"s", "cj"}})
	if pairs[0][1] != "cg" {
		t.Errorf("pairs = %v", pairs)
	}

	rt.SetViewport(4)
	wanted := rt.HAWanted()["1"]
	if len(wanted) != 8 || wanted[0] != "c" {
		t.Errorf("wanted around the cursor = %v", wanted)
	}
	rt.SetHA(map[string]string{"1/g": HAActive})
	for _, id := range rt.HAWanted()["1"] {
		if id == "g" {
			t.Error("a row already known was asked for again")
		}
	}
}

func TestASweepWithNothingLoadedLoadsTheFleetFirst(t *testing.T) {
	rt := haRuntime(t)
	fresh := New()
	fresh.UseConfig(rt.Store(), rt.Config())
	fresh.UseConnector(rt.conn)
	fresh.Store().DropCache("111")
	if err := fresh.Exec(&Call{}, "ha.sweep"); err != nil {
		t.Fatal(err)
	}
	if len(fresh.Elements()) == 0 || fresh.HA().Done == 0 {
		t.Errorf("rows %d, %+v", len(fresh.Elements()), fresh.HA())
	}

	empty := New()
	if err := empty.Exec(&Call{}, "ha.sweep"); err == nil {
		t.Error("a sweep with no tenant at all succeeded")
	}
}
