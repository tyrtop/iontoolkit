package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/sase"
)

var (
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleCmd     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleElement = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleDim     = lipgloss.NewStyle().Faint(true)
	// The nearest thing to orange in the 0-15 range, for a session about to go.
	styleHot = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	markCheck = styleOK.SetString("✓")
	markCross = styleErr.SetString("✗")
)

func drainLive(ctx context.Context, events <-chan sase.Event, commands []string, total int, sink resultSink) (int, int, error) {
	opts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithOutput(os.Stderr),
		tea.WithoutSignalHandler(),
	}
	if flagElementsPath == "-" || !term.IsTerminal(int(os.Stdin.Fd())) {
		opts = append(opts, tea.WithInput(nil))
	}

	final, err := tea.NewProgram(newBatchModel(events, commands, total, sink), opts...).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return 0, 0, err
	}

	fm := final.(batchModel)
	return total, fm.failed, fm.sinkErr
}

type inflight struct {
	element string
	started time.Time
}

type errGroup struct {
	msg   string
	count int
	first string
}

type batchModel struct {
	events   <-chan sase.Event
	commands []string
	total    int

	sink      resultSink
	sinkErr   error
	finished  int
	failed    int
	running   []inflight
	errGroups []errGroup

	spinner  spinner.Model
	progress progress.Model
	width    int
	height   int
	done     bool
}

type eventMsg sase.Event
type streamClosedMsg struct{}

func newBatchModel(events <-chan sase.Event, commands []string, total int, sink resultSink) batchModel {
	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	return batchModel{
		events:   events,
		commands: commands,
		total:    total,
		sink:     sink,
		spinner:  s,
		progress: progress.New(
			progress.WithDefaultBlend(),
			progress.WithWidth(30),
			progress.WithoutPercentage(),
		),
		width:  80,
		height: 24,
	}
}

func (m batchModel) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), m.spinner.Tick)
}

func waitForEvent(ch <-chan sase.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return eventMsg(ev)
	}
}

func (m batchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
		return m, nil

	case eventMsg:
		ev := sase.Event(msg)
		if !ev.Done {
			m.running = append(m.running, inflight{element: ev.Element, started: time.Now()})
			return m, waitForEvent(m.events)
		}

		if err := m.sink.Add(ev.Index, ev.Result); err != nil && m.sinkErr == nil {
			m.sinkErr = fmt.Errorf("write result: %w", err)
		}
		m.finished++
		for i, f := range m.running {
			if f.element == ev.Element {
				m.running = append(m.running[:i], m.running[i+1:]...)
				break
			}
		}

		cmds := []tea.Cmd{
			m.progress.SetPercent(float64(m.finished) / float64(m.total)),
			waitForEvent(m.events),
		}

		if ev.Result.Error != "" {
			m.failed++
			m.recordError(ev.Element, ev.Result.Error)
		} else {
			name := ev.Result.Name
			if name == "" {
				name = ev.Element
			}
			cmds = append(cmds, tea.Printf("%s %s  %s",
				markCheck, styleElement.Render(name), styleDim.Render(short(ev.Elapsed))))
		}

		return m, tea.Batch(cmds...)

	case streamClosedMsg:
		m.done = true
		return m, tea.Sequence(append(m.summaryCmds(), tea.Quit)...)

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

func (m *batchModel) recordError(element, msg string) {
	key := normalizeErr(msg)
	for i := range m.errGroups {
		if m.errGroups[i].msg == key {
			m.errGroups[i].count++
			return
		}
	}
	m.errGroups = append(m.errGroups, errGroup{msg: key, count: 1, first: element})
}

func (m batchModel) summaryCmds() []tea.Cmd {
	if m.failed == 0 {
		return []tea.Cmd{tea.Printf("%s %d/%d elements succeeded", markCheck, m.finished, m.total)}
	}
	cmds := []tea.Cmd{tea.Printf("%s %d of %d elements failed", markCross, m.failed, m.total)}
	for _, g := range m.errGroups {
		cmds = append(cmds, tea.Printf("    %d × %s %s",
			g.count, g.msg, styleDim.Render("(first: "+g.first+")")))
	}
	return cmds
}

func (m batchModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}

	var b strings.Builder

	b.WriteString(banner(m.width, m.height))
	b.WriteString(styleCmd.Render(strings.Join(m.commands, ", ")) + "\n")

	counts := fmt.Sprintf("  %d/%d", m.finished, m.total)
	if m.failed > 0 {
		counts += styleErr.Render(fmt.Sprintf("  %d failed", m.failed))
	}
	b.WriteString(m.spinner.View() + " " + m.progress.View() + counts + "\n")

	avail := max(2, m.height-6-bannerHeight(m.width, m.height))
	errShown := min(len(m.errGroups), max(1, avail/2))
	flightShown := min(len(m.running), max(1, avail-errShown))

	if errShown > 0 {
		b.WriteString("\n")
		for _, g := range m.errGroups[:errShown] {
			b.WriteString(fmt.Sprintf("%s %d × %s %s\n",
				markCross, g.count, g.msg,
				styleDim.Render("(first: "+g.first+")")))
		}
		if n := len(m.errGroups) - errShown; n > 0 {
			b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more error kinds\n", n)))
		}
	}

	if flightShown > 0 {
		b.WriteString("\n")
		for _, f := range m.running[:flightShown] {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				styleElement.Render(f.element),
				styleDim.Render(short(time.Since(f.started)))))
		}
		if n := len(m.running) - flightShown; n > 0 {
			b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more\n", n)))
		}
	}

	return tea.NewView(b.String())
}

func normalizeErr(s string) string {
	if strings.HasPrefix(s, "element ") {
		if i := strings.Index(s, ": "); i >= 0 {
			return s[i+2:]
		}
	}
	return s
}

func short(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}
