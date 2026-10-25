package dataview

import (
	"tyrtop.com/iontk/internal/tmpl"
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

func eval(text string, ctx map[string]any) string { return tmpl.Eval(text, ctx) }

func truthy(s string) bool { return tmpl.Truthy(s) }
