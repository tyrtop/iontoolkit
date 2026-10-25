package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type compareDoneMsg struct {
	check runtime.Check
	err   error
}

func (m tuiModel) startCompare() (tea.Model, tea.Cmd) {
	m.status = ""
	rt, ctx := m.runtime(), m.ctx
	return m, tea.Batch(func() tea.Msg {
		c := &runtime.Call{Ctx: ctx}
		err := rt.Exec(c, "data.check")
		done, _ := c.Result.(runtime.Check)
		return compareDoneMsg{check: done, err: err}
	}, m.spinner.Tick)
}

func (m tuiModel) onCompareDone(msg compareDoneMsg) (tea.Model, tea.Cmd) {
	switch {
	case errors.Is(msg.err, runtime.ErrNoLogin):
		m.status = msg.err.Error() + " — alt+u adds one"
	case msg.err != nil:
		m.status = msg.err.Error()
	default:
		m.status = runtime.CheckSummary(msg.check.ByView)
	}
	return m, nil
}

func (m tuiModel) comparePane(p *dataPage, tab dataTab, view string, width int) {
	set := m.runtime().Views()
	if set == nil {
		return
	}
	v, ok := set.View(view)
	if !ok || len(dataview.CompareViews([]dataview.View{v}, tab.Vars())) == 0 {
		return
	}

	head := "  " + ui.StyleCmd.Render("CLI check")
	switch {
	case tab.Check.Loading:
		p.head(head + " " + ui.StyleDim.Render(m.spinner.View()+" asking the device"))
		return
	case tab.Check.Err != "":
		p.head(head)
		p.lines = append(p.lines, "  "+ui.StyleErr.Render(ui.Truncate(tab.Check.Err, width-2)))
		return
	}

	cmp, ok := tab.Check.ByView[view]
	if !ok || !tab.Check.Ran() {
		p.head(head + " " + ui.StyleDim.Render("· c asks the device and compares"))
		return
	}

	detail := "· " + strings.Join(cmp.Commands, " · ")
	if age := ui.Age(time.Since(tab.Check.At)); age != "" {
		detail += " · " + age + " old"
	}
	p.head(head + " " + ui.StyleDim.Render(ui.Truncate(detail, max(10, width-14))))
	p.lines = append(p.lines, m.compareLines(cmp, width)...)
}

func (m tuiModel) compareLines(cmp dataview.Comparison, width int) []string {
	colw := max(10, (width-ui.LabelWidth-8)/2)
	cell := func(s string, w int) string { return fmt.Sprintf("%-*s", w, ui.Truncate(s, w)) }

	out := []string{ui.StyleDim.Render(fmt.Sprintf("    %s  %s  %s",
		cell("CHECK", ui.LabelWidth), cell("PORTAL", colw), "DEVICE"))}
	for _, row := range cmp.Rows {
		mark, style := compareMark(row.Verdict)
		out = append(out, fmt.Sprintf("  %s %s  %s  %s", mark,
			ui.StyleDim.Render(cell(row.Label, ui.LabelWidth)),
			cell(dashed(row.Portal), colw),
			style.Render(ui.Truncate(dashed(row.Device), colw))))
	}
	for _, e := range cmp.Errs {
		out = append(out, "  "+ui.StyleWarn.Render(ui.Truncate(e, width-2)))
	}
	return out
}

func compareMark(v dataview.Verdict) (string, lipgloss.Style) {
	switch v {
	case dataview.Agree:
		return ui.MarkCheck.String(), ui.StyleOK
	case dataview.Differ:
		return ui.MarkCross.String(), ui.StyleErr
	}
	return ui.StyleDim.Render("·"), ui.StyleDim
}

func dashed(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
