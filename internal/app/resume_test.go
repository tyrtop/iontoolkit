package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/workflow"
)

const thingsFlow = `
title: Things
endpoints:
  - name: thing_post
    method: POST
    path: /sdwan/v2.0/api/things
  - name: thing_put
    method: PUT
    path: /sdwan/v2.0/api/things/{id}
`

const thingsMain = `
return function(ctx)
  local function post(name, body)
    local _, made = ctx.run("endpoint.thing_post", { body = body })
    if type(made) == "table" and made.id ~= nil then return made.id end
    return "dry-run-" .. name
  end
  local a = post("a", { name = "a", size = 1 })
  ctx.run("endpoint.thing_put", { id = a, body = { name = "a", size = 2 } })
  local b = post("b", { name = "b", parent = a })
  ctx.run("endpoint.thing_put", { id = b, body = { name = "b", parent = a, size = 3 } })
  ctx.run("endpoint.thing_put", { id = a, body = { name = "a", size = 4, child = b } })
  post("c", { name = "c", parent = b })
end
`

type thingsPortal struct {
	mu     sync.Mutex
	things []map[string]any
	sent   []string
	failAt int
	tries  int
}

func (p *thingsPortal) GetJSON(_ context.Context, path string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if path == "/sdwan/v2.0/api/things" {
		return json.Marshal(map[string]any{"items": p.things})
	}
	for _, t := range p.things {
		if path == "/sdwan/v2.0/api/things/"+fmt.Sprint(t["id"]) {
			return json.Marshal(t)
		}
	}
	return nil, errors.New("status 404 Not Found: " + path)
}

func (p *thingsPortal) Send(_ context.Context, method, path string, body []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tries++
	if p.failAt > 0 && p.tries >= p.failAt {
		return nil, errors.New("the connection dropped")
	}
	p.sent = append(p.sent, method+" "+path+" "+string(body))
	var b map[string]any
	_ = json.Unmarshal(body, &b)
	if method == "POST" {
		b["id"] = fmt.Sprintf("t%d", len(p.things)+1)
		p.things = append(p.things, b)
		return json.Marshal(b)
	}
	for _, t := range p.things {
		if path == "/sdwan/v2.0/api/things/"+fmt.Sprint(t["id"]) {
			for k, v := range b {
				t[k] = v
			}
		}
	}
	return []byte(`{}`), nil
}

func runThings(t *testing.T, p *thingsPortal, resume bool) (string, error) {
	t.Helper()
	rt, _, _ := registryRuntime(t, map[string]map[string]string{
		"things": {workflow.ManifestName: thingsFlow, "main.lua": thingsMain},
	})
	if w := rt.Warnings(); len(w) > 0 {
		t.Fatalf("the workflow did not load: %v", w)
	}
	rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
		return p, runtime.Tenant{TSG: "t1", Name: "Trident Supply"}, nil
	})
	var lines []string
	c := &runtime.Call{Ctx: context.Background(), DoIt: true, Resume: resume, Out: func(s string) { lines = append(lines, s) }}
	err := rt.Exec(c, "workflow.things")
	return strings.Join(lines, "\n"), err
}

func TestAResumedRunSendsOnlyTheWritesThePortalDoesNotHave(t *testing.T) {
	whole := &thingsPortal{}
	if out, err := runThings(t, whole, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := strings.Join(whole.sent, "\n")

	for k := 1; k <= len(whole.sent); k++ {
		p := &thingsPortal{failAt: k}
		if _, err := runThings(t, p, false); err == nil {
			t.Fatalf("a run whose write %d failed went through", k)
		}
		p.failAt = 0
		out, err := runThings(t, p, true)
		if err != nil {
			t.Fatalf("after write %d failed, the resumed run: %v\n%s", k, err, out)
		}
		if got := strings.Join(p.sent, "\n"); got != want {
			t.Errorf("after write %d failed, the two runs sent:\n%s\nwant what one run sends:\n%s\n%s", k, got, want, out)
		}
	}

	before := len(whole.sent)
	out, err := runThings(t, whole, true)
	if err != nil || len(whole.sent) != before || !strings.Contains(out, "resume: all 6 writes are on the portal") {
		t.Errorf("a resumed run of a finished one sent %d writes: %v\n%s", len(whole.sent)-before, err, out)
	}
}

func TestAWriteIsOnThePortalWhenThePortalHasWhatItSets(t *testing.T) {
	for _, tt := range []struct {
		name, have, want string
		lenient, ok      bool
	}{
		{"the same fields", `{"a":1,"b":"x","more":true}`, `{"a":1,"b":"x"}`, false, true},
		{"a field differs", `{"a":2}`, `{"a":1}`, false, false},
		{"the portal lacks a field", `{}`, `{"a":1}`, false, false},
		{"lenient lets the portal leave a field out", `{}`, `{"a":1}`, true, true},
		{"null in the write changes nothing", `{"a":5}`, `{"a":null}`, false, true},
		{"a made-up id stands for any", `{"parent":"t9"}`, `{"parent":"dry-run-4"}`, false, true},
		{"lists in another order", `{"l":[1,2,3]}`, `{"l":[3,1,2]}`, false, true},
		{"lists of another length", `{"l":[1,2]}`, `{"l":[1,2,3]}`, false, false},
		{"a made-up id in a list goes last", `{"l":["t1","t2"]}`, `{"l":["dry-run-1","t1"]}`, false, true},
		{"an empty object for nothing", `{"o":null}`, `{"o":{}}`, false, true},
		{"ids and underscored fields are the portal's", `{"id":"x","_etag":3}`, `{"id":"y","_etag":1}`, false, true},
	} {
		var have, want any
		_ = json.Unmarshal([]byte(tt.have), &have)
		_ = json.Unmarshal([]byte(tt.want), &want)
		if ok, _ := covers(have, want, tt.lenient); ok != tt.ok {
			t.Errorf("%s: covers(%s, %s) = %v, want %v", tt.name, tt.have, tt.want, ok, tt.ok)
		}
	}
}

const thingsEndpoints = `
endpoints:
  - name: things
    path: /sdwan/v2.0/api/things
  - name: thing_post
    method: POST
    path: /sdwan/v2.0/api/things
  - name: thing_put
    method: PUT
    path: /sdwan/v2.0/api/things/{id}
`

const thingsFirst = `
return function(ctx)
  local kept = ctx.kept("things")
  if not kept then
    kept = { size = 2 }
    ctx.keep("things", kept)
  end
  local _, made = ctx.run("endpoint.thing_post", { body = { name = "a", size = 1 } })
  local id = type(made) == "table" and made.id or "dry-run-a"
  ctx.run("endpoint.thing_put", { id = id, body = { name = "a", size = kept.size } })
end
`

const thingsSecond = `
return function(ctx)
  local _, all = ctx.run("endpoint.things", {})
  local a
  for _, t in ipairs(all.items) do
    if t.name == "a" then a = t.id end
  end
  if not a then
    if ctx.doit then ctx.fail("there is no a to hang b on") end
    a = "dry-run-a"
  end
  ctx.run("endpoint.thing_post", { body = { name = "b", parent = a } })
  ctx.run("endpoint.thing_post", { body = { name = "c", parent = a } })
end
`

func TestAWorkflowOfStepsRunsThemInOrderAndResumesAtTheRightOne(t *testing.T) {
	flows := map[string]map[string]string{
		"things-all":    {workflow.ManifestName: "title: Things, in steps\nsteps:\n  - things-first\n  - things-second\n"},
		"things-first":  {workflow.ManifestName: "title: First" + thingsEndpoints, "main.lua": thingsFirst},
		"things-second": {workflow.ManifestName: "title: Second" + thingsEndpoints, "main.lua": thingsSecond},
	}
	run := func(rt *runtime.Runtime, p *thingsPortal, resume bool) (string, error) {
		rt.UsePortal(func(context.Context) (runtime.Portal, runtime.Tenant, error) {
			return p, runtime.Tenant{TSG: "t1", Name: "Trident Supply"}, nil
		})
		var lines []string
		c := &runtime.Call{Ctx: context.Background(), DoIt: true, Resume: resume, Out: func(s string) { lines = append(lines, s) }}
		err := rt.Exec(c, "workflow.things-all")
		return strings.Join(lines, "\n"), err
	}
	fresh := func() *runtime.Runtime {
		rt, _, _ := registryRuntime(t, flows)
		if w := rt.Warnings(); len(w) > 0 {
			t.Fatalf("the workflows did not load: %v", w)
		}
		return rt
	}

	whole := &thingsPortal{}
	rt := fresh()
	out, err := run(rt, whole, false)
	if err != nil || !strings.Contains(out, "step 1 of 2: things-first") || !strings.Contains(out, "step 2 of 2: things-second") {
		t.Fatalf("%v\n%s", err, out)
	}
	want := strings.Join(whole.sent, "\n")
	if len(whole.sent) != 4 {
		t.Fatalf("one run sent %d writes, want 4:\n%s", len(whole.sent), want)
	}

	before := len(whole.sent)
	out, err = run(rt, whole, true)
	if err != nil || len(whole.sent) != before || !strings.Contains(out, "resume: all 2 steps are done") {
		t.Errorf("a resumed run of a finished one sent %d writes: %v\n%s", len(whole.sent)-before, err, out)
	}

	for k := 1; k <= 4; k++ {
		p := &thingsPortal{failAt: k}
		rt := fresh()
		if _, err := run(rt, p, false); err == nil {
			t.Fatalf("a run whose write %d failed went through", k)
		}
		p.failAt = 0
		out, err := run(rt, p, true)
		if err != nil {
			t.Fatalf("after write %d failed, the resumed run: %v\n%s", k, err, out)
		}
		if got := strings.Join(p.sent, "\n"); got != want {
			t.Errorf("after write %d failed, the two runs sent:\n%s\nwant what one run sends:\n%s\n%s", k, got, want, out)
		}
	}
}

func TestSomethingKeptIsNotWrittenOver(t *testing.T) {
	rt, _, _ := registryRuntime(t, nil)
	keep, kept := keeper(rt, config.Tenant{TSGID: "t1"})
	if text, _, err := kept("site"); err != nil || text != "" {
		t.Fatalf("before anything is kept: %q, %v", text, err)
	}
	where, err := keep("site", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keep("site", `{"a":2}`); err == nil {
		t.Error("a second keep under the same name went through")
	}
	if text, at, err := kept("site"); err != nil || text != `{"a":1}` || at != where {
		t.Errorf("kept = %q at %s, %v; want the first one, at %s", text, at, err, where)
	}
	if _, err := keep("../site", "{}"); err == nil {
		t.Error("a name that climbs out of the folder was kept")
	}
}
