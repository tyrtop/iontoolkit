package ionterm

import (
	"strconv"
	"strings"
	"testing"
)

func TestWindowShowsLastRows(t *testing.T) {
	screen := "r0\nr1\nr2\nr3\nr4\nr5"
	got := Window(screen, 0, 5, 10, 3)
	want := "r3\nr4\nr5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWindowFitsWithoutClipping(t *testing.T) {
	got := Window("a\nb", 0, 1, 10, 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5 (padded to pane height)", len(lines))
	}
	if lines[0] != "a" || lines[1] != "b" {
		t.Errorf("content = %q, want a/b at the top", got)
	}
}

func TestWindowTruncatesWideLines(t *testing.T) {
	screen := "0123456789abcdef"
	got := Window(screen, 0, 0, 8, 1)
	if got != "01234567" {
		t.Errorf("got %q, want the first 8 columns", got)
	}
}

func TestWindowFollowsCursorHorizontally(t *testing.T) {
	screen := "0123456789abcdef"
	got := Window(screen, 15, 0, 8, 1)
	if !strings.HasSuffix(got, "f") {
		t.Errorf("got %q, want the window to include the cursor column", got)
	}
	if n := len(got); n > 8 {
		t.Errorf("window is %d wide, want 8", n)
	}
}

func TestWindowPreservesStylesWhileClipping(t *testing.T) {
	screen := "\x1b[31mRED-TEXT-THAT-IS-LONG\x1b[0m"
	got := Window(screen, 0, 0, 7, 1)
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("got %q, want the color sequence preserved", got)
	}
	if !strings.Contains(got, "RED") {
		t.Errorf("got %q, want visible text", got)
	}
}

func TestWindowZeroSize(t *testing.T) {
	if got := Window("x", 0, 0, 0, 5); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := Window("x", 0, 0, 5, 0); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

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

func TestWindowPadsRatherThanScrollingAMostlyEmptyBuffer(t *testing.T) {
	content := []string{"login:", "Password:", "ion1# dump x", "out1", "out2", "ion1#"}
	screen := strings.Join(content, "\n") + strings.Repeat("\n", 44)

	got := Window(screen, 5, 5, 80, 27)
	lines := strings.Split(got, "\n")

	if len(lines) != 27 {
		t.Fatalf("got %d lines, want a constant 27", len(lines))
	}
	if lines[0] != "login:" {
		t.Errorf("first visible line = %q, want the top of the content", lines[0])
	}
	if !strings.Contains(got, "out2") {
		t.Error("content missing from the window")
	}
	for i := len(content); i < len(lines); i++ {
		if !blank(lines[i]) {
			t.Errorf("line %d should be blank padding, got %q", i, lines[i])
		}
	}
}

func TestWindowKeepsCursorVisibleWhenScrolled(t *testing.T) {
	var rows []string
	for i := 0; i < 60; i++ {
		rows = append(rows, "row"+strconv.Itoa(i))
	}
	screen := strings.Join(rows, "\n")

	got := Window(screen, 0, 59, 20, 10)
	lines := strings.Split(got, "\n")

	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	if lines[9] != "row59" {
		t.Errorf("last visible line = %q, want row59 (the cursor row)", lines[9])
	}
	if lines[0] != "row50" {
		t.Errorf("first visible line = %q, want row50", lines[0])
	}
}

func TestWindowShowsContentAboveCursorNotBelow(t *testing.T) {
	var rows []string
	for i := 0; i < 50; i++ {
		rows = append(rows, "row"+strconv.Itoa(i))
	}
	screen := strings.Join(rows, "\n")

	got := Window(screen, 0, 18, 20, 27)
	lines := strings.Split(got, "\n")

	if lines[0] != "row0" {
		t.Errorf("first line = %q; a cursor inside the first screenful must not scroll the view", lines[0])
	}
	if len(lines) != 27 {
		t.Errorf("got %d lines, want a constant 27", len(lines))
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
