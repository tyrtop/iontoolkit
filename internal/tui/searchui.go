package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"tyrtop.com/iontk/internal/runtime"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

func DrainTenants(ctx context.Context, events <-chan runtime.TenantLoad, total int) {
	opts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithOutput(os.Stderr),
		tea.WithoutSignalHandler(),
		tea.WithInput(nil),
	}
	if _, err := tea.NewProgram(newLoadModel(events, total), opts...).Run(); err != nil &&
		!errors.Is(err, tea.ErrProgramKilled) {
		for range events {
		}
	}
}

type loadModel struct {
	events <-chan runtime.TenantLoad
	total  int

	started  []inflight
	finished int
	failed   int

	spinner  spinner.Model
	progress progress.Model
	width    int
	height   int
	done     bool
}

type loadMsg runtime.TenantLoad
type loadClosedMsg struct{}

func newLoadModel(events <-chan runtime.TenantLoad, total int) loadModel {
	s := spinner.New()
	s.Style = ui.StyleSpinner
	return loadModel{
		events:  events,
		total:   total,
		spinner: s,
		progress: progress.New(
			progress.WithDefaultBlend(),
			progress.WithWidth(30),
			progress.WithoutPercentage(),
		),
		width:  80,
		height: 24,
	}
}

func (m loadModel) Init() tea.Cmd {
	return tea.Batch(waitForLoad(m.events), m.spinner.Tick)
}

func waitForLoad(ch <-chan runtime.TenantLoad) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return loadClosedMsg{}
		}
		return loadMsg(ev)
	}
}

func (m loadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case loadMsg:
		ev := runtime.TenantLoad(msg)
		if !ev.Done {
			m.started = append(m.started, inflight{element: ev.Tenant, started: time.Now()})
			return m, waitForLoad(m.events)
		}

		m.finished++
		for i, f := range m.started {
			if f.element == ev.Tenant {
				m.started = append(m.started[:i], m.started[i+1:]...)
				break
			}
		}

		cmds := []tea.Cmd{
			m.progress.SetPercent(float64(m.finished) / float64(m.total)),
			waitForLoad(m.events),
		}
		if ev.Err != nil {
			m.failed++
			cmds = append(cmds, tea.Printf("%s %s  %s",
				ui.MarkCross, ui.StyleElement.Render(ev.Tenant),
				ui.StyleErr.Render(app.ShortAuthError(ev.Err))))
		} else {
			mark, note := ui.MarkCheck.String(), ui.StyleDim.Render
			if ev.Warn {
				mark, note = ui.StyleWarn.Render("!"), ui.StyleWarn.Render
			}
			cmds = append(cmds, tea.Printf("%s %s  %s",
				mark, ui.StyleElement.Render(ev.Tenant),
				note(fmt.Sprintf("%d elements %s", ev.Count, ev.Note))))
		}
		return m, tea.Batch(cmds...)

	case loadClosedMsg:
		m.done = true
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m loadModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}

	var b strings.Builder
	counts := fmt.Sprintf("  %d/%s", m.finished, plural(m.total, "tenant"))
	if m.failed > 0 {
		counts += ui.StyleErr.Render(fmt.Sprintf("  %d unreachable", m.failed))
	}
	b.WriteString(m.spinner.View() + " " + m.progress.View() + counts + "\n")

	for _, f := range m.started {
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			ui.StyleElement.Render(f.element),
			ui.StyleDim.Render(short(time.Since(f.started)))))
	}
	return tea.NewView(b.String())
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
