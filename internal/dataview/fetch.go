package dataview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Getter interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
}

type Vars map[string]string

type Data struct {
	Vars      Vars
	Bodies    map[string]any
	Errs      map[string]string
	Items     map[int][]map[string]any
	Truncated map[int]int
}

func newData() *Data {
	return &Data{
		Bodies:    map[string]any{},
		Errs:      map[string]string{},
		Items:     map[int][]map[string]any{},
		Truncated: map[int]int{},
	}
}

func Fetch(ctx context.Context, g Getter, v View, vars Vars) (*Data, error) {
	d := newData()
	d.Vars = vars

	ctxMap := varsCtx(vars)

	for _, name := range sortedKeys(v.Fetch) {
		path, err := expand(v.Fetch[name], ctxMap)
		if err != nil {
			d.Errs[name] = err.Error()
			continue
		}
		body, err := get(ctx, g, path)
		if err != nil {
			d.Errs[name] = err.Error()
			continue
		}
		d.Bodies[name] = body
		ctxMap[name] = body
	}

	for i, sec := range v.Sections {
		if sec.ForEach == "" {
			continue
		}
		items, err := itemsAt(ctxMap, sec.ForEach)
		if err != nil {
			d.Errs[sec.Title] = err.Error()
			continue
		}
		if len(items) > sec.MaxItems {
			d.Truncated[i] = len(items) - sec.MaxItems
			items = items[:sec.MaxItems]
		}

		rows := make([]map[string]any, 0, len(items))
		for _, item := range items {
			row := map[string]any{}
			for k, val := range ctxMap {
				row[k] = val
			}
			row["item"] = item

			for _, name := range sortedKeys(sec.Fetch) {
				path, err := expand(sec.Fetch[name], row)
				if err != nil {
					row[errKey(name)] = err.Error()
					continue
				}
				body, err := get(ctx, g, path)
				if err != nil {
					row[errKey(name)] = err.Error()
					continue
				}
				row[name] = body
			}
			rows = append(rows, row)
		}
		d.Items[i] = rows
	}

	if len(v.Fetch) > 0 && len(d.Bodies) == 0 {
		return d, fmt.Errorf("%s: %s", v.Name, firstErr(d.Errs))
	}
	return d, nil
}

func errKey(name string) string { return "_err_" + name }

func RowErr(row map[string]any) string {
	for _, k := range sortedAnyKeys(row) {
		if strings.HasPrefix(k, "_err_") {
			if s, ok := row[k].(string); ok {
				return s
			}
		}
	}
	return ""
}

func get(ctx context.Context, g Getter, path string) (any, error) {
	b, err := g.GetJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: json decode: %w", path, err)
	}
	return out, nil
}

func itemsAt(ctx map[string]any, path string) ([]any, error) {
	cur := any(ctx)
	for _, part := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, nil
			}
			cur = v[i]
		default:
			return nil, nil
		}
		if cur == nil {
			return nil, nil
		}
	}
	switch v := cur.(type) {
	case []any:
		return v, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("for_each %s is a %T, not a list", path, cur)
	}
}

func varsCtx(vars Vars) map[string]any {
	out := make(map[string]any, len(vars))
	for k, val := range vars {
		out[k] = val
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstErr(errs map[string]string) string {
	for _, k := range sortedKeys(errs) {
		return errs[k]
	}
	return "nothing answered"
}
