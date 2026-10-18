package main

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type spinnerChoice struct {
	name string
	spin spinner.Spinner
}

var spinnerChoices = []spinnerChoice{
	{"minidot", spinner.MiniDot},
	{"dot", spinner.Dot},
	{"line", spinner.Line},
	{"jump", spinner.Jump},
	{"pulse", spinner.Pulse},
	{"points", spinner.Points},
	{"meter", spinner.Meter},
	{"ellipsis", spinner.Ellipsis},
	{"hamburger", spinner.Hamburger},
	{"globe", spinner.Globe},
	{"moon", spinner.Moon},
	{"monkey", spinner.Monkey},
}

const defaultSpinner = "minidot"

func spinnerIndex(name string) int {
	for i, c := range spinnerChoices {
		if c.name == name {
			return i
		}
	}
	return 0
}

func spinnerName(name string) string {
	return spinnerChoices[spinnerIndex(name)].name
}

func nextSpinnerName(name string) string {
	return spinnerChoices[(spinnerIndex(name)+1)%len(spinnerChoices)].name
}

func newSpinner(name string) spinner.Model {
	s := spinner.New()
	s.Spinner = spinnerChoices[spinnerIndex(name)].spin
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	return s
}

func (m tuiModel) withSpinner(name string) (tuiModel, tea.Cmd) {
	m.spinnerName = spinnerName(name)
	m.spinner = newSpinner(m.spinnerName)
	return m, m.spinner.Tick
}
