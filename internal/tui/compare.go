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

// compareState is a tab's device half. It is tab-wide rather than per pane
// because every view's commands run in one session, and keyed by view for
// rendering, because each view asks its own questions.
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

// deviceRunner is the CLI half of a comparison. ToolkitSession is what a batch
// run uses, so this inherits the dial retry and the pager handling instead of
// growing a second copy of either.
type deviceRunner interface {
	ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error)
}

// runnerFor is fixture mode's one substitution, the same place dialerFor makes
// it: the playback device, reached through the real ioncli code, so autologin
// and the pager scraper are exercised rather than skipped.
func (m tuiModel) runnerFor(c *api.Client) deviceRunner {
	if app.Fixture != nil {
		return app.Fixture.SessionClient(app.Defaults.PagerOff, app.Defaults.PagerPrompts)
	}
	return c
}

// startCompare is the explicit keypress. The rest of rich-data mode asks for
// no device login at all, which is the point of the mode; this one action
// dials the appliance and logs in, so it never happens on its own.
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

// compareCmd runs the device commands in one session, then reads the portal
// side. The portal is fetched again rather than taken off the page: a page can
// be minutes old, and a comparison between two different moments is not one.
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

// compareSummary is what the footer says once a comparison lands, because the
// marks that matter may be further down the page than the window reaches.
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

// comparePane is the device half under a view's own sections, so a reading and
// its check are read together. Its heading is a jump target like any other.
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

// compareLines lays each row out as portal then device with a mark. Padded
// plain and styled afterwards: padding a styled cell counts its escape bytes
// toward the width and the columns drift apart row by row.
func (m tuiModel) compareLines(cmp dataview.Comparison, width int) []string {
	colw := max(10, (width-ui.LabelWidth-8)/2)
	cell := func(s string, w int) string { return fmt.Sprintf("%-*s", w, ui.Truncate(s, w)) }

	// Four columns of gutter: the mark and its space sit where the header has
	// nothing to say.
	out := []string{ui.StyleDim.Render(fmt.Sprintf("    %s  %s  %s",
		cell("CHECK", ui.LabelWidth), cell("PORTAL", colw), "DEVICE"))}
	for _, row := range cmp.Rows {
		mark, style := compareMark(row.Verdict)
		// Only the device cell takes the verdict's colour: it is the answer
		// the mark is about, and colouring both makes a row shout twice.
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
	// Nothing to compare, which is not a disagreement.
	return ui.StyleDim.Render("·"), ui.StyleDim
}

func dashed(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
