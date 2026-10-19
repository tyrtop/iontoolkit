package ionterm

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type View struct {
	Content string
	CursorX int
	CursorY int

	// Offset is how far above the live bottom the window was taken, after
	// clamping to what there is to scroll; Total is every line the session
	// holds, screen and scrollback together. A marker needs both, or a pane
	// frozen part way up reads as a hung one.
	Offset int
	Total  int
}

func blank(line string) bool {
	return strings.TrimSpace(ansi.Strip(line)) == ""
}

// The blank row the cursor sits on stays: an empty prompt line is where the
// typing goes.
func screenLines(screen string, cursorY int) []string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	last := len(lines)
	for last > 0 && blank(lines[last-1]) && last-1 > cursorY {
		last--
	}
	return lines[:last]
}

// The window is padded out to its full height, or the frame changes size as
// output arrives.
func fitLines(lines []string, left, width, height int) string {
	out := make([]string, height)
	for i := range out {
		if i >= len(lines) {
			out[i] = ""
			continue
		}
		ln := lines[i]
		if left > 0 {
			ln = ansi.Cut(ln, left, left+width)
		} else if ansi.StringWidth(ln) > width {
			ln = ansi.Truncate(ln, width, "")
		}
		out[i] = ln
	}
	return strings.Join(out, "\n")
}

func Render(screen string, cursorX, cursorY, width, height int) View {
	if width <= 0 || height <= 0 {
		return View{}
	}

	lines := screenLines(screen, cursorY)

	top := 0
	if len(lines) > height {
		top = min(max(0, cursorY-height+1), len(lines)-height)
	}
	lines = lines[top:]
	if len(lines) > height {
		lines = lines[:height]
	}

	left := 0
	if cursorX >= width {
		left = cursorX - width + 1
	}

	return View{
		Content: fitLines(lines, left, width, height),
		CursorX: min(max(0, cursorX-left), width-1),
		CursorY: min(max(0, cursorY-top), height-1),
	}
}

func Window(screen string, cursorX, cursorY, width, height int) string {
	return Render(screen, cursorX, cursorY, width, height).Content
}

func (s *Session) View(width, height int) View {
	return s.ScrolledView(width, height, 0)
}

func (s *Session) Window(width, height int) string {
	return s.View(width, height).Content
}

// ScrolledView clamps off to what there is to scroll and reports what it used,
// so a caller can hold an offset without tracking how much output arrived
// behind it.
//
// Everything it reads is taken under one lock: the screen, the cursor and the
// scrollback have to agree, or a line shows up twice in the frame that catches
// them mid-write.
func (s *Session) ScrolledView(width, height, off int) View {
	if width <= 0 || height <= 0 {
		return View{}
	}

	s.emuMu.Lock()
	defer s.emuMu.Unlock()

	screen := s.emu.Render()
	pos := s.emu.CursorPosition()
	sb := s.emu.Scrollback()

	lines := screenLines(screen, pos.Y)
	total := sb.Len() + len(lines)
	off = max(0, min(off, max(0, total-height)))

	if off == 0 {
		v := Render(screen, pos.X, pos.Y, width, height)
		v.Total = total
		return v
	}

	// Window over the scrollback and the screen as one buffer, bottom
	// anchored and then lifted.
	end := total - off
	start := max(0, end-height)
	window := make([]string, 0, height)
	for i := start; i < min(end, sb.Len()); i++ {
		window = append(window, sb.Line(i).Render())
	}
	if end > sb.Len() {
		window = append(window, lines[max(0, start-sb.Len()):end-sb.Len()]...)
	}

	// No cursor: it belongs to the live bottom, and drawing it in the middle
	// of frozen output would point at a line nothing is being typed on.
	return View{
		Content: fitLines(window, 0, width, height),
		Offset:  off,
		Total:   total,
	}
}

// MaxScroll is what a key has to clamp to: without it a held key runs the
// offset up into nothing and the way back is as many presses as it took.
func (s *Session) MaxScroll(height int) int {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	lines := screenLines(s.emu.Render(), s.emu.CursorPosition().Y)
	return max(0, s.emu.ScrollbackLen()+len(lines)-height)
}

func (s *Session) Scrollback() int {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	return s.emu.ScrollbackLen()
}

// What a copy takes: scrollback and screen, stripped, trailing blanks
// dropped. One lock, like ScrolledView, so the halves agree.
func (s *Session) Buffer() string {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()

	sb := s.emu.Scrollback()
	lines := screenLines(s.emu.Render(), s.emu.CursorPosition().Y)

	out := make([]string, 0, sb.Len()+len(lines))
	for i := 0; i < sb.Len(); i++ {
		out = append(out, strings.TrimRight(ansi.Strip(sb.Line(i).Render()), " \t"))
	}
	for _, ln := range lines {
		out = append(out, strings.TrimRight(ansi.Strip(ln), " \t"))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
