package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
)

type legacySite struct {
	mu     sync.Mutex
	gets   map[string]string
	sent   []string
	bodies map[string]string
	fail   map[string]bool
	stale  map[string]int
}

func (p *legacySite) GetJSON(_ context.Context, path string) ([]byte, error) {
	if b, ok := p.gets[path]; ok {
		return []byte(b), nil
	}
	return nil, errors.New(path + ": status 404 Not Found")
}

func (p *legacySite) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, method+" "+path)
	p.bodies[method+" "+path] = string(body)
	if p.fail[method+" "+path] {
		return nil, errors.New("status 400 Bad Request: ELEMENT_OFFLINE")
	}
	if p.stale[method+" "+path] > 0 {
		p.stale[method+" "+path]--
		return nil, errors.New(`status 400 Bad Request: {"_error":[{"code":"INVALID_JSON_COMMON_ATTR_MISMATCH"}]}`)
	}
	return body, nil
}

func (p *legacySite) LookupElement(context.Context, string) (api.Element, error) {
	return api.Element{}, errors.New("unused")
}

func destroyFixture() *legacySite {
	s := "/sdwan/v2.0/api/sites/s9"
	return &legacySite{bodies: map[string]string{}, fail: map[string]bool{}, stale: map[string]int{}, gets: map[string]string{
		"/sdwan/v4.9/api/sites":       `{"items":[{"id":"s9","name":"S2647-LEGACY"},{"id":"s1","name":"S2647"}]}`,
		"/sdwan/v4.9/api/sites/s9":    `{"id":"s9","name":"S2647-LEGACY","admin_state":"active","multicast_peer_group_id":"m1","_etag":4}`,
		"/sdwan/v3.1/api/elements":    `{"items":[{"id":"e1","name":"S2647-RTR-01","site_id":"s9"},{"id":"e2","name":"S2647-RTR-02","site_id":"s9"},{"id":"e3","name":"OTHER","site_id":"s1"}]}`,
		"/sdwan/v3.1/api/elements/e1": `{"id":"e1","name":"S2647-RTR-01","site_id":"s9","spoke_ha_config":{"cluster_id":"c1"},"sw_obj":{"x":1},"tags":null,"_etag":7,"model_name":"ion 2000"}`,
		"/sdwan/v3.1/api/elements/e2": `{"id":"e2","name":"S2647-RTR-02","site_id":"s9","spoke_ha_config":null,"_etag":3}`,
		"/sdwan/v4.17/api/sites/s9/elements/e1/interfaces": `{"items":[
			{"id":"i1","name":"1","site_wan_interface_ids":["w1"]},
			{"id":"i2","name":"2","site_wan_interface_ids":null},
			{"id":"i3","name":"3","site_wan_interface_ids":["w1"]},
			{"id":"i4","name":"4"},
			{"id":"i34","name":"34","bypass_pair":{"wan":"i3","lan":"i4"}}]}`,
		"/sdwan/v4.17/api/sites/s9/elements/e2/interfaces":   `{"items":[]}`,
		"/sdwan/v2.0/api/sites/s9/elements/e1/securityzones": `{"items":[{"id":"z1","zone_id":"lan"}]}`,
		"/sdwan/v2.0/api/sites/s9/elements/e2/securityzones": `{"items":[]}`,
		s + "/ipfixlocalprefixes":                            `{"items":[{"id":"p1","prefix_id":"x"}]}`,
		s + "/spokeclusters":                                 `{"items":[{"id":"c1","name":"HA"}]}`,
		s + "/natlocalprefixes":                              `{"items":[]}`,
		s + "/sitesecurityzones":                             `{"items":[{"id":"sz1","zone_id":"lan"}]}`,
		s + "/extensions":                                    `{"items":[]}`,
		"/sdwan/v2.3/api/sites/s9/dhcpservers":               `{"items":[{"id":"d1","subnet":"10.0.0.0/24"}]}`,
		"/sdwan/v3.3/api/sites/s9/lannetworks":               `{"items":[{"id":"l1","name":"LAN"}]}`,
		"/sdwan/v2.7/api/sites/s9/waninterfaces":             `{"items":[{"id":"w1","name":"ISP"}]}`,
		"/sdwan/v2.3/api/serviceendpoints":                   `{"items":[{"id":"ep1","name":"S2647-LEGACY-EP","site_id":"s9"},{"id":"ep2","name":"keep","site_id":"s1"}]}`,
		"/sdwan/v2.1/api/servicebindingmaps":                 `{"items":[{"id":"sb1","name":"Default","service_bindings":[{"service_endpoint_ids":["ep1","ep2"]},{"service_endpoint_ids":["ep2"]}]}]}`,
	}}
}

func destroyRuntime(t *testing.T, p *legacySite) *runtime.Runtime {
	t.Helper()
	dir := os.Getenv("ION1200_DESTROY")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config/iontk/workflows/ion1200-destroy")
	}
	WorkflowPaths = []string{dir}
	t.Cleanup(func() { WorkflowPaths = nil })
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	cfg, _ := st.Config()
	rt := NewRuntime(st, cfg)
	if w := rt.Warnings(); len(w) > 0 {
		t.Fatalf("workflow refused: %v", w)
	}
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Tractor Supply"}, nil
	})
	return rt
}

func TestDestroyDryRunListsEveryWriteInDoSitesOrder(t *testing.T) {
	p := destroyFixture()
	rt := destroyRuntime(t, p)
	lines, _, err := exec(rt, "workflow.ion1200-destroy", map[string]string{"site": "S2647-LEGACY"}, nil, false)
	out := strings.Join(lines, "\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(p.sent) != 0 {
		t.Fatalf("a dry run sent %v", p.sent)
	}
	var plans []string
	for _, l := range lines {
		if strings.HasPrefix(l, "plan ") {
			plans = append(plans, strings.TrimPrefix(l, "plan "))
		}
	}
	want := []string{
		"PUT /sdwan/v4.17/api/sites/s9/elements/e1/interfaces/i1",
		"DELETE /sdwan/v2.0/api/sites/s9/elements/e1/securityzones/z1",
		"PUT /sdwan/v3.1/api/elements/e1",
		"PUT /sdwan/v3.1/api/elements/e1",
		"POST /sdwan/v2.0/api/elements/e1/operations",
		"PUT /sdwan/v3.1/api/elements/e2",
		"POST /sdwan/v2.0/api/elements/e2/operations",
		"DELETE /sdwan/v2.0/api/sites/s9/ipfixlocalprefixes/p1",
		"DELETE /sdwan/v2.0/api/sites/s9/spokeclusters/c1",
		"DELETE /sdwan/v2.0/api/sites/s9/sitesecurityzones/sz1",
		"DELETE /sdwan/v2.3/api/sites/s9/dhcpservers/d1",
		"DELETE /sdwan/v3.3/api/sites/s9/lannetworks/l1",
		"DELETE /sdwan/v2.7/api/sites/s9/waninterfaces/w1",
		"PUT /sdwan/v2.1/api/servicebindingmaps/sb1",
		"DELETE /sdwan/v2.3/api/serviceendpoints/ep1",
		"PUT /sdwan/v4.9/api/sites/s9",
		"DELETE /sdwan/v4.9/api/sites/s9",
	}
	if strings.Join(plans, "\n") != strings.Join(want, "\n") {
		t.Errorf("planned:\n%s\nwant:\n%s\n\n%s", strings.Join(plans, "\n"), strings.Join(want, "\n"), out)
	}
	if !strings.Contains(out, "archived to ") {
		t.Errorf("nothing was archived:\n%s", out)
	}
}

func TestDestroyKeepsEveryBindingAndDeclaimsARouterThatWillNotUnassign(t *testing.T) {
	p := destroyFixture()
	p.fail["PUT /sdwan/v3.1/api/elements/e2"] = true
	rt := destroyRuntime(t, p)
	lines, _, err := exec(rt, "workflow.ion1200-destroy", map[string]string{"site": "S2647-LEGACY"}, nil, true)
	out := strings.Join(lines, "\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(strings.Join(p.sent, "\n"), "POST /sdwan/v2.0/api/elements/e2/operations") {
		t.Errorf("the router that would not unassign was not declaimed:\n%s", strings.Join(p.sent, "\n"))
	}
	var sb map[string]any
	if err := json.Unmarshal([]byte(p.bodies["PUT /sdwan/v2.1/api/servicebindingmaps/sb1"]), &sb); err != nil {
		t.Fatal(err)
	}
	if b, _ := sb["service_bindings"].([]any); len(b) != 2 {
		t.Errorf("binding map sent with %d bindings, want both kept: %v", len(b), sb)
	}
	var unassign map[string]any
	_ = json.Unmarshal([]byte(p.bodies["PUT /sdwan/v3.1/api/elements/e1"]), &unassign)
	if unassign["site_id"] != float64(1) || unassign["sw_obj"] != nil || unassign["spoke_ha_config"] != nil {
		t.Errorf("unassign body = %v", unassign)
	}
	if _, has := unassign["model_name"]; has {
		t.Errorf("unassign sent a field outside element_put_items: %v", unassign)
	}
	if _, has := unassign["tags"]; !has {
		t.Errorf("a null the portal sent was dropped from the body: %v", unassign)
	}
	if !strings.Contains(out, "did not unassign") {
		t.Errorf("the failed unassign was not reported:\n%s", out)
	}
}

func TestDestroyRefusesASiteThatIsNotLegacy(t *testing.T) {
	rt := destroyRuntime(t, destroyFixture())
	if _, _, err := exec(rt, "workflow.ion1200-destroy", map[string]string{"site": "S2647"}, nil, false); err == nil ||
		!strings.Contains(err.Error(), "-LEGACY") {
		t.Errorf("a site without -LEGACY = %v, want it refused before anything is read", err)
	}
}

func TestDestroyReadsARouterAgainWhenTheControllerMovedItOn(t *testing.T) {
	p := destroyFixture()
	p.stale["PUT /sdwan/v3.1/api/elements/e2"] = 1
	rt := destroyRuntime(t, p)
	lines, _, err := exec(rt, "workflow.ion1200-destroy", map[string]string{"site": "S2647-LEGACY"}, nil, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(lines, "\n"))
	}
	n := 0
	for _, s := range p.sent {
		if s == "PUT /sdwan/v3.1/api/elements/e2" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("e2 was put %d times, want a second try after the stale etag", n)
	}
	out := strings.Join(lines, "\n")
	if strings.Contains(out, "did not unassign") {
		t.Errorf("a stale etag was taken for a failed unassign:\n%s", out)
	}
	i := strings.Index(out, "archived to ")
	path := strings.TrimSpace(strings.SplitN(out[i+len("archived to "):], "\n", 2)[0])
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(path, ".json") || !strings.Contains(string(b), "S2647-LEGACY-EP") {
		t.Errorf("archive %s (%v) does not hold the service endpoints:\n%.300s", path, err, b)
	}
}
