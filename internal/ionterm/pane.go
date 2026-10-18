package ionterm

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type View struct {
	Content string
	CursorX int
	CursorY int
}

func blank(line string) bool {
	return strings.TrimSpace(ansi.Strip(line)) == ""
}

func Render(screen string, cursorX, cursorY, width, height int) View {
	if width <= 0 || height <= 0 {
		return View{}
	}

	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")

	last := len(lines)
	for last > 0 && blank(lines[last-1]) && last-1 > cursorY {
		last--
	}
	lines = lines[:last]

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

	return View{
		Content: strings.Join(out, "\n"),
		CursorX: min(max(0, cursorX-left), width-1),
		CursorY: min(max(0, cursorY-top), height-1),
	}
}

func Window(screen string, cursorX, cursorY, width, height int) string {
	return Render(screen, cursorX, cursorY, width, height).Content
}

func (s *Session) View(width, height int) View {
	x, y := s.Cursor()
	return Render(s.Render(), x, y, width, height)
}

func (s *Session) Window(width, height int) string {
	return s.View(width, height).Content
}
