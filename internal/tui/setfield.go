package tui

import (
	"fmt"
	"strconv"

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
	t, ok := ui.ThemeByName(value)
	if !ok {
		return m, nil, fmt.Errorf("no theme is called %q", value)
	}
	m.cfg.Theme = t.Name
	if err := m.store.SaveConfig(m.cfg); err != nil {
		return m, nil, err
	}
	next, cmd := m.applyTheme(t)
	return next.(tuiModel), cmd, nil
}

func setMode(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	mode, ok := parseMode(value)
	if !ok {
		return m, nil, fmt.Errorf("no mode is called %q", value)
	}
	m.mode = mode
	m.armedMacro = ""
	return m, nil, nil
}

func setJump(m tuiModel, value string) (tuiModel, tea.Cmd, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return m, nil, fmt.Errorf("the jump is a number of rows, not %q", value)
	}
	m.cfg.PageStep = n
	if err := m.store.SaveConfig(m.cfg); err != nil {
		return m, nil, err
	}
	return m, nil, nil
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
