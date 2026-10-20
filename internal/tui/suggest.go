package tui

import (
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/ioncmd"
	"tyrtop.com/iontk/internal/ui"
)

const suggestRows = 8

type suggest struct {
	items  []string
	cursor int
	sticky bool
}

func newSuggest(pool []string, query string) suggest {
	return suggest{items: ioncmd.Fuzzy(pool, query), cursor: -1}
}

func newPicker(pool []string, query string) suggest {
	return suggest{items: ioncmd.Fuzzy(pool, query), cursor: 0, sticky: true}.clamp()
}

func (s suggest) floor() int {
	if s.sticky {
		return 0
	}
	return -1
}

func (s suggest) clamp() suggest {
	if len(s.items) == 0 {
		s.cursor = -1
		return s
	}
	s.cursor = min(max(s.cursor, s.floor()), len(s.items)-1)
	return s
}

func (s suggest) refilter(pool []string, query string) suggest {
	held, ok := s.current()
	s.items = ioncmd.Fuzzy(pool, query)
	s.cursor = s.floor()
	if ok {
		for i, it := range s.items {
			if it == held {
				s.cursor = i
				break
			}
		}
	}
	return s.clamp()
}

func (s suggest) move(step int) suggest {
	s.cursor += step
	return s.clamp()
}

func (s suggest) current() (string, bool) {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return "", false
	}
	return s.items[s.cursor], true
}

func (s suggest) window(rows int) (int, int) {
	if rows <= 0 {
		return 0, 0
	}
	if len(s.items) <= rows {
		return 0, len(s.items)
	}
	start := 0
	if s.cursor >= rows {
		start = s.cursor - rows + 1
	}
	return start, start + rows
}

func (s suggest) count(what string) string {
	if len(s.items) == 0 {
		return ui.StyleWarn.Render("no " + what + " match")
	}
	return ui.StyleDim.Render(fmt.Sprintf("%d %s", len(s.items), what))
}

func (s suggest) view(width, rows int) string {
	var b strings.Builder
	start, end := s.window(rows)
	for i := start; i < end; i++ {
		prefix := "  "
		line := ui.Truncate(s.items[i], max(10, width-4))
		if i == s.cursor {
			prefix = ui.StyleCmd.Render("> ")
			line = ui.StyleElement.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	for i := end - start; i < rows; i++ {
		b.WriteString("\n")
	}
	return b.String()
}
