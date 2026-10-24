package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

var setters = []struct {
	field string
	set   func(m tuiModel, value string) (tuiModel, tea.Cmd, error)
}{
	{"theme", setTheme},
	{"mode", setMode},
	{"jump", setJump},
	{"filter", setFilter},
}

func setFields() []string {
	out := make([]string, 0, len(setters))
	for _, s := range setters {
		out = append(out, s.field)
	}
	return out
}

func (m tuiModel) setField(field, value string) (tuiModel, tea.Cmd, error) {
	for _, s := range setters {
		if s.field == field {
			return s.set(m, value)
		}
	}
	return m, nil, fmt.Errorf("%q is not something to set", field)
}

func setTheme(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	if err := m.runtime().Set("theme", value); err != nil {
		return m, nil, err
	}
	t, _ := ui.ThemeByName(m.option("theme"))
	next, cmd := m.applyTheme(t)
	return next.(tuiModel), cmd, nil
}

func setMode(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	mode, ok := parseMode(value)
	if !ok {
		return m, nil, fmt.Errorf("no mode is called %q", value)
	}
	m.setMode(mode)
	return m, nil, nil
}

func setJump(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	return m, nil, m.runtime().Set("page_step", value)
}

func setFilter(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	m.filter.SetValue(value)
	m.filterOn = value != ""
	m.applyFilter()
	if value == "" {
		m.filter.Blur()
		return m, nil, nil
	}
	return m, m.filter.Focus(), nil
}
