package dataview

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

// Rendered is a view's text, laid out but not styled: the TUI paints it, and a
// test can read it without a terminal.
type Rendered struct {
	View     string
	Sections []RenderedSection
	// Errs is the fetches that failed, so a pane can say why a section is
	// thin rather than leaving it blank.
	Errs []string
}

type RenderedSection struct {
	Title   string
	Rows    []RenderedRow
	Headers []string
	Widths  []int
	Table   []RenderedTableRow
	Empty   string
	// Truncated is how many rows max_items dropped.
	Truncated int
}

type RenderedRow struct {
	Label string
	Value string
}

type RenderedTableRow struct {
	Cells []string
	// Err is a per-item fetch that failed, which is why the cells after the
	// first may be empty.
	Err string
}

// Render turns one view's data into text. A template that fails renders as the
// error in place of the value: a broken view should say so where the value
// would have been, not take the pane down.
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

// eval renders one template. A field the element has not got comes out empty,
// not "<no value>" and not an error: half of what a view asks for is optional
// on a given appliance -- an element with no HA has no spoke_ha_config for
// {{ .el.spoke_ha_config.cluster_id }} to walk into. A view's own mistakes, a
// bad function or unparseable text, do show, prefixed with "!", because
// nothing else would tell the person who wrote it.
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

// missingField is text/template's way of saying the path ran out: walking
// into a field of something absent, or of a type that has no such field.
func missingField(err error) bool {
	s := err.Error()
	return strings.Contains(s, "nil pointer evaluating") ||
		strings.Contains(s, "can't evaluate field")
}

// expand is eval for a path, where an empty result is a bug rather than a
// blank: a path with an unresolved id in it would fetch the wrong thing.
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
	// utc is the API's timestamp integers, whose unit varies by field. Always
	// go through this: the scale is three orders of magnitude apart between
	// two fields of the same object.
	"utc": func(v any) string {
		n := toInt64(v)
		if n == 0 {
			return ""
		}
		return api.UTCTime(n).Format("2006-01-02 15:04")
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
	// unwrap takes the single record out of an items envelope, which is how a
	// bypass pair's status arrives where every other interface sends a bare
	// object.
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
	// lookup is the join the format would otherwise be missing: find the
	// record in a list whose field matches a value. The field may hold the
	// value or be a list containing it, so it works both ways round -- a
	// circuit found by the id a port is bound to, and the port found by the
	// circuit id in its site_wan_interface_ids.
	//
	// It returns the record, not one field, so a caller can use several:
	// {{ with lookup $.wans.items "id" .item.circuit }}{{ .name }}{{ end }}
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
	// first is the first element of a list, for a view that wants one address
	// out of ipv4_addresses.
	"first": func(v any) any {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return ""
		}
		return list[0]
	},
}

// sameValue compares what came out of two JSON documents. Ids are strings in
// this API, but comparing them as text keeps a number on one side of a join
// from silently never matching.
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

// ageString is coarse on purpose: a reboot three weeks ago does not need
// minutes.
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
