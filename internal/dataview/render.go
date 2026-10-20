package dataview

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/sase/api"
)

type Rendered struct {
	View     string
	Sections []RenderedSection
	Errs     []string
}

type RenderedSection struct {
	Title     string
	Rows      []RenderedRow
	Headers   []string
	Widths    []int
	Table     []RenderedTableRow
	Empty     string
	Truncated int
}

type RenderedRow struct {
	Label string
	Value string
}

type RenderedTableRow struct {
	Cells []string
	Err   string
}

func Render(v View, d *Data) Rendered {
	out := Rendered{View: v.Name}
	for _, name := range sortedKeys(d.Errs) {
		out.Errs = append(out.Errs, name+": "+d.Errs[name])
	}

	ctx := map[string]any{}
	for k, val := range d.Vars {
		ctx[k] = val
	}
	for k, val := range d.Bodies {
		ctx[k] = val
	}

	for i, sec := range v.Sections {
		if sec.When != "" && !truthy(eval(sec.When, ctx)) {
			continue
		}
		rs := RenderedSection{Title: sec.Title, Empty: sec.Empty, Truncated: d.Truncated[i]}

		for _, row := range sec.Rows {
			rs.Rows = append(rs.Rows, RenderedRow{Label: row.Label, Value: eval(row.Value, ctx)})
		}

		if sec.ForEach != "" {
			for _, c := range sec.Columns {
				rs.Headers = append(rs.Headers, c.Header)
				rs.Widths = append(rs.Widths, c.Width)
			}
			for _, item := range d.Items[i] {
				tr := RenderedTableRow{Err: RowErr(item)}
				for _, c := range sec.Columns {
					tr.Cells = append(tr.Cells, eval(c.Value, item))
				}
				rs.Table = append(rs.Table, tr)
			}
		}
		out.Sections = append(out.Sections, rs)
	}
	return out
}

// a field the element has not got comes out empty, not "<no value>": half of
// what a view asks for is optional on a given appliance. A view's own mistakes
// do show, prefixed "!", because nothing else would tell whoever wrote it.
func eval(text string, ctx map[string]any) string {
	t, err := template.New("v").Funcs(funcs).Option("missingkey=zero").Parse(text)
	if err != nil {
		return "!" + err.Error()
	}
	var b strings.Builder
	if err := t.Execute(&b, ctx); err != nil {
		if missingField(err) {
			return ""
		}
		return "!" + err.Error()
	}
	return strings.TrimSpace(strings.ReplaceAll(b.String(), "<no value>", ""))
}

func missingField(err error) bool {
	s := err.Error()
	return strings.Contains(s, "nil pointer evaluating") ||
		strings.Contains(s, "can't evaluate field")
}

func expand(text string, ctx map[string]any) (string, error) {
	got := eval(text, ctx)
	if strings.HasPrefix(got, "!") {
		return "", fmt.Errorf("%s", strings.TrimPrefix(got, "!"))
	}
	if err := checkPath(got); err != nil {
		return "", fmt.Errorf("%q expanded to %q, which %w", text, got, err)
	}
	if strings.Contains(got, "//") {
		return "", fmt.Errorf("%q expanded to %q: an id in the path is empty", text, got)
	}
	return got, nil
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "0", "<no value>", "null", "[]", "map[]":
		return false
	}
	return true
}

var funcs = template.FuncMap{
	"utc": func(v any) string {
		n := toInt64(v)
		if n == 0 {
			return ""
		}
		return clock.Stamp(api.UTCTime(n))
	},
	"ago": func(v any) string {
		n := toInt64(v)
		if n == 0 {
			return ""
		}
		t := api.UTCTime(n)
		if t.IsZero() {
			return ""
		}
		return ageString(time.Since(t))
	},
	"unwrap": func(v any) any {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		items, ok := m["items"].([]any)
		if !ok || len(items) == 0 {
			return v
		}
		return items[0]
	},
	"count": func(v any) int {
		switch t := v.(type) {
		case []any:
			return len(t)
		case map[string]any:
			return len(t)
		case string:
			return len(t)
		}
		return 0
	},
	"join": func(v any, sep string) string {
		list, ok := v.([]any)
		if !ok {
			return ""
		}
		parts := make([]string, 0, len(list))
		for _, e := range list {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, sep)
	},
	"yesno": func(v any) string {
		if b, ok := v.(bool); ok && b {
			return "yes"
		}
		return "no"
	},
	"dash": func(v any) string {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" || s == "<nil>" {
			return "-"
		}
		return s
	},
	"lookup": func(list any, field string, want any) any {
		items, ok := list.([]any)
		if !ok || want == nil || want == "" {
			return nil
		}
		for _, it := range items {
			rec, ok := it.(map[string]any)
			if !ok {
				continue
			}
			switch have := rec[field].(type) {
			case []any:
				for _, h := range have {
					if sameValue(h, want) {
						return rec
					}
				}
			default:
				if sameValue(have, want) {
					return rec
				}
			}
		}
		return nil
	},
	"first": func(v any) any {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return ""
		}
		return list[0]
	},

	"lines": func(v any) []any {
		return textLines(v, "")
	},
	"grep": func(v any, pat string) []any {
		return textLines(v, pat)
	},
	"kv": func(v any, key string) string {
		k := strings.ToLower(strings.TrimSpace(key))
		for _, line := range strings.Split(fmt.Sprint(v), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(line), k) {
				continue
			}
			return strings.TrimSpace(strings.TrimLeft(line[len(k):], " \t:="))
		}
		return ""
	},
	"field": func(v any, n int) string {
		f := strings.Fields(fmt.Sprint(v))
		if n < 1 || n > len(f) {
			return ""
		}
		return f[n-1]
	},
}

func textLines(v any, want string) []any {
	want = strings.ToLower(want)
	var out []any
	for _, line := range strings.Split(fmt.Sprint(v), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if want != "" && !strings.Contains(strings.ToLower(line), want) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func ageString(d time.Duration) string {
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
