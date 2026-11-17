package tui

import (
	"fmt"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

const (
	fetchChunkSize = runtime.CellChunk
	fetchPace      = 400 * time.Millisecond
)

type fetchedCell = runtime.Cell

type fetchSpec struct {
	name  string
	key   string
	value *tmpl.T
}

type cellReader = runtime.CellReader

func (m tuiModel) fetchWorking() bool { return m.rt != nil && m.rt.CellsWorking() }

func fetchedPaint(name string) func(*tuiModel, cell) string {
	return func(m *tuiModel, c cell) string {
		if c.here {
			return ui.Plain(c.text)
		}
		got, ok := m.runtime().Cell(name, c.el.Key())
		switch {
		case ok && got.Failed:
			return ui.StyleWarn.Render(c.text)
		case !ok:
			return ui.StyleDim.Render(c.text)
		case got.Cached && strings.HasSuffix(c.text, "*"):
			return c.row(strings.TrimSuffix(c.text, "*")) + ui.StyleDim.Render("*")
		}
		return c.row(c.text)
	}
}

func specOf(c column.Spec) *fetchSpec {
	if !c.Fetches() {
		return nil
	}
	return &fetchSpec{name: c.Name, key: c.Key, value: c.Template}
}

var _ cellReader = (*api.Client)(nil)

func (m tuiModel) cellsNote() string {
	var notes []string
	for _, col := range m.listCols {
		if col.fetch == nil {
			continue
		}
		done, total := m.runtime().CellsProgress(col.name)
		if total == 0 || done >= total {
			continue
		}
		notes = append(notes, ui.StyleWarn.Render(col.name)+
			ui.StyleDim.Render(fmt.Sprintf(" %d/%d", min(done, total), total)))
	}
	if len(notes) == 0 {
		return ""
	}
	if len(notes) > 2 {
		notes = notes[:2]
	}
	return m.spinner.View() + " " + strings.Join(notes, ui.StyleDim.Render(" · "))
}
