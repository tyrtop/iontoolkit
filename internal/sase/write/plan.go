package write

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"tyrtop.com/iontk/internal/tmpl"
)

type Getter interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
}

type Change struct {
	Field string `json:"field"`
	From  any    `json:"from"`
	To    any    `json:"to"`
}

type Plan struct {
	Spec    string
	Label   string
	Method  string
	Path    string
	Before  map[string]any
	After   map[string]any
	Changes []Change
}

func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// the whole record, _etag and all: the portal refuses a stale one
func (p Plan) Body() ([]byte, error) { return json.Marshal(p.After) }

func (w Spec) Records(ctx context.Context, g Getter, vars map[string]string) ([]map[string]any, error) {
	path, err := w.list.FillFrom(varsCtx(vars, nil))
	if err != nil {
		return nil, fmt.Errorf("%s list: %w", w.Name, err)
	}
	b, err := g.GetJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	var body any
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("%s: json decode: %w", path, err)
	}
	if m, ok := body.(map[string]any); ok {
		body = m["items"]
	}
	list, _ := body.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if rec, ok := it.(map[string]any); ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (w Spec) Matches(rec map[string]any, sel string) bool {
	field, want := w.Match, sel
	if k, v, ok := strings.Cut(sel, "="); ok {
		field, want = strings.TrimSpace(k), strings.TrimSpace(v)
	}
	got, ok := lookup(rec, field)
	return ok && strings.EqualFold(fmt.Sprint(got), want)
}

func (w Spec) LabelOf(vars map[string]string, rec map[string]any) string {
	return tmpl.Eval(w.Label, varsCtx(vars, rec))
}

func (w Spec) Plan(vars map[string]string, rec map[string]any, set map[string]string) (Plan, error) {
	path, err := w.item.FillFrom(varsCtx(vars, rec))
	if err != nil {
		return Plan{}, fmt.Errorf("%s item: %w", w.Name, err)
	}
	after, err := clone(rec)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{
		Spec:   w.Name,
		Label:  w.LabelOf(vars, rec),
		Method: w.item.Method,
		Path:   path,
		Before: rec,
		After:  after,
	}

	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return w.fieldIndex(keys[i]) < w.fieldIndex(keys[j]) })

	for _, k := range keys {
		f, ok := w.Field(k)
		if !ok {
			return Plan{}, fmt.Errorf("%s has no editable field %q (it has %s)", w.Name, k, strings.Join(w.FieldKeys(), ", "))
		}
		to, err := parse(f, set[k])
		if err != nil {
			return Plan{}, err
		}
		from, _ := lookup(rec, k)
		if same(from, to) {
			continue
		}
		if err := assign(after, k, to); err != nil {
			return Plan{}, err
		}
		p.Changes = append(p.Changes, Change{Field: k, From: from, To: to})
	}
	return p, nil
}

func (w Spec) fieldIndex(key string) int {
	for i, f := range w.Fields {
		if f.Key == key {
			return i
		}
	}
	return len(w.Fields)
}

func Show(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func parse(f Field, s string) (any, error) {
	s = strings.TrimSpace(s)
	switch f.Type {
	case TypeBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("%s is true or false, not %q", f.Label, s)
		}
		return b, nil
	case TypeInt:
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, not %q", f.Label, s)
		}
		return float64(n), nil
	case TypeList:
		out := []any{}
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out, nil
	}
	return s, nil
}

func same(a, b any) bool {
	if a == nil {
		switch v := b.(type) {
		case string:
			return v == ""
		case []any:
			return len(v) == 0
		}
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func Value(rec map[string]any, key string) any {
	v, _ := lookup(rec, key)
	return v
}

func lookup(rec map[string]any, key string) (any, bool) {
	cur := any(rec)
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// a DHCP interface carries static_config as null
func assign(rec map[string]any, key string, v any) error {
	parts := strings.Split(key, ".")
	cur := rec
	for _, part := range parts[:len(parts)-1] {
		switch next := cur[part].(type) {
		case map[string]any:
			cur = next
		case nil:
			m := map[string]any{}
			cur[part] = m
			cur = m
		default:
			return fmt.Errorf("%s: %s is a %T, not an object", key, part, next)
		}
	}
	cur[parts[len(parts)-1]] = v
	return nil
}

func clone(rec map[string]any) (map[string]any, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("copy record: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("copy record: %w", err)
	}
	return out, nil
}

func varsCtx(vars map[string]string, item map[string]any) map[string]any {
	ctx := make(map[string]any, len(vars)+1)
	for k, v := range vars {
		ctx[k] = v
	}
	if item != nil {
		ctx["item"] = item
	}
	return ctx
}
