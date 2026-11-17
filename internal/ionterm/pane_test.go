package ionterm

import (
	"strconv"
	"strings"
	"testing"
)

func TestSessionWindowUsesDialedBufferNotPane(t *testing.T) {
	d := &fakeDialer{conn: newFakeConn(fixtureFrames(t))}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(contextTODO(), d, "123", "x", 200, 50, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if cols, rows := s.Size(); cols != 200 || rows != 50 {
		t.Fatalf("dialed %dx%d, want 200x50", cols, rows)
	}

	eventually(t, "output", func() bool {
		return strings.Contains(plain(s.Render()), "DPDK Controlled")
	})

	win := s.Window(40, 10)
	if n := len(strings.Split(win, "\n")); n != 10 {
		t.Errorf("window has %d rows, want exactly 10", n)
	}
	for _, ln := range strings.Split(win, "\n") {
		if w := plainWidth(ln); w > 40 {
			t.Errorf("line %d cols wide, want at most 40: %q", w, ln)
		}
	}
}

func TestRenderIsAlwaysConstantHeight(t *testing.T) {
	for _, screen := range []string{"", "one", "a\nb\nc", strings.Repeat("x\n", 80)} {
		v := Render(screen, 0, 0, 40, 12)
		if n := len(strings.Split(v.Content, "\n")); n != 12 {
			t.Errorf("screen %q rendered %d lines, want 12", screen[:min(8, len(screen))], n)
		}
	}
}

func TestRenderReportsCursorInsideThePane(t *testing.T) {
	var rows []string
	for i := 0; i < 60; i++ {
		rows = append(rows, "row"+strconv.Itoa(i))
	}
	screen := strings.Join(rows, "\n")

	v := Render(screen, 3, 59, 20, 10)
	if v.CursorY != 9 {
		t.Errorf("CursorY = %d, want 9 (last pane row)", v.CursorY)
	}
	if v.CursorX != 3 {
		t.Errorf("CursorX = %d, want 3", v.CursorX)
	}

	wide := Render("0123456789abcdefghij", 18, 0, 8, 3)
	if wide.CursorX != 7 {
		t.Errorf("scrolled CursorX = %d, want 7 (right edge)", wide.CursorX)
	}
	if wide.CursorY != 0 {
		t.Errorf("scrolled CursorY = %d, want 0", wide.CursorY)
	}
}

func TestRenderCursorStaysInBounds(t *testing.T) {
	v := Render("x", 999, 999, 10, 4)
	if v.CursorX < 0 || v.CursorX > 9 {
		t.Errorf("CursorX = %d, out of a 10-wide pane", v.CursorX)
	}
	if v.CursorY < 0 || v.CursorY > 3 {
		t.Errorf("CursorY = %d, out of a 4-tall pane", v.CursorY)
	}
}
