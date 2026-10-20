package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
)

type compareState struct {
	loading bool
	err     string
	at      time.Time
	byView  map[string]dataview.Comparison
}

func (c compareState) ran() bool { return !c.at.IsZero() }

type compareDoneMsg struct {
	eid    string
	err    string
	byView map[string]dataview.Comparison
}

type deviceRunner interface {
	ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error)
}

func (m tuiModel) runnerFor(c *api.Client) deviceRunner {
	if app.Fixture != nil {
		return app.Fixture.SessionClient(app.Defaults.PagerOff, app.Defaults.PagerPrompts)
	}
	return c
}

func (m tuiModel) startCompare() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok || tab.cmp.loading || m.views == nil {
		return m, nil
	}

	views := dataview.CompareViews(m.views.Views, tab.vars())
	if len(views) == 0 {
		m.status = "no view asks this element's CLI anything"
		return m, nil
	}
	c := m.tenantClient(tab.tsg)
	if c == nil {
		m.status = "could not reach " + m.tenantName(tab.tsg)
		return m, nil
	}
	creds := m.credsFor(tab.tsg)
	if creds.User == "" || creds.Pass == "" {
		m.status = "no ION login saved for " + m.tenantName(tab.tsg) + " — alt+u adds one"
		return m, nil
	}

	tab.cmp.loading = true
	tab.cmp.err = ""
	m.data[m.dataActive] = tab
	m.status = ""
	return m, tea.Batch(compareCmd(m.ctx, m.runnerFor(c), c, views, tab, creds), m.spinner.Tick)
}

func compareCmd(ctx context.Context, r deviceRunner, g dataview.Getter, views []dataview.View, t dataTab, creds ionterm.Credentials) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, app.Defaults.ElementTimeout)
		defer cancel()

		msg := compareDoneMsg{eid: t.eid, byView: map[string]dataview.Comparison{}}
		outs, err := r.ToolkitSession(ctx, t.eid, t.name+"#", creds.User, creds.Pass, dataview.CompareCommands(views))
		if err != nil {
			msg.err = err.Error()
			return msg
		}

		out := dataview.DeviceOutput{}
		for _, o := range outs {
			out[o.Command] = o.Output
		}
		for _, v := range views {
			d, err := dataview.FetchCompare(ctx, g, v, t.vars())
			cmp := dataview.RenderCompare(v, d, out)
			if err != nil {
				cmp.Errs = append(cmp.Errs, err.Error())
			}
			msg.byView[v.Name] = cmp
		}
		return msg
	}
}

func (m tuiModel) onCompareDone(msg compareDoneMsg) (tea.Model, tea.Cmd) {
	for i := range m.data {
		if m.data[i].eid != msg.eid {
			continue
		}
		m.data[i].cmp = compareState{err: msg.err, at: time.Now(), byView: msg.byView}
		if msg.err == "" {
			m.status = compareSummary(msg.byView)
		}
		return m, nil
	}
	return m, nil
}

func compareSummary(byView map[string]dataview.Comparison) string {
	var differ, unknown, total int
	for _, cmp := range byView {
		for _, row := range cmp.Rows {
			total++
			switch row.Verdict {
			case dataview.Differ:
				differ++
			case dataview.Unknown:
				unknown++
			}
		}
	}
	switch {
	case total == 0:
		return ""
	case differ > 0:
		return fmt.Sprintf("%d of %d CLI checks disagree with the portal", differ, total)
	case unknown == total:
		return "the device answered none of the CLI checks"
	default:
		return fmt.Sprintf("the device agrees on all %d CLI checks", total-unknown)
	}
}

func (m tuiModel) comparePane(p *dataPage, tab dataTab, view string, width int) {
	if m.views == nil {
		return
	}
	v, ok := m.views.View(view)
	if !ok || len(dataview.CompareViews([]dataview.View{v}, tab.vars())) == 0 {
		return
	}

	head := "  " + ui.StyleCmd.Render("CLI check")
	switch {
	case tab.cmp.loading:
		p.head(head + " " + ui.StyleDim.Render(m.spinner.View()+" asking the device"))
		return
	case tab.cmp.err != "":
		p.head(head)
		p.lines = append(p.lines, "  "+ui.StyleErr.Render(ui.Truncate(tab.cmp.err, width-2)))
		return
	}

	cmp, ok := tab.cmp.byView[view]
	if !ok || !tab.cmp.ran() {
		p.head(head + " " + ui.StyleDim.Render("· c asks the device and compares"))
		return
	}

	detail := "· " + strings.Join(cmp.Commands, " · ")
	if age := ui.Age(time.Since(tab.cmp.at)); age != "" {
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
