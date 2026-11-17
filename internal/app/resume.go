package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/workflow"
)

const (
	placeholder = "dry-run"
	standIn     = placeholder + "-write-"
)

type planned struct {
	method, path string
	body         any
	to           config.Tenant
}

type pickUp struct {
	rt      *runtime.Runtime
	ctx     context.Context
	name    string
	plan    []planned
	done    int
	next    int
	checks  int
	waiting bool
	kept    bool
	failed  error
	last    *bool
	lists   map[string][]any
}

func walk(rt *runtime.Runtime, c *runtime.Call, w workflow.Workflow, p placed, run config.Tenant) *pickUp {
	pk := &pickUp{rt: rt, ctx: c.Ctx, name: WorkflowPrefix + w.Name, lists: map[string][]any{}}
	quiet := &runtime.Call{Ctx: c.Ctx, Out: func(string) {}}
	host := endpointHost(rt, quiet, w, p, run, func(call *runtime.Call, e endpoint.Endpoint, to config.Tenant, where string) error {
		if path, err := e.Fill(call.Args); err == nil && (e.Method == "" || e.Method == http.MethodGet) && strings.Contains(path, standIn) {
			at, ok := pk.real(path)
			if !ok {
				return fmt.Errorf("%s: status 404: what it fetches is not on the portal yet", path)
			}
			v, err := pk.get(to, at)
			call.Result = EndpointCall{Answer: v}
			return err
		}
		err := callEndpoint(rt, call, pk.name, e, to, where)
		r, ok := call.Result.(EndpointCall)
		if !ok {
			return err
		}
		for _, wr := range r.Writes {
			pk.plan = append(pk.plan, planned{method: wr.Method, path: wr.Path, body: wr.Body, to: to})
			if wr.Method == http.MethodPost {
				made := map[string]any{}
				if b, ok := wr.Body.(map[string]any); ok {
					for k, v := range b {
						made[k] = v
					}
				}
				made["id"] = fmt.Sprintf("%s%d", standIn, len(pk.plan)-1)
				r.Answer = made
			}
		}
		call.Result = r
		return err
	})
	host.Export = func(string, string, string) (string, error) { return "", nil }
	_, host.Kept = keeper(rt, run)
	host.Keep = func(string, string) (string, error) {
		pk.kept = true
		return "", nil
	}
	host.ExitOnWait, host.Resume = true, true
	res, err := w.Run(c.Ctx, p.input, host, false)
	pk.waiting = res.Waiting != ""
	if err != nil && !pk.waiting {
		pk.failed = err
	}
	return pk
}

func (pk *pickUp) finished() bool {
	if pk.failed != nil || pk.waiting || pk.kept {
		return false
	}
	if len(pk.plan) == 0 {
		return true
	}
	if pk.last == nil {
		pk.checks++
		on := pk.onPortal(len(pk.plan) - 1)
		pk.last = &on
	}
	return *pk.last
}

func (pk *pickUp) locate() {
	lo, hi := 0, len(pk.plan)
	if pk.last != nil && *pk.last {
		lo = hi
	} else if pk.last != nil {
		hi--
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		pk.checks++
		if pk.onPortal(mid - 1) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	pk.done = lo
}

func (pk *pickUp) report(c *runtime.Call) {
	n := len(pk.plan)
	switch {
	case n == 0:
		c.Say("resume: no writes to look for")
	case pk.done == 0:
		c.Say("resume: none of the %d writes are on the portal yet (%d checks)", n, pk.checks)
	case pk.done == n:
		c.Say("resume: all %d writes are on the portal (%d checks)", n, pk.checks)
	default:
		at := pk.plan[pk.done]
		c.Say("resume: writes 1 to %d of %d are on the portal (%d checks); picking up at %s %s", pk.done, n, pk.checks, at.method, at.path)
	}
}

func findStep(rt *runtime.Runtime, c *runtime.Call, steps []workflow.Workflow, p placed, run config.Tenant) (int, *pickUp, error) {
	looked := map[int]*pickUp{}
	look := func(i int) *pickUp {
		if pk, ok := looked[i]; ok {
			return pk
		}
		looked[i] = walk(rt, c, steps[i], placed{input: steps[i].Takes(p.input), tenants: p.tenants}, run)
		return looked[i]
	}
	n := len(steps)
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if look(mid).finished() {
			lo = mid + 1
		} else {
			hi = mid
		}
		if err := c.Ctx.Err(); err != nil {
			return 0, nil, err
		}
	}
	if lo == n {
		c.Say("resume: all %d steps are done (looked at %d)", n, len(looked))
		return n, nil, nil
	}
	c.Say("resume: %d of %d steps are done (looked at %d); picking up at step %d, %s", lo, n, len(looked), lo+1, steps[lo].Name)
	pk := look(lo)
	if pk.failed != nil {
		return lo, nil, nil
	}
	pk.locate()
	if pk.done > 0 {
		pk.report(c)
	}
	return lo, pk, nil
}

func (pk *pickUp) send(call *runtime.Call, e endpoint.Endpoint, to config.Tenant, where string) error {
	path, err := e.Fill(call.Args)
	if err != nil || e.Method == "" || e.Method == http.MethodGet {
		return callEndpoint(pk.rt, call, pk.name, e, to, where)
	}
	body := bodyOf(call)
	if pk.next < pk.done {
		at := pk.plan[pk.next]
		if e.Method != at.method || !samePath(at.path, path) {
			call.Say("resume: %s %s isn't the next write the dry run listed (%s %s), so it goes out", e.Method, path, at.method, at.path)
		} else {
			pk.next++
			answer := body
			if e.Method == http.MethodPost {
				have, ok := pk.find(to, path, body)
				if !ok {
					return callEndpoint(pk.rt, call, pk.name, e, to, where)
				}
				answer = have
			}
			call.Say("skip %s %s%s: on the portal", e.Method, path, where)
			call.Result = EndpointCall{Answer: answer}
			return nil
		}
	}
	return callEndpoint(pk.rt, call, pk.name, e, to, where)
}

func bodyOf(call *runtime.Call) any {
	var body any
	if b := call.Args["body"]; b != "" {
		_ = json.Unmarshal([]byte(b), &body)
	}
	return body
}

func (pk *pickUp) onPortal(i int) bool {
	w := pk.plan[i]
	path, ok := pk.real(w.path)
	if !ok {
		return false
	}
	switch w.method {
	case http.MethodPost:
		_, ok := pk.find(w.to, path, w.body)
		return ok
	case http.MethodDelete:
		_, err := pk.get(w.to, path)
		return err != nil && strings.Contains(err.Error(), "404")
	}
	have, err := pk.get(w.to, path)
	if err != nil {
		return false
	}
	var later []map[string]any
	for _, x := range pk.plan[i+1:] {
		if b, ok := x.body.(map[string]any); ok && x.method == w.method && x.path == w.path {
			later = append(later, b)
		}
	}
	return coversOrLater(have, w.body, later)
}

func coversOrLater(have, want any, later []map[string]any) bool {
	h, ok := have.(map[string]any)
	wm, ok2 := want.(map[string]any)
	if !ok || !ok2 || len(later) == 0 {
		ok, _ := covers(have, want, false)
		return ok
	}
	for k, v := range wm {
		if k == "id" || strings.HasPrefix(k, "_") {
			continue
		}
		if ok, _ := covers(h[k], v, false); ok {
			continue
		}
		set := false
		for _, l := range later {
			if lv, has := l[k]; has {
				if ok, _ := covers(h[k], lv, false); ok {
					set = true
					break
				}
			}
		}
		if !set {
			return false
		}
	}
	return true
}

func (pk *pickUp) real(path string) (string, bool) {
	parts := strings.Split(path, "/")
	for i, s := range parts {
		if !strings.HasPrefix(s, placeholder) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(s, standIn))
		if !strings.HasPrefix(s, standIn) || err != nil || n < 0 || n >= len(pk.plan) {
			return "", false
		}
		made := pk.plan[n]
		in, ok := pk.real(made.path)
		if !ok {
			return "", false
		}
		have, ok := pk.find(made.to, in, made.body)
		if !ok {
			return "", false
		}
		rec, _ := have.(map[string]any)
		parts[i] = fmt.Sprint(rec["id"])
	}
	return strings.Join(parts, "/"), true
}

func (pk *pickUp) get(to config.Tenant, path string) (any, error) {
	p, _, err := pk.rt.PortalAs(pk.ctx, to)
	if err != nil {
		return nil, err
	}
	b, err := p.GetJSON(pk.ctx, path)
	if err != nil {
		return nil, err
	}
	var v any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("%s: json decode: %w", path, err)
		}
	}
	return v, nil
}

func (pk *pickUp) find(to config.Tenant, path string, body any) (any, bool) {
	key := to.TSGID + " " + path
	list, ok := pk.lists[key]
	if !ok {
		if v, err := pk.get(to, path); err == nil {
			list = itemsOf(v)
		}
		pk.lists[key] = list
	}
	for _, it := range list {
		if sameObject(it, body) {
			return it, true
		}
	}
	return nil, false
}

func itemsOf(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		if items, ok := t["items"].([]any); ok {
			return items
		}
		return []any{t}
	}
	return nil
}

func sameObject(have, want any) bool {
	h, ok := have.(map[string]any)
	w, ok2 := want.(map[string]any)
	if !ok || !ok2 {
		return false
	}
	if name, ok := w["name"].(string); ok && name != "" && !strings.HasPrefix(name, placeholder) {
		return h["name"] == name
	}
	same, matched := covers(have, want, true)
	return same && matched > 0
}

func covers(have, want any, lenient bool) (bool, int) {
	switch w := want.(type) {
	case nil:
		return true, 0
	case string:
		if strings.HasPrefix(w, placeholder) {
			return true, 0
		}
		if have == nil {
			return lenient, 0
		}
		h, ok := have.(string)
		return ok && h == w, 1
	case map[string]any:
		if have == nil {
			return lenient || len(w) == 0, 0
		}
		h, ok := have.(map[string]any)
		if !ok {
			return false, 0
		}
		n := 0
		for k, v := range w {
			if k == "id" || strings.HasPrefix(k, "_") {
				continue
			}
			ok, m := covers(h[k], v, lenient)
			if !ok {
				return false, 0
			}
			n += m
		}
		return true, n
	case []any:
		if have == nil {
			return lenient || len(w) == 0, 0
		}
		h, ok := have.([]any)
		if !ok || len(h) != len(w) {
			return false, 0
		}
		return coversAll(h, w, lenient)
	}
	if have == nil {
		return lenient, 0
	}
	return have == want, 1
}

func coversAll(have, want []any, lenient bool) (bool, int) {
	used := make([]bool, len(have))
	n := 0
	for _, last := range []bool{false, true} {
		for _, x := range want {
			if hasMadeUp(x) != last {
				continue
			}
			found := false
			for i, y := range have {
				if used[i] {
					continue
				}
				if ok, m := covers(y, x, lenient); ok {
					used[i], found = true, true
					n += m
					break
				}
			}
			if !found {
				return false, 0
			}
		}
	}
	return true, n
}

func hasMadeUp(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.HasPrefix(t, placeholder)
	case map[string]any:
		for _, x := range t {
			if hasMadeUp(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if hasMadeUp(x) {
				return true
			}
		}
	}
	return false
}

func samePath(plan, real string) bool {
	a, b := strings.Split(plan, "/"), strings.Split(real, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] && !strings.HasPrefix(a[i], placeholder) {
			return false
		}
	}
	return true
}
