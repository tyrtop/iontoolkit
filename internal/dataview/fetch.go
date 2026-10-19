package dataview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Getter is one authenticated GET of a SASE path, which is all a view is
// allowed to do. The api client satisfies it.
type Getter interface {
	GetJSON(ctx context.Context, path string) ([]byte, error)
}

// Vars are the element's identifiers, which is what a view's paths are
// written against: {{ .element_id }}, {{ .site_id }}, {{ .cluster_id }}.
type Vars map[string]string

// Data is one view's answers for one element.
type Data struct {
	// Vars are the ids the view was fetched for. Kept because the templates
	// use them too: a section gated on {{ .cluster_id }} is asking whether the
	// element is in a cluster at all.
	Vars Vars
	// Bodies is each view-level fetch, decoded, keyed by its name.
	Bodies map[string]any
	// Errs is the fetches that failed, keyed the same way. A view whose
	// optional half is refused still renders the half that answered.
	Errs map[string]string
	// Items is each table section's rows, keyed by section index, with any
	// per-item fetches already merged in.
	Items map[int][]map[string]any
	// Truncated is the sections whose for_each was longer than max_items.
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

// Fetch reads everything one view needs. It does not stop at the first
// failure: a view is several unrelated endpoints, and one of them being
// refused says nothing about the others.
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

	// Nothing at all answered: that is a tenant or a token problem rather than
	// a view problem, and saying so beats a pane of blank rows.
	if len(v.Fetch) > 0 && len(d.Bodies) == 0 {
		return d, fmt.Errorf("%s: %s", v.Name, firstErr(d.Errs))
	}
	return d, nil
}

// RowErr is what a per-item fetch failure is stored under, so a row can show
// why its cells are empty.
func errKey(name string) string { return "_err_" + name }

// RowErr returns the first per-item fetch error on a row.
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

// itemsAt walks a dotted path to the list a table iterates. A path that lands
// on nothing is an empty table, not an error: an element with no subinterfaces
// has no subinterfaces. A path that lands on something that is not a list is a
// mistake in the view.
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

// varsCtx is the element's ids as a template context, which every template in
// a view starts from.
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
