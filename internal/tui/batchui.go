package tui

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
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/ui"
)

const (
	completedKept = 300
	inflightRows  = 5
	errKindRows   = 3
)

func DrainLive(ctx context.Context, events <-chan sase.Event, commands []string, total int, sink output.Sink, stdinTaken bool) (int, int, error) {
	opts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithOutput(os.Stderr),
		tea.WithoutSignalHandler(),
	}
	if stdinTaken || !term.IsTerminal(int(os.Stdin.Fd())) {
		opts = append(opts, tea.WithInput(nil))
	}

	final, err := tea.NewProgram(newBatchModel(events, commands, total, sink), opts...).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return 0, 0, err
	}

	fm := final.(batchModel)
	// bubbletea drops tea.Printf while the altscreen is up
	for _, line := range fm.summaryLines() {
		fmt.Fprintln(os.Stderr, line)
	}
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

	sink      output.Sink
	sinkErr   error
	finished  int
	failed    int
	running   []inflight
	completed []string
	errGroups []errGroup

	spinner  spinner.Model
	progress progress.Model
	width    int
	height   int
	done     bool
}

type eventMsg sase.Event
type streamClosedMsg struct{}

func newBatchModel(events <-chan sase.Event, commands []string, total int, sink output.Sink) batchModel {
	s := spinner.New()
	s.Style = ui.StyleSpinner
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

		name := ev.Result.Name
		if name == "" {
			name = ev.Element
		}
		if ev.Result.Error != "" {
			m.failed++
			m.recordError(ev.Element, ev.Result.Error)
			m.record(fmt.Sprintf("%s %s  %s", ui.MarkCross,
				ui.StyleElement.Render(name), ui.StyleErr.Render(normalizeErr(ev.Result.Error))))
		} else {
			m.record(fmt.Sprintf("%s %s  %s", ui.MarkCheck,
				ui.StyleElement.Render(name), ui.StyleDim.Render(short(ev.Elapsed))))
		}

		return m, tea.Batch(
			m.progress.SetPercent(float64(m.finished)/float64(m.total)),
			waitForEvent(m.events),
		)

	case streamClosedMsg:
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

func (m *batchModel) record(line string) {
	m.completed = append(m.completed, line)
	if len(m.completed) > completedKept {
		m.completed = m.completed[len(m.completed)-completedKept:]
	}
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

func (m batchModel) summaryLines() []string {
	if m.failed == 0 {
		return []string{fmt.Sprintf("%s %d/%d elements succeeded", ui.MarkCheck, m.finished, m.total)}
	}
	lines := []string{fmt.Sprintf("%s %d of %d elements failed", ui.MarkCross, m.failed, m.total)}
	for _, g := range m.errGroups {
		lines = append(lines, fmt.Sprintf("    %d × %s %s",
			g.count, g.msg, ui.StyleDim.Render("(first: "+g.first+")")))
	}
	return lines
}

func (m batchModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}

	head := m.head()
	lines := append(head, m.scroll(m.height-len(head))...)
	for i, l := range lines {
		lines[i] = ui.Truncate(l, m.width)
	}

	v := tea.NewView(ui.ClipFrame(strings.Join(lines, "\n"), m.height))
	v.AltScreen = true
	return v
}

func (m batchModel) head() []string {
	lines := strings.Split(strings.TrimRight(ui.Banner(m.width, m.height), "\n"), "\n")
	lines = append(lines, ui.StyleCmd.Render(strings.Join(m.commands, ", ")))

	counts := fmt.Sprintf("  %d/%d", m.finished, m.total)
	if m.failed > 0 {
		counts += ui.StyleErr.Render(fmt.Sprintf("  %d failed", m.failed))
	}
	lines = append(lines, m.spinner.View()+" "+m.progress.View()+counts, "")

	if shown := min(len(m.errGroups), errKindRows); shown > 0 {
		for i, g := range m.errGroups[:shown] {
			if i == shown-1 && len(m.errGroups) > shown {
				lines = append(lines, ui.StyleDim.Render(
					fmt.Sprintf("  … %d more error kinds", len(m.errGroups)-shown+1)))
				break
			}
			lines = append(lines, fmt.Sprintf("%s %d × %s %s", ui.MarkCross, g.count, g.msg,
				ui.StyleDim.Render("(first: "+g.first+")")))
		}
		lines = append(lines, "")
	}

	for i := range inflightRows {
		if i == inflightRows-1 && len(m.running) > inflightRows {
			lines = append(lines, ui.StyleDim.Render(
				fmt.Sprintf("  … %d more in flight", len(m.running)-inflightRows+1)))
			break
		}
		if i < len(m.running) {
			f := m.running[i]
			lines = append(lines, "  "+ui.StyleElement.Render(f.element)+"  "+
				ui.StyleDim.Render(short(time.Since(f.started))))
			continue
		}
		lines = append(lines, "")
	}
	return append(lines, "")
}

func (m batchModel) scroll(n int) []string {
	if n <= 0 {
		return nil
	}
	shown := m.completed
	if len(shown) > n {
		shown = shown[len(shown)-n:]
	}
	out := make([]string, n)
	copy(out, shown)
	return out
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
