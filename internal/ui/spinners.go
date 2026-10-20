package ui

import "charm.land/bubbles/v2/spinner"

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

func KnownSpinner(name string) bool {
	for _, c := range spinnerChoices {
		if c.name == name {
			return true
		}
	}
	return false
}

func SpinnerNames() []string {
	out := make([]string, 0, len(spinnerChoices))
	for _, c := range spinnerChoices {
		out = append(out, c.name)
	}
	return out
}

func NewSpinner(name string) spinner.Model {
	s := spinner.New()
	s.Spinner = spinnerChoices[spinnerIndex(name)].spin
	s.Style = StyleSpinner
	return s
}

func SpinnerFrame(name string) string { return SpinnerFrameAt(name, 0) }

func SpinnerFrameAt(name string, at int) string {
	frames := spinnerChoices[spinnerIndex(name)].spin.Frames
	if len(frames) == 0 {
		return ""
	}
	return frames[((at%len(frames))+len(frames))%len(frames)]
}
