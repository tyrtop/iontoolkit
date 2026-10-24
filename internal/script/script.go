package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

func Load(dir string) (*Set, error) { return load(dir, check) }

func LoadPackage(dir string) (*Set, error) { return load(dir, compiles) }

func load(dir string, check func(name, src string) error) (*Set, error) {
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
	openSafeLibs(L)
	L.SetGlobal("require", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("require %q: an action has no require", L.CheckString(1))
		return 0
	}))
	ctx, cancel := context.WithTimeout(context.Background(), loadBudget)
	defer cancel()
	L.SetContext(ctx)
	fn, err := L.LoadString(src)
	if err != nil {
		return runErr(name, err)
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s.lua: its top level was still running after %s, so it was not loaded", name, loadBudget)
		}
		return runErr(name, err)
	}
	if _, ok := L.Get(-1).(*lua.LFunction); !ok {
		return fmt.Errorf("%s.lua: ends without returning a function; a script is `return function(ctx) ... end`", name)
	}
	return nil
}

func compiles(name, src string) error {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	if _, err := L.LoadString(src); err != nil {
		return runErr(name, err)
	}
	return nil
}

// gopher-lua raises the context's own error, after the line, once it is done
var vmStopped = regexp.MustCompile(`^(\S*\d+: )?context (deadline exceeded|canceled)$`)

func stoppedOnly(ctx context.Context, err error) bool {
	return ctx.Err() != nil && vmStopped.MatchString(luaMessage(err))
}

func luaMessage(err error) string {
	var ae *lua.ApiError
	if errors.As(err, &ae) && ae.Object != nil {
		return trimLua(ae.Object.String())
	}
	return trimLua(err.Error())
}

var (
	located = regexp.MustCompile(`^\S+\.lua:\d+:`)
	atLine  = regexp.MustCompile(`^\d+: `)
	// gopher-lua's parser writes "<string> line:2(column:0) near 'x':   unterminated string"
	parsed   = regexp.MustCompile(`(?s)^<string> (?:line:(\d+)\(column:\d+\) )?(.*):\s{2,}(\S.*)$`)
	compiled = regexp.MustCompile(`(?s)^compile error near line\((\d+)\) <string>: (.*)$`)
)

func runErr(name string, err error) error {
	msg := luaMessage(err)
	switch {
	case located.MatchString(msg):
		return fmt.Errorf("%s", msg)
	case atLine.MatchString(msg):
		return fmt.Errorf("%s.lua:%s", name, msg)
	}
	return fmt.Errorf("%s.lua: %s", name, msg)
}

func trimLua(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "<string>:"))
	if m := parsed.FindStringSubmatch(s); m != nil {
		s = m[2] + ": " + m[3]
		if m[1] != "" {
			s = m[1] + ": " + s
		}
	} else if m := compiled.FindStringSubmatch(s); m != nil {
		s = "near line " + m[1] + ": " + m[2]
	}
	return strings.TrimSpace(s)
}

type Run struct {
	Name   string
	State  map[string]any
	Export func(text, slug string) (string, error)
	Exec   func(Call) ([]string, any, error)
	Budget time.Duration

	Input    map[string]string
	DoIt     bool
	OnStatus func(string)
}

type Call struct {
	Line string
	Name string
	Args map[string]string
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

const loadBudget = 2 * time.Second

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
	parent := ctx
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
	// a Lua table cannot hold JSON null or tell {} from []
	L.SetField(api, "null", jsonNullValue(L))
	L.SetField(api, "object", L.NewFunction(func(L *lua.LState) int {
		L.Push(newObject(L))
		return 1
	}))

	L.SetField(api, "status", L.NewFunction(func(L *lua.LState) int {
		line := L.CheckString(1)
		add(Effect{Kind: EffectStatus, Value: line})
		if r.OnStatus != nil {
			r.OnStatus(line)
		}
		return 0
	}))
	input := L.NewTable()
	for k, v := range r.Input {
		L.SetField(input, k, lua.LString(v))
	}
	L.SetField(api, "input", input)
	L.SetField(api, "doit", lua.LBool(r.DoIt))
	L.SetGlobal("require", s.require(L))
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
			text, slug := "", optString(L, 2, r.Name)
			if t, ok := L.Get(1).(*lua.LTable); ok {
				var b strings.Builder
				enc := json.NewEncoder(&b)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				if err := enc.Encode(tableFromLua(t)); err != nil {
					L.RaiseError("export: %s", err)
					return 0
				}
				text = strings.TrimSuffix(b.String(), "\n")
			} else {
				text = L.CheckString(1)
			}
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

	if r.Exec != nil {
		L.SetField(api, "run", L.NewFunction(func(L *lua.LState) int {
			call := Call{Line: L.CheckString(1)}
			if t, ok := L.Get(2).(*lua.LTable); ok {
				call = Call{Name: call.Line, Args: argsOf(t)}
			}
			said, result, err := r.Exec(call)
			if err != nil {
				L.RaiseError("run %s: %s", call.Line+call.Name, err)
				return 0
			}
			out := L.NewTable()
			for _, s := range said {
				out.Append(lua.LString(s))
			}
			L.Push(out)
			L.Push(toLua(L, plain(result)))
			return 2
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
		return nil, runErr(r.Name, err)
	}
	stop := func(err error) error {
		took := time.Since(started)
		if took >= time.Second {
			took = took.Round(time.Second)
		} else {
			took = took.Round(time.Millisecond)
		}
		switch {
		case stoppedOnly(ctx, err) && parent.Err() != nil:
			return fmt.Errorf("%s.lua was stopped after %s; what it sent before then was sent", r.Name, took)
		case stoppedOnly(ctx, err):
			return fmt.Errorf("%s.lua ran past its %s budget and was stopped; what it sent before then was sent", r.Name, budget)
		case parent.Err() != nil:
			return fmt.Errorf("stopped after %s: %w", took, runErr(r.Name, err))
		}
		return runErr(r.Name, err)
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		return effects, stop(err)
	}
	body, ok := L.Get(-1).(*lua.LFunction)
	if !ok {
		return nil, fmt.Errorf("%s.lua: does not return a function", r.Name)
	}
	L.Pop(1)

	L.Push(body)
	L.Push(api)
	if err := L.PCall(1, 0, nil); err != nil {
		if failed != nil {
			return effects, failed
		}
		return effects, stop(err)
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

func argsOf(t *lua.LTable) map[string]string {
	out := map[string]string{}
	t.ForEach(func(k, v lua.LValue) {
		if inner, ok := v.(*lua.LTable); ok {
			b, err := json.Marshal(tableFromLua(inner))
			if err == nil {
				out[k.String()] = string(b)
			}
			return
		}
		out[k.String()] = v.String()
	})
	return out
}

func plain(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

type jsonNull struct{}

func jsonNullValue(L *lua.LState) lua.LValue {
	ud := L.NewUserData()
	ud.Value = jsonNull{}
	return ud
}

const (
	metaObject = "__iontk_object"
	metaNulls  = "__iontk_nulls"
)

func newObject(L *lua.LState) *lua.LTable {
	t := L.NewTable()
	meta := L.NewTable()
	L.SetField(meta, metaObject, lua.LTrue)
	L.SetMetatable(t, meta)
	return t
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
		out := newObject(L)
		var nulls *lua.LTable
		for k, e := range t {
			if e == nil {
				if nulls == nil {
					nulls = L.NewTable()
				}
				nulls.Append(lua.LString(k))
				continue
			}
			L.SetField(out, k, toLua(L, e))
		}
		if nulls != nil {
			L.SetField(L.GetMetatable(out), metaNulls, nulls)
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
	case *lua.LTable:
		return tableFromLua(t)
	}
	return nil
}

func tableFromLua(t *lua.LTable) any {
	object, nulls := false, []string(nil)
	if meta, ok := t.Metatable.(*lua.LTable); ok {
		object = lua.LVAsBool(meta.RawGetString(metaObject))
		if list, ok := meta.RawGetString(metaNulls).(*lua.LTable); ok {
			list.ForEach(func(_, k lua.LValue) { nulls = append(nulls, k.String()) })
		}
	}
	n := t.MaxN()
	keys := 0
	t.ForEach(func(lua.LValue, lua.LValue) { keys++ })
	if keys == n && !object {
		out := make([]any, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, fromLua(t.RawGetInt(i)))
		}
		return out
	}
	out := make(map[string]any, keys+len(nulls))
	for _, k := range nulls {
		out[k] = nil
	}
	t.ForEach(func(k, v lua.LValue) { out[k.String()] = fromLua(v) })
	return out
}

func (s *Set) require(L *lua.LState) *lua.LFunction {
	loaded := map[string]lua.LValue{}
	return L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if v, ok := loaded[name]; ok {
			L.Push(v)
			return 1
		}
		src, ok := s.src[name]
		if !ok {
			L.RaiseError("require %q: there is no %s.lua beside this script", name, name)
			return 0
		}
		fn, err := L.Load(strings.NewReader(src), name+".lua")
		if err != nil {
			L.RaiseError("require %q: %s", name, err)
			return 0
		}
		L.Push(fn)
		L.Call(0, 1)
		v := L.Get(-1)
		L.Pop(1)
		if v == lua.LNil {
			v = lua.LTrue
		}
		loaded[name] = v
		L.Push(v)
		return 1
	})
}
