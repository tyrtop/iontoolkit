package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	return haRuntimeWith(t, haFleet, nil)
}

func haRuntimeWith(t *testing.T, fleet string, wrap func(http.RoundTripper) http.RoundTripper) *Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	os.WriteFile(path, []byte(fleet), 0o600)
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := withConfig(t)
	rt.Config().Tenants = []config.Tenant{{Name: "Acme Co", TSGID: "111", ClientID: "a", Added: true}}
	rt.SetElements(nil)
	rt.UseConnector(Connector{
		Connect: func(_ context.Context, _ *config.Store, t config.Tenant) (*api.Client, error) {
			tr := set.Transport(t.TSGID)
			if wrap != nil {
				tr = wrap(tr)
			}
			c := &http.Client{Transport: tr}
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
	if len(next.Pairs) != 1 || !rt.HAWorking() || rt.HA().Bulk != "403 forbidden" {
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

type laterQueriesRefused struct {
	next    http.RoundTripper
	mu      sync.Mutex
	queries int
}

func cannedAnswer(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

// the portal answers only the elements a query names, one page
func (r *laterQueriesRefused) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/spokeclusters/status/query") {
		return r.next.RoundTrip(req)
	}
	body, _ := io.ReadAll(req.Body)
	var q struct {
		Params struct {
			Element struct {
				In []string `json:"in"`
			} `json:"element_id"`
		} `json:"query_params"`
		Page int `json:"dest_page"`
	}
	json.Unmarshal(body, &q)
	if q.Page > 1 {
		return cannedAnswer(req, http.StatusOK, `{"items":[]}`), nil
	}
	r.mu.Lock()
	r.queries++
	n := r.queries
	r.mu.Unlock()
	if n > 1 {
		return cannedAnswer(req, http.StatusForbidden, `{}`), nil
	}
	asked := map[string]bool{}
	for _, id := range q.Params.Element.In {
		asked[id] = true
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&env)
	resp.Body.Close()
	var kept []map[string]any
	for _, row := range env.Items {
		if id, _ := row["element_id"].(string); asked[id] {
			kept = append(kept, row)
		}
	}
	out, _ := json.Marshal(map[string]any{"items": kept})
	resp.Body, resp.ContentLength = io.NopCloser(bytes.NewReader(out)), int64(len(out))
	resp.Header.Del("Content-Length")
	return resp, nil
}

func TestALaterHAChunkThatIsRefusedFallsBackToTheClustersLeft(t *testing.T) {
	refused := &laterQueriesRefused{}
	rt := haRuntimeWith(t, pairedFleet(api.ClusterQueryLimit/2+10), func(next http.RoundTripper) http.RoundTripper {
		refused.next = next
		return refused
	})
	w := HAClusters(elementsOf(rt))
	if len(w.Elements) <= api.ClusterQueryLimit {
		t.Fatalf("%d clustered elements fit one query, so this proves nothing", len(w.Elements))
	}
	rt.SweepHA(t.Context(), "111", w, 0)
	if refused.queries < 2 {
		t.Fatalf("%d chunks asked, want the second refused", refused.queries)
	}
	if h := rt.HA(); h.Done < h.Total || h.Failed != 0 || rt.HAWorking() {
		t.Errorf("after the sweep: %+v", h)
	}
	for _, r := range rt.Elements() {
		if word, _ := rt.HAState(r); r.ClusterID() != "" && word != HAActive && word != HAStandby {
			t.Errorf("%s reads %q, want it read one cluster at a time", r.Name, word)
		}
	}
}

func TestStoppingTheSweepsEndsThemAndClosingEndsTheFeed(t *testing.T) {
	rt := New()
	ctx, done, _ := rt.sweeping(t.Context())
	go func() {
		<-ctx.Done()
		done()
	}()
	stopped := make(chan struct{})
	go func() {
		rt.StopSweeps()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopSweeps did not stop the sweep running and return")
	}
	if ctx, done, _ := rt.sweeping(t.Context()); ctx.Err() != nil {
		t.Error("a sweep after the reset started stopped")
	} else {
		done()
	}

	rt.Close()
	for {
		select {
		case _, ok := <-rt.Changes():
			if !ok {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("the change feed of a closed runtime never ended")
		}
	}
}

func TestARefreshThatFollowsWaitsForItsReads(t *testing.T) {
	rt := haRuntime(t)
	var said []string
	if err := rt.Exec(&Call{Ctx: t.Context(), Args: map[string]string{"follow": "yes"},
		Out: func(s string) { said = append(said, s) }}, "fleet.refresh"); err != nil {
		t.Fatal(err)
	}
	if h := rt.HA(); h.Total == 0 || h.Done != h.Total || rt.HAWorking() {
		t.Errorf("fleet.refresh follow=yes returned with HA at %+v\n%s", h, strings.Join(said, "\n"))
	}
}

func pairedFleet(sites int) string {
	var fleet strings.Builder
	fleet.WriteString("tenants:\n  - name: Acme Co\n    tsg_id: \"111\"\n    elements:\n")
	for i := 1; i <= sites; i++ {
		for _, r := range []string{"01", "02"} {
			fmt.Fprintf(&fleet, "      - name: store-%04d-rtr-%s\n        site: Store %04d\n        connected: true\n        ha: true\n", i, r, i)
		}
	}
	return fleet.String()
}

func TestAStoppedHASweepLeavesNothingQueued(t *testing.T) {
	rt := haRuntimeWith(t, pairedFleet(api.ClusterQueryLimit/2+10), nil)
	w := HAClusters(elementsOf(rt))
	ctx, done, _ := rt.sweeping(t.Context())
	go func() {
		defer done()
		rt.SweepHA(ctx, "111", w, time.Hour)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(rt.Queued("111")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first chunk never queued the rest")
		}
		time.Sleep(5 * time.Millisecond)
	}
	rt.StopSweeps()
	if rt.HAWorking() {
		t.Errorf("after the stop: %+v, want nothing queued or in flight", rt.HA())
	}
}

type hangingPortal struct{ asked chan struct{} }

func (h hangingPortal) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case h.asked <- struct{}{}:
	default:
	}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestARefreshStoppedPartWayAddsNoRows(t *testing.T) {
	hang := hangingPortal{asked: make(chan struct{}, 1)}
	rt := haRuntimeWith(t, haFleet, nil)
	rt.ResetClients()
	rt.SetElements(nil)
	rt.UseConnector(Connector{
		Connect: func(_ context.Context, _ *config.Store, _ config.Tenant) (*api.Client, error) {
			c := &http.Client{Transport: hang}
			return api.NewClient(c, c, "fixture", false, 100, 100), nil
		},
		Explain: func(err error) string { return err.Error() },
	})
	finished := make(chan struct{})
	go func() {
		rt.RefreshTenants(&Call{Ctx: t.Context()}, rt.FleetTenants(), true)
		close(finished)
	}()
	select {
	case <-hang.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never asked the portal")
	}
	rt.StopSweeps()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not end the refresh")
	}
	if n := len(rt.Elements()); n != 0 || rt.Fleet().Issues["111"] != "" {
		t.Errorf("after the stop: %d rows, issue %q, want the refresh dropped", n, rt.Fleet().Issues["111"])
	}
}

type countedQueries struct {
	next    http.RoundTripper
	mu      sync.Mutex
	queries int
}

func (r *countedQueries) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/spokeclusters/status/query") {
		r.mu.Lock()
		r.queries++
		r.mu.Unlock()
	}
	return r.next.RoundTrip(req)
}

func (r *countedQueries) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries
}

func haList(t *testing.T, rt *Runtime, args map[string]string) (map[string]any, string) {
	t.Helper()
	var said []string
	c := &Call{Args: args, Out: func(s string) { said = append(said, s) }}
	if err := rt.Exec(c, "ha.list"); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	states, _ := c.Result.(map[string]any)
	return states, strings.Join(said, "\n")
}

func TestHAListNamesOneActiveAndOneStandbyPerPair(t *testing.T) {
	q := &countedQueries{}
	rt := haRuntimeWith(t, pairedFleet(3), func(next http.RoundTripper) http.RoundTripper { q.next = next; return q })
	fresh := New()
	fresh.UseConfig(rt.Store(), rt.Config())
	fresh.UseConnector(rt.conn)
	fresh.SetListCalls(rt.listCalls)

	states, said := haList(t, fresh, map[string]string{"tenant": "acme co"})
	if len(fresh.Elements()) != 6 || len(states) != 6 || q.asked() == 0 {
		t.Fatalf("rows %d, states %v, queries %d\n%s", len(fresh.Elements()), states, q.asked(), said)
	}
	bySite := map[string][]string{}
	for _, el := range fresh.Elements() {
		w, _ := states[el.ID].(string)
		bySite[el.SiteID] = append(bySite[el.SiteID], w)
	}
	for site, ws := range bySite {
		if len(ws) != 2 || !(ws[0] == HAActive && ws[1] == HAStandby || ws[0] == HAStandby && ws[1] == HAActive) {
			t.Errorf("site %s = %v, want one active and one standby", site, ws)
		}
	}
	if !strings.Contains(said, "Acme Co: 6 clustered elements, 3 active, 3 standby, 0 unknown (6 asked of the portal)") {
		t.Errorf("said:\n%s", said)
	}

	n := q.asked()
	if again, said := haList(t, fresh, map[string]string{"tenant": "111"}); len(again) != 6 || q.asked() != n ||
		!strings.Contains(said, "(read already)") {
		t.Errorf("a second ask went to the portal: %d -> %d\n%s", n, q.asked(), said)
	}
	if _, said := haList(t, fresh, map[string]string{"tenant": "111", "refresh": "yes"}); q.asked() == n {
		t.Errorf("refresh=yes did not ask the portal\n%s", said)
	}

	saved := New()
	saved.UseConfig(rt.Store(), rt.Config())
	saved.UseConnector(rt.conn)
	saved.SetListCalls(rt.listCalls)
	if rows, _, _ := saved.LoadCache(); rows != 6 {
		t.Fatalf("cache rows %d", rows)
	}
	n = q.asked()
	if _, said := haList(t, saved, map[string]string{"tenant": "111"}); q.asked() == n {
		t.Errorf("a saved HA word was trusted without asking the portal\n%s", said)
	}
}

func TestHAListRefusesATenantItCannotName(t *testing.T) {
	rt := haRuntime(t)
	if err := rt.Exec(&Call{Args: map[string]string{"tenant": "Nobody"}}, "ha.list"); err == nil ||
		!strings.Contains(err.Error(), `no tenant is called "Nobody"`) {
		t.Errorf("an unknown tenant = %v", err)
	}
	if err := New().Exec(&Call{}, "ha.list"); err == nil || !strings.Contains(err.Error(), "no tenant") {
		t.Errorf("no tenant at all = %v", err)
	}
}
