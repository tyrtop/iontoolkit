package tui

import (
	"fmt"
	"image/color"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

type termColourState struct {
	bg, fg  color.Color
	hexBg   string
	hexFg   string
	probed  bool
	applied bool
}

var termColour termColourState

const colourProbeWait = 120 * time.Millisecond

type colourProbeDoneMsg struct{}

func probeTermColour() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		tea.RequestForegroundColor,
		tea.Tick(colourProbeWait, func(time.Time) tea.Msg { return colourProbeDoneMsg{} }),
	)
}

func rememberTermColour(bg, fg color.Color) {
	if termColour.applied {
		return
	}
	if h := hexOfColour(bg); h != "" {
		termColour.bg, termColour.hexBg = bg, h
	}
	if h := hexOfColour(fg); h != "" {
		termColour.fg, termColour.hexFg = fg, h
	}
	if termColour.hexBg != "" && termColour.hexFg != "" {
		termColour.probed = true
	}
}

func hexOfColour(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return ui.HexFromRGB([3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)})
}

func frameColours() (bg, fg color.Color) {
	if !termColour.probed {
		return nil, nil
	}
	bg, fg = ui.Background(), ui.Foreground()
	if bg == nil {
		bg = termColour.bg
	}
	if fg == nil {
		fg = termColour.fg
	}
	if bg != nil || fg != nil {
		termColour.applied = true
	}
	return bg, fg
}

func restoreTermColour(w io.Writer) {
	if !termColour.applied {
		return
	}
	if termColour.hexBg != "" {
		fmt.Fprintf(w, "\x1b]11;%s\x07", termColour.hexBg)
	}
	if termColour.hexFg != "" {
		fmt.Fprintf(w, "\x1b]10;%s\x07", termColour.hexFg)
	}
}

func termColourLine() string {
	switch {
	case !termColour.probed:
		return "asking the terminal"
	case termColour.hexBg == "" && termColour.hexFg == "":
		return "terminal did not answer; iontk cannot put its colours back"
	case termColour.hexFg == "":
		return termColour.hexBg + " bg, no fg answer"
	case termColour.hexBg == "":
		return termColour.hexFg + " fg, no bg answer"
	}
	return termColour.hexBg + " bg · " + termColour.hexFg + " fg"
}
