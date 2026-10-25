package tui

import (
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

func ColumnsPath(st *config.Store) string { return app.ColumnsPath(st) }

func (m *tuiModel) loadColumns(l app.List) {
	m.columnsErr = l.Err
	m.listSpec, m.listFrom = nil, ""
	if l.From != "" {
		base, _ := defaultColumns()
		m.listSpec, m.listFrom = buildColumns(l.Set, base), l.From
	}
}

func buildColumns(set *column.Set, base []elementColumn) []elementColumn {
	out := make([]elementColumn, 0, len(set.Columns)+1)
	for _, spec := range set.Columns {
		out = append(out, buildColumn(spec, base))
	}
	return withCaretAndMark(out)
}

func buildColumn(spec column.Spec, base []elementColumn) elementColumn {
	col, shipped := baseColumn(spec, base)

	if shipped {
		col.title = firstOf(spec.Header, col.title)
	} else {
		col.title = firstOf(spec.Header, ui.Title(spec.Name))
	}
	if spec.Min > 0 {
		col.min = spec.Min
	}
	if spec.Max > 0 {
		col.max = spec.Max
	}
	if spec.Flex != nil {
		col.flex = *spec.Flex
	}
	if spec.Mark != nil {
		col.mark = *spec.Mark
	}
	if col.max < col.min {
		col.max = col.min
	}

	if spec.Template != nil || spec.Fetches() || spec.Go == colHA {
		s := spec
		col.value = func(m *tuiModel, el elementRow) string { return m.runtime().CellText(s, el) }
		col.live = false
	}
	if len(spec.Rules) > 0 {
		col.paint = rulePaint(spec.Rules, spec.Subject())
	}
	col.shown = spec.Shown
	if spec.PerElement() {
		col.fetch = specOf(spec)
		col.paint = fetchedPaint(spec.Name)
	}
	return col
}

func baseColumn(spec column.Spec, base []elementColumn) (elementColumn, bool) {
	if spec.Go != "" {
		col := goProviders[spec.Go]
		col.name = spec.Name
		return col, true
	}
	for _, c := range base {
		if c.name == spec.Name {
			return c, true
		}
	}
	if spec.Fetches() {
		return elementColumn{name: spec.Name, min: 6, max: 24, flex: true}, false
	}
	return elementColumn{name: spec.Name, min: 4, max: 24, flex: true}, false
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func withCaretAndMark(cols []elementColumn) []elementColumn {
	if len(cols) == 0 || cols[0].name != colCaret {
		caret := goProviders[colCaret]
		caret.min, caret.max = 2, 2
		cols = append([]elementColumn{caret}, cols...)
	}
	for _, c := range cols {
		if c.mark {
			return cols
		}
	}
	for i, c := range cols {
		if c.value == nil {
			continue
		}
		cols[i].mark = true
		break
	}
	return cols
}

func rulePaint(rules []column.CompiledRule, from string) func(*tuiModel, cell) string {
	return func(m *tuiModel, c cell) string {
		if c.here {
			return ui.Plain(c.text)
		}
		for _, r := range rules {
			if r.When != nil && !tmpl.Truthy(r.When.Render(m.runtime().RowContext(c.el, from, r.When.Text()))) {
				continue
			}
			style, _ := ui.StyleNamed(r.Role)
			return style.Render(c.text)
		}
		return c.row(c.text)
	}
}

func markValue(value func(*tuiModel, elementRow) string) func(*tuiModel, elementRow) string {
	return func(m *tuiModel, el elementRow) string {
		if value == nil {
			return selectedMark
		}
		return "  " + value(m, el)
	}
}

func markPaint(inner func(*tuiModel, cell) string) func(*tuiModel, cell) string {
	return func(m *tuiModel, c cell) string {
		if !c.ticked {
			if inner == nil {
				return c.row(c.text)
			}
			return inner(m, c)
		}
		c.text = afterMark(c.text)
		if c.here {
			return selectedMark + c.text
		}
		painted := c.row(c.text)
		if inner != nil {
			painted = inner(m, c)
		}
		return ui.StyleRow.Render(selectedMark) + ui.StyleBold.Render(painted)
	}
}
