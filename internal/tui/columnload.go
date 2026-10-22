package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

var Columns string

func ColumnsPath(st *config.Store) string {
	if Columns != "" {
		return Columns
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, column.FileName)
}

func (m *tuiModel) loadColumns(st *config.Store) {
	set, err := column.Load(ColumnsPath(st))
	if err != nil {
		m.columnsErr = err.Error()
		return
	}
	if set == nil {
		return
	}
	base, _ := defaultColumns()
	cols, err := buildColumns(set, base)
	if err != nil {
		m.columnsErr = err.Error()
		return
	}
	m.listSpec, m.listSpecs = cols, set.Columns
	m.listPaths = listPathsOf(set)
}

func buildColumns(set *column.Set, base []elementColumn) ([]elementColumn, error) {
	out := make([]elementColumn, 0, len(set.Columns)+1)
	for _, spec := range set.Columns {
		col, err := buildColumn(spec, base)
		if err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return withCaretAndMark(out), nil
}

func buildColumn(spec column.Spec, base []elementColumn) (elementColumn, error) {
	col, shipped, err := baseColumn(spec, base)
	if err != nil {
		return col, err
	}

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

	if spec.Template != nil {
		col.value = templateValue(spec.Template, spec.Subject())
		col.live = false
	}
	if len(spec.Rules) > 0 {
		col.paint = rulePaint(spec.Rules, spec.Subject())
	}
	col.shown = spec.Shown
	if spec.Fleet() && subjectOf(fleetSpec{path: spec.Fetch}) == "" {
		if _, ok := elementValues[spec.On]; !ok {
			return col, fmt.Errorf("column %q joins on %q, and an element has %s",
				spec.Name, spec.On, strings.Join(sortedFields(elementValues), ", "))
		}
	}
	switch {
	case spec.PerElement():
		col.fetch = specOf(spec)
		col.value = fetchedValue(spec.Name)
		col.paint = fetchedPaint(spec.Name)
		col.live = false
	case spec.Fleet():
		col.value = fleetValue(spec)
		col.live = false
	}
	return col, nil
}

func baseColumn(spec column.Spec, base []elementColumn) (elementColumn, bool, error) {
	if spec.Go != "" {
		col, ok := goProviders[spec.Go]
		if !ok {
			return elementColumn{}, false, fmt.Errorf("column %q names go: %q, and the ones iontk works out are %s",
				spec.Name, spec.Go, strings.Join(goProviderNames(), ", "))
		}
		col.name = spec.Name
		return col, true, nil
	}
	for _, c := range base {
		if c.name == spec.Name {
			return c, true, nil
		}
	}
	if spec.Fetches() {
		return elementColumn{name: spec.Name, min: 6, max: 24, flex: true}, false, nil
	}
	if spec.From != "" {
		return elementColumn{name: spec.Name, min: 4, max: 24, flex: true}, false, nil
	}
	if spec.Template == nil {
		return elementColumn{}, false, fmt.Errorf("column %q is not one iontk draws (%s) and has no value of its own",
			spec.Name, strings.Join(namesOf(base), ", "))
	}
	return elementColumn{name: spec.Name, min: 4, max: 24, flex: true}, false, nil
}

func sortedFields(m map[string]func(*tuiModel, elementRow) any) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func listPathsOf(set *column.Set) map[string]string {
	out := map[string]string{}
	for _, name := range []string{column.ElementsSrc, column.SitesSrc} {
		if path := set.Source(name); path != "" {
			out[name] = path
		}
	}
	return out
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

func namesOf(cols []elementColumn) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.name)
	}
	return out
}

func templateValue(t *tmpl.T, from string) func(*tuiModel, elementRow) string {
	if read, ok := fieldReader(t.Text(), from); ok {
		return read
	}
	flat := flatEnough(t.Text(), from)
	return func(m *tuiModel, el elementRow) string {
		return t.Render(m.rowContext(el, from, flat))
	}
}

func flatEnough(text, from string) bool {
	values := valuesFor(from)
	for _, f := range fieldsIn(text) {
		if _, ok := values[f]; !ok {
			return false
		}
	}
	return true
}

var fieldRef = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)`)

func fieldsIn(text string) []string {
	var out []string
	for _, m := range fieldRef.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func fieldReader(text, from string) (func(*tuiModel, elementRow) string, bool) {
	name, ok := loneField(text)
	if !ok {
		return nil, false
	}
	read, ok := valuesFor(from)[name]
	if !ok {
		return nil, false
	}
	return func(m *tuiModel, el elementRow) string { return asText(read(m, el)) }, true
}

func asText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	}
	return fmt.Sprint(v)
}

func loneField(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{{") || !strings.HasSuffix(t, "}}") {
		return "", false
	}
	t = strings.TrimSpace(t[2 : len(t)-2])
	if !strings.HasPrefix(t, ".") {
		return "", false
	}
	t = t[1:]
	if t == "" || strings.ContainsAny(t, " .|({") {
		return "", false
	}
	return t, true
}

var elementValues = map[string]func(*tuiModel, elementRow) any{
	"id":               func(_ *tuiModel, el elementRow) any { return el.ID },
	"name":             func(_ *tuiModel, el elementRow) any { return el.Name },
	"label":            func(_ *tuiModel, el elementRow) any { return el.label() },
	"model_name":       func(_ *tuiModel, el elementRow) any { return el.Model },
	"serial_number":    func(_ *tuiModel, el elementRow) any { return el.Serial },
	"software_version": func(_ *tuiModel, el elementRow) any { return el.Software },
	"hw_id":            func(_ *tuiModel, el elementRow) any { return el.HWID },
	"site_id":          func(_ *tuiModel, el elementRow) any { return el.SiteID },
	"state":            func(_ *tuiModel, el elementRow) any { return el.State },
	"role":             func(_ *tuiModel, el elementRow) any { return el.Role },
	"description":      func(_ *tuiModel, el elementRow) any { return el.Description },
	"cluster_id":       func(_ *tuiModel, el elementRow) any { return el.ClusterID() },
	"connected":        func(_ *tuiModel, el elementRow) any { return el.Connected },
	"tenant":           func(_ *tuiModel, el elementRow) any { return el.Tenant },
	"tsg":              func(_ *tuiModel, el elementRow) any { return el.TSG },
	"site_name":        func(m *tuiModel, el elementRow) any { return m.siteName(el) },
}

var siteValues = map[string]func(*tuiModel, elementRow) any{
	"id":   func(_ *tuiModel, el elementRow) any { return el.SiteID },
	"name": func(m *tuiModel, el elementRow) any { return m.siteName(el) },
}

func valuesFor(from string) map[string]func(*tuiModel, elementRow) any {
	if from == column.FromSite {
		return siteValues
	}
	return elementValues
}

func rulePaint(rules []column.CompiledRule, from string) func(*tuiModel, cell) string {
	return func(m *tuiModel, c cell) string {
		if c.here {
			return ui.Plain(c.text)
		}
		for _, r := range rules {
			if r.When != nil && !tmpl.Truthy(r.When.Render(m.rowContext(c.el, from, flatEnough(r.When.Text(), from)))) {
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

type rowContext struct {
	key     string
	element map[string]any
	site    map[string]any
	flat    map[string]any
}

func (m *tuiModel) rowContext(el elementRow, from string, flat bool) map[string]any {
	if m.rowCtx.key != el.key() {
		m.rowCtx = rowContext{key: el.key()}
	}
	if from == column.FromSite {
		if m.rowCtx.site == nil {
			m.rowCtx.site = map[string]any{"id": el.SiteID, "name": m.siteName(el)}
		}
		return m.rowCtx.site
	}
	if flat {
		if m.rowCtx.flat == nil {
			ctx := make(map[string]any, len(elementValues))
			for name, read := range elementValues {
				ctx[name] = read(m, el)
			}
			m.rowCtx.flat = ctx
		}
		return m.rowCtx.flat
	}
	if m.rowCtx.element == nil {
		ctx := elementContext(el)
		ctx["site_name"] = m.siteName(el)
		ctx["label"] = el.label()
		ctx["tenant"] = el.Tenant
		ctx["tsg"] = el.TSG
		m.rowCtx.element = ctx
	}
	return m.rowCtx.element
}

func elementContext(el elementRow) map[string]any {
	ctx := map[string]any{}
	if b, err := json.Marshal(el.Element); err == nil {
		_ = json.Unmarshal(b, &ctx)
	}
	ctx["tenant"] = el.Tenant
	ctx["tsg"] = el.TSG
	return ctx
}
