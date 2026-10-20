package tui

import (
	"fmt"
	"image/color"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/ui"
)

type termColorState struct {
	bg, fg  color.Color
	hexBg   string
	hexFg   string
	probed  bool
	applied bool
}

var termColor termColorState

const colorProbeWait = 120 * time.Millisecond

type colorProbeDoneMsg struct{}

func probeTermColor() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		tea.RequestForegroundColor,
		tea.Tick(colorProbeWait, func(time.Time) tea.Msg { return colorProbeDoneMsg{} }),
	)
}

func rememberTermColor(bg, fg color.Color) {
	if termColor.applied {
		return
	}
	if h := hexOfColor(bg); h != "" {
		termColor.bg, termColor.hexBg = bg, h
	}
	if h := hexOfColor(fg); h != "" {
		termColor.fg, termColor.hexFg = fg, h
	}
	if termColor.hexBg != "" && termColor.hexFg != "" {
		termColor.probed = true
	}
}

func hexOfColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return ui.HexFromRGB([3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)})
}

func frameColors() (bg, fg color.Color) {
	if !termColor.probed {
		return nil, nil
	}
	bg, fg = ui.Background(), ui.Foreground()
	if bg == nil {
		bg = termColor.bg
	}
	if fg == nil {
		fg = termColor.fg
	}
	if bg != nil || fg != nil {
		termColor.applied = true
	}
	return bg, fg
}

func restoreTermColor(w io.Writer) {
	if !termColor.applied {
		return
	}
	if termColor.hexBg != "" {
		fmt.Fprintf(w, "\x1b]11;%s\x07", termColor.hexBg)
	}
	if termColor.hexFg != "" {
		fmt.Fprintf(w, "\x1b]10;%s\x07", termColor.hexFg)
	}
}

func termColorLine() string {
	switch {
	case !termColor.probed:
		return "asking the terminal"
	case termColor.hexBg == "" && termColor.hexFg == "":
		return "terminal did not answer; iontk cannot put its colors back"
	case termColor.hexFg == "":
		return termColor.hexBg + " bg, no fg answer"
	case termColor.hexBg == "":
		return termColor.hexFg + " fg, no bg answer"
	}
	return termColor.hexBg + " bg · " + termColor.hexFg + " fg"
}
