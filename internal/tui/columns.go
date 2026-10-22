package tui

import (
	"sync"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

const (
	colCaret  = "caret"
	colStatus = "status"
	colName   = "name"
	colSite   = "site"
	colModel  = "model"
	colSerial = "sn"
	colState  = "state"
	colHA     = "ha"
	colTenant = "tenant"
)

const colGap = 2

const selectedMark = "▌ "

type elementColumn struct {
	name  string
	title string
	min   int
	max   int
	flex  bool
	live  bool
	mark  bool

	shown *tmpl.T
	fetch *fetchSpec
	value func(m *tuiModel, el elementRow) string
	paint func(m *tuiModel, c cell) string
}

type cell struct {
	el     elementRow
	text   string
	here   bool
	ticked bool
	row    func(string) string
}

var goProviders = map[string]elementColumn{
	colCaret:  {name: colCaret, paint: paintCaret},
	colStatus: {name: colStatus, live: true, value: valueStatus, paint: paintStatus},
	colHA:     {name: colHA, value: valueHA, paint: paintHA},
}

func goProviderNames() []string { return []string{colCaret, colStatus, colHA} }

var (
	defaultOnce sync.Once
	defaultCols []elementColumn
	defaultErr  error
)

func defaultColumns() ([]elementColumn, error) {
	defaultOnce.Do(func() {
		set, err := column.Builtin()
		if err != nil {
			defaultErr = err
			return
		}
		defaultCols, defaultErr = buildColumns(set, nil)
	})
	return defaultCols, defaultErr
}

func (m *tuiModel) columns() []elementColumn {
	cols := m.listSpec
	if cols == nil {
		cols, _ = defaultColumns()
	}
	when := m.fleetWhen()
	out := make([]elementColumn, 0, len(cols))
	for _, c := range cols {
		if c.shown != nil && !tmpl.Truthy(c.shown.Render(when)) {
			continue
		}
		out = append(out, c)
	}
	return markColumns(out)
}

func (m *tuiModel) fleetWhen() map[string]any {
	return map[string]any{
		"elements":  len(m.all),
		"tenants":   m.tenantCount,
		"serials":   m.showSerialColumn(),
		"clustered": m.showHAColumn(),
	}
}

func markColumns(cols []elementColumn) []elementColumn {
	out := make([]elementColumn, len(cols))
	copy(out, cols)
	for i, c := range out {
		if !c.mark {
			continue
		}
		out[i].value = markValue(c.value)
		out[i].paint = markPaint(c.paint)
	}
	return out
}

func sameColumns(a, b []elementColumn) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name {
			return false
		}
	}
	return true
}

func valueStatus(m *tuiModel, el elementRow) string {
	glyph, _ := m.rowStatusOf(el)
	return glyph
}

func valueHA(m *tuiModel, el elementRow) string {
	word, cached := m.haState(el)
	if cached {
		return word + "*"
	}
	return word
}

func paintCaret(_ *tuiModel, c cell) string {
	if c.here {
		return ">"
	}
	return c.text
}

func paintStatus(m *tuiModel, c cell) string {
	if c.here {
		return ui.Plain(c.text)
	}
	_, style := m.rowStatusOf(c.el)
	return style.Render(c.text)
}

func paintHA(m *tuiModel, c cell) string {
	if c.here {
		return ui.Plain(c.text)
	}
	return m.haCell(c.el)
}

func afterMark(text string) string {
	r := []rune(text)
	if len(r) < 2 {
		return text
	}
	return string(r[2:])
}

type rowCells struct {
	text  []string
	width []int
}

func (m *tuiModel) rowCells(cols []elementColumn, el elementRow) rowCells {
	key := el.key()
	rc, ok := m.listCache[key]
	if !ok {
		rc = rowCells{text: make([]string, len(cols)), width: make([]int, len(cols))}
		for j, c := range cols {
			if c.live {
				rc.width[j] = -1
				continue
			}
			rc.text[j] = cellText(m, c, el)
			rc.width[j] = ui.Width(rc.text[j])
		}
		m.listCache[key] = rc
	}
	for j, c := range cols {
		if !c.live {
			continue
		}
		rc.text[j] = cellText(m, c, el)
		if c.flex {
			rc.width[j] = ui.Width(rc.text[j])
		}
	}
	return rc
}

func cellText(m *tuiModel, c elementColumn, el elementRow) string {
	if c.value == nil {
		return ""
	}
	return c.value(m, el)
}

func (m *tuiModel) forgetRows() { m.listCache, m.rowCtx = nil, rowContext{} }

func (m *tuiModel) forgetRow(key string) {
	delete(m.listCache, key)
	if m.rowCtx.key == key {
		m.rowCtx = rowContext{}
	}
}

func truncCell(text string, width, limit int) string {
	if width >= 0 && width <= limit {
		return text
	}
	return ui.Truncate(text, limit)
}

func fitColumns(cols []elementColumn, cells [][]int, avail int) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = max(c.min, ui.Width(c.title))
		if c.flex {
			for _, row := range cells {
				if i < len(row) && row[i] > w[i] {
					w[i] = row[i]
				}
			}
		}
		w[i] = min(w[i], c.max)
	}
	return shaveColumns(cols, w, avail)
}

func shaveColumns(cols []elementColumn, w []int, avail int) []int {
	total := func() int {
		n := 0
		for _, x := range w {
			n += x + colGap
		}
		return n
	}
	for total() > avail {
		widest, at := 0, -1
		for i, c := range cols {
			if c.flex && w[i] > c.min && w[i] > widest {
				widest, at = w[i], i
			}
		}
		if at < 0 {
			break
		}
		w[at]--
	}
	return w
}
