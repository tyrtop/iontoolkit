package ui

import (
	"charm.land/bubbles/v2/spinner"
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

const DefaultSpinner = "minidot"

func spinnerIndex(name string) int {
	for i, c := range spinnerChoices {
		if c.name == name {
			return i
		}
	}
	return 0
}

func SpinnerName(name string) string {
	return spinnerChoices[spinnerIndex(name)].name
}

func NextSpinnerName(name string) string {
	return spinnerChoices[(spinnerIndex(name)+1)%len(spinnerChoices)].name
}

func NewSpinner(name string) spinner.Model {
	s := spinner.New()
	s.Spinner = spinnerChoices[spinnerIndex(name)].spin
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	return s
}
