package script

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"tyrtop.com/iontk/internal/tmpl"
)

const DirName = "actions"

type Set struct {
	src  map[string]string
	from map[string]string
}

func Load(dir string) (*Set, error) {
	set := &Set{src: map[string]string{}, from: map[string]string{}}
	if dir == "" {
		return set, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return set, fmt.Errorf("read %s: %w", dir, err)
	}

	var bad []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			bad = append(bad, fmt.Sprintf("%s: %s", e.Name(), rerr))
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".lua")
		if err := check(name, string(b)); err != nil {
			bad = append(bad, err.Error())
			continue
		}
		set.src[name], set.from[name] = string(b), path
	}
	if len(bad) > 0 {
		return set, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return set, nil
}

func (s *Set) Has(name string) bool {
	_, ok := s.src[name]
	return ok
}

func (s *Set) Source(name string) string { return s.from[name] }

func (s *Set) Names() []string {
	out := make([]string, 0, len(s.src))
	for name := range s.src {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func check(name, src string) error {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	fn, err := L.LoadString(src)
	if err != nil {
		return fmt.Errorf("%s.lua: %s", name, trimLua(err.Error()))
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		return fmt.Errorf("%s.lua: %s", name, trimLua(err.Error()))
	}
	if _, ok := L.Get(-1).(*lua.LFunction); !ok {
		return fmt.Errorf("%s.lua: ends without returning a function; a script is `return function(ctx) ... end`", name)
	}
	return nil
}

func trimLua(s string) string {
	s = strings.TrimPrefix(s, "<string>:")
	return strings.TrimSpace(s)
}

type Run struct {
	Name   string
	State  map[string]any
	Get    func(path string, vars map[string]string) (any, error)
	Export func(text, slug string) (string, error)
	Budget time.Duration
}

type Effect struct {
	Kind  string
	Field string
	Value string
}

const (
	EffectStatus = "status"
	EffectSet    = "set"
	EffectGoto   = "goto"
	EffectExport = "export"
)

const DefaultBudget = 2 * time.Minute

type stopped struct{ reason string }

func (e stopped) Error() string { return e.reason }

func (s *Set) Run(ctx context.Context, r Run) ([]Effect, error) {
	src, ok := s.src[r.Name]
	if !ok {
		return nil, fmt.Errorf("no script is called %q", r.Name)
	}
	budget := r.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	L.SetContext(ctx)
	openSafeLibs(L)

	var effects []Effect
	var failed *stopped
	started := time.Now()

	add := func(e Effect) { effects = append(effects, e) }

	api := L.NewTable()
	L.SetField(api, "state", toLua(L, r.State))

	L.SetField(api, "status", L.NewFunction(func(L *lua.LState) int {
		add(Effect{Kind: EffectStatus, Value: L.CheckString(1)})
		return 0
	}))
	L.SetField(api, "fail", L.NewFunction(func(L *lua.LState) int {
		failed = &stopped{reason: L.CheckString(1)}
		L.RaiseError("%s", failed.reason)
		return 0
	}))
	L.SetField(api, "set", L.NewFunction(func(L *lua.LState) int {
		add(Effect{Kind: EffectSet, Field: L.CheckString(1), Value: L.CheckString(2)})
		return 0
	}))
	L.SetField(api, "goto", L.NewFunction(func(L *lua.LState) int {
		add(Effect{Kind: EffectGoto, Value: L.CheckString(1)})
		return 0
	}))
	L.SetField(api, "open", L.GetField(api, "goto"))

	if r.Export != nil {
		L.SetField(api, "export", L.NewFunction(func(L *lua.LState) int {
			text, slug := L.CheckString(1), optString(L, 2, r.Name)
			where, err := r.Export(text, slug)
			if err != nil {
				L.RaiseError("export: %s", err)
				return 0
			}
			add(Effect{Kind: EffectExport, Value: where})
			L.Push(lua.LString(where))
			return 1
		}))
	}

	if r.Get != nil {
		L.SetField(api, "get", L.NewFunction(func(L *lua.LState) int {
			body, err := r.Get(L.CheckString(1), varsOf(L, 2))
			if err != nil {
				L.RaiseError("get %s: %s", L.CheckString(1), err)
				return 0
			}
			L.Push(toLua(L, body))
			return 1
		}))
		L.SetField(api, "try", L.NewFunction(func(L *lua.LState) int {
			body, err := r.Get(L.CheckString(1), varsOf(L, 2))
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(toLua(L, body))
			return 1
		}))
	}

	L.SetField(api, "budget", L.NewFunction(func(L *lua.LState) int {
		d := optDuration(L, 1, budget)
		end := time.Now().Add(d)
		b := L.NewTable()
		L.SetField(b, "tick", L.NewFunction(func(L *lua.LState) int {
			every := optDuration(L, 2, time.Second)
			if time.Now().Add(every).After(end) {
				L.Push(lua.LFalse)
				return 1
			}
			select {
			case <-ctx.Done():
				L.Push(lua.LFalse)
			case <-time.After(every):
				L.Push(lua.LTrue)
			}
			return 1
		}))
		L.SetField(b, "spent", L.NewFunction(func(L *lua.LState) int {
			L.Push(lua.LString(time.Since(started).Round(time.Second).String()))
			return 1
		}))
		L.Push(b)
		return 1
	}))

	for name, fn := range helpers() {
		L.SetField(api, name, L.NewFunction(fn))
	}

	fn, err := L.LoadString(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", r.Name, trimLua(err.Error()))
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		return effects, fmt.Errorf("%s: %s", r.Name, trimLua(err.Error()))
	}
	body, ok := L.Get(-1).(*lua.LFunction)
	if !ok {
		return nil, fmt.Errorf("%s: does not return a function", r.Name)
	}
	L.Pop(1)

	L.Push(body)
	L.Push(api)
	if err := L.PCall(1, 0, nil); err != nil {
		if failed != nil {
			return effects, failed
		}
		if ctx.Err() != nil {
			return effects, fmt.Errorf("%s ran for %s and was stopped", r.Name, budget)
		}
		return effects, fmt.Errorf("%s: %s", r.Name, trimLua(err.Error()))
	}
	return effects, nil
}

func openSafeLibs(L *lua.LState) {
	for _, open := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(open.fn))
		L.Push(lua.LString(open.name))
		L.Call(1, 0)
	}
	// print writes straight to the terminal the TUI is drawing on
	for _, gone := range []string{"dofile", "loadfile", "load", "loadstring", "print", "collectgarbage"} {
		L.SetGlobal(gone, lua.LNil)
	}
}

func helpers() map[string]lua.LGFunction {
	return map[string]lua.LGFunction{
		"utc": func(L *lua.LState) int {
			L.Push(lua.LString(callFunc("utc", L.CheckAny(1))))
			return 1
		},
		"ago": func(L *lua.LState) int {
			L.Push(lua.LString(callFunc("ago", L.CheckAny(1))))
			return 1
		},
		"pick": func(L *lua.LState) int {
			list, want := L.CheckTable(1), L.CheckFunction(2)
			var found lua.LValue = lua.LNil
			list.ForEach(func(_, v lua.LValue) {
				if found != lua.LNil {
					return
				}
				L.Push(want)
				L.Push(v)
				if err := L.PCall(1, 1, nil); err != nil {
					return
				}
				if lua.LVAsBool(L.Get(-1)) {
					found = v
				}
				L.Pop(1)
			})
			L.Push(found)
			return 1
		},
		"lookup": func(L *lua.LState) int {
			list, field, want := L.CheckTable(1), L.CheckString(2), L.CheckAny(3)
			var found lua.LValue = lua.LNil
			list.ForEach(func(_, v lua.LValue) {
				row, ok := v.(*lua.LTable)
				if !ok || found != lua.LNil {
					return
				}
				switch have := L.GetField(row, field).(type) {
				case *lua.LTable:
					have.ForEach(func(_, one lua.LValue) {
						if one.String() == want.String() {
							found = row
						}
					})
				default:
					if have.String() == want.String() {
						found = row
					}
				}
			})
			L.Push(found)
			return 1
		},
	}
}

func callFunc(name string, v lua.LValue) string {
	fn, ok := tmpl.Funcs[name].(func(any) string)
	if !ok {
		return ""
	}
	return fn(fromLua(v))
}

func optString(L *lua.LState, at int, def string) string {
	if L.GetTop() < at {
		return def
	}
	return L.CheckString(at)
}

func optDuration(L *lua.LState, at int, def time.Duration) time.Duration {
	if L.GetTop() < at {
		return def
	}
	d, err := time.ParseDuration(L.CheckString(at))
	if err != nil || d <= 0 {
		L.RaiseError("%q is not a length of time", L.CheckString(at))
		return def
	}
	return d
}

func varsOf(L *lua.LState, at int) map[string]string {
	if L.GetTop() < at {
		return nil
	}
	t, ok := L.Get(at).(*lua.LTable)
	if !ok {
		return nil
	}
	out := map[string]string{}
	t.ForEach(func(k, v lua.LValue) { out[k.String()] = v.String() })
	return out
}

func toLua(L *lua.LState, v any) lua.LValue {
	switch t := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(t)
	case string:
		return lua.LString(t)
	case float64:
		return lua.LNumber(t)
	case int:
		return lua.LNumber(t)
	case int64:
		return lua.LNumber(t)
	case json.Number:
		n, _ := t.Float64()
		return lua.LNumber(n)
	case []any:
		out := L.NewTable()
		for _, e := range t {
			out.Append(toLua(L, e))
		}
		return out
	case map[string]any:
		out := L.NewTable()
		for k, e := range t {
			L.SetField(out, k, toLua(L, e))
		}
		return out
	}
	return lua.LString(fmt.Sprint(v))
}

func fromLua(v lua.LValue) any {
	switch t := v.(type) {
	case lua.LBool:
		return bool(t)
	case lua.LString:
		return string(t)
	case lua.LNumber:
		return float64(t)
	}
	return nil
}
