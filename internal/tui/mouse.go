package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"tyrtop.com/iontk/internal/ionterm"
)

const (
	wheelStep = 3
	paneTop   = 2
)

var selStyle = lipgloss.NewStyle().Reverse(true)

type selection struct {
	eid      string
	from, to ionterm.Spot
	dragging bool
}

func (m tuiModel) mouseMode() tea.MouseMode {
	if m.phase == phaseSession && m.frontSession() != nil {
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

func (m tuiModel) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	s := m.frontSession()
	if m.phase != phaseSession || s == nil || m.favPick || m.slash != slashOff {
		return m, nil
	}
	cols, rows := m.paneSize()
	at := msg.Mouse()

	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.sesScroll += wheelStep
		case tea.MouseWheelDown:
			m.sesScroll -= wheelStep
		}
		m.sesScroll = min(max(0, m.sesScroll), s.MaxScroll(rows))

	case tea.MouseClickMsg:
		m.sel = selection{}
		if msg.Button != tea.MouseLeft || at.Y < paneTop || at.Y >= paneTop+rows {
			return m, nil
		}
		spot := m.spotAt(s, cols, rows, at.X, at.Y)
		m.sel = selection{eid: s.ElementID, from: spot, to: spot, dragging: true}

	case tea.MouseMotionMsg:
		if !m.sel.dragging {
			return m, nil
		}
		switch {
		case at.Y < paneTop:
			m.sesScroll = min(m.sesScroll+1, s.MaxScroll(rows))
		case at.Y >= paneTop+rows:
			m.sesScroll = max(0, m.sesScroll-1)
		}
		m.sel.to = m.spotAt(s, cols, rows, at.X, at.Y)

	case tea.MouseReleaseMsg:
		if !m.sel.dragging {
			return m, nil
		}
		m.sel.dragging = false
		m.sel.to = m.spotAt(s, cols, rows, at.X, at.Y)
		if m.sel.from == m.sel.to {
			m.sel = selection{}
			return m, nil
		}
		text := s.Copy(m.sel.from, m.sel.to)
		m.status = "copied " + plural(strings.Count(text, "\n")+1, "line")
		return m, tea.SetClipboard(text)
	}
	return m, nil
}

func (m tuiModel) spotAt(s *ionterm.Session, cols, rows, x, y int) ionterm.Spot {
	view := s.ScrolledView(cols, rows, m.sesScroll)
	row := min(max(0, y-paneTop), rows-1)
	return ionterm.Spot{Line: view.Top + row, Col: view.Left + min(max(0, x), cols-1)}
}

func (m tuiModel) paintSelection(eid string, view ionterm.View, cols int) string {
	if m.sel.eid != eid || m.sel.from == m.sel.to {
		return view.Content
	}
	a, b := m.sel.from, m.sel.to
	if b.Before(a) {
		a, b = b, a
	}
	rows := strings.Split(view.Content, "\n")
	for i, row := range rows {
		line := view.Top + i
		if line < a.Line || line > b.Line {
			continue
		}
		left, right := 0, cols
		if line == a.Line {
			left = a.Col - view.Left
		}
		if line == b.Line {
			right = b.Col - view.Left + 1
		}
		left, right = max(0, left), min(cols, right)
		if left >= right {
			continue
		}
		if w := ansi.StringWidth(row); w < right {
			row += strings.Repeat(" ", right-w)
		}
		mid := ansi.Strip(ansi.Cut(row, left, right))
		rows[i] = ansi.Cut(row, 0, left) + ansi.ResetStyle + selStyle.Render(mid) + ansi.Cut(row, right, ansi.StringWidth(row))
	}
	return strings.Join(rows, "\n")
}
