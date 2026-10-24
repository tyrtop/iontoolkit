package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/ui"
)

type nilSink struct{}

func (nilSink) Add(int, sase.Result) error { return nil }
func (nilSink) Close() error               { return nil }

func batchAfter(t *testing.T, total, width, height int, evs []sase.Event) batchModel {
	t.Helper()
	m := newBatchModel(nil, []string{"dump interface status all"}, total, nilSink{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(batchModel)
	for _, ev := range evs {
		next, _ := m.Update(eventMsg(ev))
		m = next.(batchModel)
	}
	return m
}

func done(i int, name string, err string) sase.Event {
	return sase.Event{Index: i, Element: name, Done: true,
		Result: sase.Result{ElementID: name, Name: name, Error: err}}
}

func frameLines(m batchModel) []string {
	return strings.Split(ui.Plain(m.View().Content), "\n")
}

func lineOf(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func TestTheBannerStaysAboveTheElementsThatFinished(t *testing.T) {
	m := batchAfter(t, 3, 80, 24, []sase.Event{
		done(0, "S0224-RTR-01", ""),
		done(1, "S0272-RTR-01", ""),
	})

	lines := frameLines(m)
	top := strings.Split(ui.Plain(ui.Banner(80, 24)), "\n")[0]
	banner := lineOf(lines, top)
	bar := lineOf(lines, "2/3")
	first := lineOf(lines, "S0224-RTR-01")
	second := lineOf(lines, "S0272-RTR-01")

	if banner < 0 || bar < 0 || first < 0 || second < 0 {
		t.Fatalf("frame is missing a row: banner=%d bar=%d first=%d second=%d\n%s",
			banner, bar, first, second, m.View().Content)
	}
	if banner != 0 {
		t.Errorf("banner on row %d, want the top row", banner)
	}
	if bar > first {
		t.Errorf("bar on row %d is below the first element on row %d", bar, first)
	}
	if first > second {
		t.Errorf("rows arrive out of order: %q on %d, %q on %d",
			"S0224-RTR-01", first, "S0272-RTR-01", second)
	}
	if !m.View().AltScreen {
		t.Error("the frame must hold the altscreen, or the banner scrolls away")
	}
}

func TestTheFrameKeepsTheTerminalHeightWhateverHasFinished(t *testing.T) {
	const height = 30
	for _, n := range []int{0, 1, 12, 400} {
		evs := make([]sase.Event, 0, n)
		for i := range n {
			evs = append(evs, done(i, fmt.Sprintf("S%04d-RTR-01", i), ""))
		}
		m := batchAfter(t, max(n, 1), 80, height, evs)
		if got := len(frameLines(m)); got != height {
			t.Errorf("%d finished: frame is %d rows, want %d", n, got, height)
		}
	}
}

func TestTheScrollKeepsTheNewestWhenItOverflows(t *testing.T) {
	const total = 200
	evs := make([]sase.Event, 0, total)
	for i := range total {
		evs = append(evs, done(i, fmt.Sprintf("S%04d-RTR-01", i), ""))
	}
	m := batchAfter(t, total, 80, 24, evs)

	lines := frameLines(m)
	if lineOf(lines, "S0199-RTR-01") < 0 {
		t.Errorf("the element that just finished is not on the frame:\n%s", m.View().Content)
	}
	if at := lineOf(lines, "S0000-RTR-01"); at >= 0 {
		t.Errorf("the first element is still on row %d of a 24-row frame", at)
	}
}

func TestAFailedElementIsOnTheScrollAndInTheSummary(t *testing.T) {
	m := batchAfter(t, 2, 80, 24, []sase.Event{
		done(0, "S0224-RTR-01", "element 1: dial failed"),
		done(1, "S0272-RTR-01", ""),
	})

	if at := lineOf(frameLines(m), "S0224-RTR-01"); at < 0 {
		t.Errorf("the element that failed is not on the frame:\n%s", m.View().Content)
	}
	summary := ui.Plain(strings.Join(m.summaryLines(), "\n"))
	if !strings.Contains(summary, "1 of 2 elements failed") {
		t.Errorf("summary = %q, want the failure count", summary)
	}
	if !strings.Contains(summary, "dial failed") {
		t.Errorf("summary = %q, want the reason", summary)
	}
}
