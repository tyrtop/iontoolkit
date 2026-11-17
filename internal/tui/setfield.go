package tui

import (
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

func setFields() []string {
	out := make([]string, 0, len(app.ActionSets))
	for f := range app.ActionSets {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func (m tuiModel) setField(field, value string) (tuiModel, tea.Cmd, error) {
	apply, ok := app.ActionSets[field]
	if !ok {
		return m, nil, fmt.Errorf("%q is not something to set", field)
	}
	if err := apply(m.runtime(), value); err != nil {
		return m, nil, err
	}
	next, cmd := m.syncField(field)
	return next, cmd, nil
}

func (m tuiModel) syncField(field string) (tuiModel, tea.Cmd) {
	switch field {
	case "theme":
		t, _ := ui.ThemeByName(m.option("theme"))
		next, cmd := m.applyTheme(t)
		return next.(tuiModel), cmd
	case "filter":
		q := m.runtime().Query()
		m.filter.SetValue(q)
		m.filterOn = q != ""
		m.rebuildRows()
		if q == "" {
			m.filter.Blur()
			return m, nil
		}
		return m, m.filter.Focus()
	}
	m.rebuildRows()
	return m, nil
}
