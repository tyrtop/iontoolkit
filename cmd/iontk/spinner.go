package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

type spinner struct {
	mu     sync.Mutex
	w      io.Writer
	waits  func() []runtime.Wait
	frames []string
	width  int
	label  string
	start  time.Time
	shown  bool
	quit   chan struct{}
	done   chan struct{}
}

func newSpinner(w io.Writer, waits func() []runtime.Wait, frames []string, width int, label string) *spinner {
	return &spinner{w: w, waits: waits, frames: frames, width: width, label: label, start: time.Now(),
		quit: make(chan struct{}), done: make(chan struct{})}
}

func startSpinner(rt *runtime.Runtime, label string) *spinner {
	width, _, err := term.GetSize(int(os.Stderr.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	m := ui.NewSpinner(ui.CurrentTheme().Spinner)
	s := newSpinner(os.Stderr, rt.Waits, m.Spinner.Frames, width, label)
	go s.loop(time.NewTicker(m.Spinner.FPS))
	return s
}

func (s *spinner) loop(t *time.Ticker) {
	defer close(s.done)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-s.quit:
			s.mu.Lock()
			s.clear()
			s.mu.Unlock()
			return
		case now := <-t.C:
			s.draw(i, now)
		}
	}
}

func (s *spinner) draw(i int, now time.Time) {
	label := runtime.Wait{What: s.label, Since: s.start}.Line(now)
	if waits := s.waits(); len(waits) > 0 {
		label = waits[0].Line(now)
		if len(waits) > 1 {
			label += fmt.Sprintf(" (and %d more)", len(waits)-1)
		}
	}
	frame := ""
	if len(s.frames) > 0 {
		frame = s.frames[i%len(s.frames)]
	}
	rest := ui.Truncate(" "+label, max(0, s.width-1-ui.Width(frame)))
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprint(s.w, "\r\x1b[K"+ui.StyleSpinner.Render(frame)+ui.StyleDim.Render(rest))
	s.shown = true
}

func (s *spinner) clear() {
	if s.shown {
		fmt.Fprint(s.w, "\r\x1b[K")
		s.shown = false
	}
}

func (s *spinner) println(w io.Writer, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	fmt.Fprintln(w, line)
}

func (s *spinner) stop() {
	close(s.quit)
	<-s.done
}
