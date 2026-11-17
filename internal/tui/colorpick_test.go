package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ui"
)

func pickerOn(t *testing.T, theme, role string) tuiModel {
	t.Helper()
	m := editorOn(t, theme)

	roles := editRoles()
	for i, r := range roles {
		if r == role {
			m.themeRow = i
		}
	}
	if roles[m.themeRow] != role {
		t.Fatalf("there is no %q role to pick for", role)
	}

	next, _ := m.onSettingsKey("enter")
	m = next.(tuiModel)
	if !m.pick.on {
		t.Fatalf("enter on %s did not open the picker", role)
	}
	return m
}

func TestMovingOnTheDiscAppliesTheColorWithoutSavingIt(t *testing.T) {
	m := pickerOn(t, "nord", "ok")
	before := m.themeDraft.OK

	next, _ := m.onSettingsKey("left")
	m = next.(tuiModel)
	if m.themeDraft.OK == before {
		t.Fatalf("ok is still %q after moving on the disc", before)
	}
	if !strings.HasPrefix(m.themeDraft.OK, "#") {
		t.Errorf("the disc gave %q, want a hex", m.themeDraft.OK)
	}
	if got := ui.CurrentTheme().OK; got != m.themeDraft.OK {
		t.Errorf("CurrentTheme().OK = %q, want the color under the cursor %q",
			got, m.themeDraft.OK)
	}

	path := filepath.Join(app.ThemesPath(m.store()), ui.ThemeFileName("nord"))
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), m.themeDraft.OK) {
		t.Errorf("%s already holds %q before enter:\n%s", path, m.themeDraft.OK, b)
	}

	picked := m.themeDraft.OK
	next, _ = m.onSettingsKey("enter")
	m = next.(tuiModel)
	if m.pick.on {
		t.Error("enter did not close the picker")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("enter did not write the scheme's file: %v", err)
	}
	if !strings.Contains(string(b), picked) {
		t.Errorf("%s does not hold the picked %q:\n%s", path, picked, b)
	}
}

func TestEscapingThePickerPutsTheOldColorBack(t *testing.T) {
	m := pickerOn(t, "nord", "warn")
	original := m.themeDraft.Warn

	next, _ := m.onSettingsKey("right")
	m = next.(tuiModel)
	if m.themeDraft.Warn == original {
		t.Fatal("nothing changed, so there is nothing to put back")
	}

	next, _ = m.onSettingsKey("esc")
	m = next.(tuiModel)
	if m.pick.on {
		t.Error("esc did not close the picker")
	}
	if m.themeDraft.Warn != original {
		t.Errorf("warn = %q after esc, want the original %q", m.themeDraft.Warn, original)
	}
	if got := ui.CurrentTheme().Warn; got != original {
		t.Errorf("the frame still shows %q, want %q", got, original)
	}
	if !m.themeEdit {
		t.Error("esc out of the picker left the editor too")
	}
}

func TestTheChartStoresAnIndexRatherThanAHex(t *testing.T) {
	m := pickerOn(t, "nord", "element")

	next, _ := m.onSettingsKey("tab")
	m = next.(tuiModel)
	if !m.pick.grid {
		t.Fatal("tab did not switch to the chart")
	}

	next, _ = m.onSettingsKey("down")
	m = next.(tuiModel)
	next, _ = m.onSettingsKey("right")
	m = next.(tuiModel)

	got := m.themeDraft.Element
	n, err := strconv.Atoi(got)
	if err != nil || n < 0 || n > 255 {
		t.Errorf("element = %q off the chart, want an index 0-255", got)
	}
	if got := ui.CurrentTheme().Element; got != m.themeDraft.Element {
		t.Errorf("the chart color was not applied: frame has %q", got)
	}
}

func TestBrightnessChangesTheColorUnderTheCursor(t *testing.T) {
	m := pickerOn(t, "nord", "cmd")
	next, _ := m.onSettingsKey("left")
	m = next.(tuiModel)
	before := m.themeDraft.Cmd

	next, _ = m.onSettingsKey("-")
	m = next.(tuiModel)
	if m.themeDraft.Cmd == before {
		t.Errorf("cmd is still %q after -", before)
	}
	if m.pick.val >= 1 {
		t.Errorf("brightness = %v, want it below the top", m.pick.val)
	}
}

func TestThePickerDrawsTheDiscAndNamesItsKeys(t *testing.T) {
	m := pickerOn(t, "Ben's Theme", "row")

	out := m.viewSettings()
	if !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Errorf("the disc was not drawn:\n%s", plain(out))
	}
	flat := plain(out)
	for _, want := range []string{"Picking Row", "brightness", "the other one", "Preview · Ben's Theme"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the picker does not say %q:\n%s", want, flat)
		}
	}

	next, _ := m.onSettingsKey("tab")
	m = next.(tuiModel)
	if chart := plain(m.viewSettings()); !strings.Contains(chart, "the cube") {
		t.Errorf("the chart was not drawn:\n%s", chart)
	}
}

func TestTheDiscFitsTheTerminalItIsDrawnIn(t *testing.T) {
	m := pickerOn(t, "nord", "ok")
	for _, h := range []int{24, 30, 44, 60} {
		m.height = h
		cols, rows := m.wheelSize()
		if rows < wheelMinRows || rows > wheelMaxRows {
			t.Errorf("height %d gave %d rows, want %d-%d", h, rows, wheelMinRows, wheelMaxRows)
		}
		if cols < rows {
			t.Errorf("height %d gave %dx%d, which is taller than it is wide", h, cols, rows)
		}
		if lines := len(m.wheelLines()); lines != rows {
			t.Errorf("height %d drew %d lines for %d rows", h, lines, rows)
		}
	}
}

func TestRenderTheColorPicker(t *testing.T) {
	m := pickerOn(t, "Ben's Theme", "element")
	t.Log("\n" + m.viewSettings())
	next, _ := m.onSettingsKey("tab")
	t.Log("\n" + next.(tuiModel).viewSettings())
}

func TestTheFrameCarriesTheSchemesBackground(t *testing.T) {
	pinTheme(t)
	m := settingsModel(t)
	m.width, m.height = 110, 44
	m.setSection, m.setFocus = secTheme, focusContent

	use := func(name string) tuiModel {
		t.Helper()
		for i, th := range ui.Themes() {
			if th.Name == name {
				m.setCursor = i + 1
			}
		}
		next, _ := m.onSettingsKey("enter")
		return next.(tuiModel)
	}

	dark := use("Ben's Theme")
	if got := dark.View().BackgroundColor; got != nil {
		t.Errorf("the background went out before the terminal was asked: %v", got)
	}
	answered, _ := dark.Update(colorProbeDoneMsg{})
	dark = answered.(tuiModel)

	v := dark.View()
	if v.BackgroundColor == nil {
		t.Error("the frame carries no background for a scheme that sets one")
	}
	if v.ForegroundColor == nil {
		t.Error("the frame carries no default text color")
	}

	plainTheme := use("classic")
	if got := plainTheme.View().BackgroundColor; got != nil {
		t.Errorf("classic set the terminal background to %v, want it left alone", got)
	}
}

func TestTheBackgroundIsPickedLikeAnyOtherRole(t *testing.T) {
	probeAnswered(t)
	m := pickerOn(t, "nord", "background")

	next, _ := m.onSettingsKey("left")
	m = next.(tuiModel)
	if m.themeDraft.Bg == "" {
		t.Fatal("the disc did not set a background")
	}
	if got := ui.CurrentTheme().Bg; got != m.themeDraft.Bg {
		t.Errorf("CurrentTheme().Bg = %q, want the picked %q", got, m.themeDraft.Bg)
	}
	if m.View().BackgroundColor == nil {
		t.Error("the picked background never reached the frame")
	}

	next, _ = m.onSettingsKey("esc")
	m = next.(tuiModel)
	out := plain(m.viewSettings())
	for _, want := range []string{"Background", "Other"} {
		if !strings.Contains(out, want) {
			t.Errorf("the editor does not list %q:\n%s", want, out)
		}
	}
}

func TestSwitchingViewsDoesNotChangeTheColor(t *testing.T) {
	m := pickerOn(t, "Ben's Theme", "element")
	before := m.themeDraft.Element

	next, _ := m.onSettingsKey("tab")
	m = next.(tuiModel)
	if m.themeDraft.Element != before {
		t.Errorf("tab changed element from %q to %q", before, m.themeDraft.Element)
	}

	chart := ui.Grid256()
	idx := chart[m.pick.row][m.pick.col]
	if idx < 16 {
		t.Errorf("the chart cursor landed on %d, want it near the hex it came from", idx)
	}
	want, _ := ui.RGBOf(before)
	got, _ := ui.RGBOf(strconv.Itoa(idx))
	for i := range 3 {
		if d := int(want[i]) - int(got[i]); d > 40 || d < -40 {
			t.Errorf("the chart cursor is on %d (%v), which is nowhere near %q (%v)",
				idx, got, before, want)
			break
		}
	}

	next, _ = m.onSettingsKey("tab")
	m = next.(tuiModel)
	if m.pick.grid {
		t.Error("tab did not go back to the disc")
	}
	if m.themeDraft.Element != before {
		t.Errorf("going back changed element to %q", m.themeDraft.Element)
	}
}

func probeAnswered(t *testing.T) {
	t.Helper()
	before := termColor
	t.Cleanup(func() { termColor = before })
	termColor.probed = true
}

func TestTheTerminalsOwnColorsAreSetRatherThanReset(t *testing.T) {
	pinTheme(t)
	before := termColor
	t.Cleanup(func() { termColor = before })

	termColor = termColorState{}
	if bg, fg := frameColors(); bg != nil || fg != nil {
		t.Errorf("frameColors() = %v/%v before the terminal answered", bg, fg)
	}

	rememberTermColor(colorOf(0x12, 0x34, 0x56), colorOf(0xab, 0xcd, 0xef))
	if termColor.hexBg != "#123456" || termColor.hexFg != "#abcdef" {
		t.Fatalf("the report was not kept: %q / %q", termColor.hexBg, termColor.hexFg)
	}
	if !termColor.probed {
		t.Error("both halves answered, so the frame should stop holding its colors")
	}

	if ok := ui.UseTheme("classic"); !ok {
		t.Fatal("classic is not in the set")
	}
	bg, _ := frameColors()
	if got := hexOfColor(bg); got != "#123456" {
		t.Errorf("on classic the frame carries %q, want the terminal's own #123456", got)
	}

	if ok := ui.UseTheme("Ben's Theme"); !ok {
		t.Fatal("Ben's Theme is not in the set")
	}
	bg, _ = frameColors()
	if got := hexOfColor(bg); got != "#1e1e2e" {
		t.Errorf("on Ben's Theme the frame carries %q, want the scheme's own", got)
	}

	var buf strings.Builder
	restoreTermColor(&buf)
	if got := buf.String(); got != "\x1b]11;#123456\x07\x1b]10;#abcdef\x07" {
		t.Errorf("restore wrote %q", got)
	}

	rememberTermColor(colorOf(0, 0, 0), nil)
	if termColor.hexBg != "#123456" {
		t.Errorf("bg = %q, want the color from before iontk set one", termColor.hexBg)
	}

	termColor = termColorState{probed: true}
	ui.UseTheme("classic")
	buf.Reset()
	if bg, fg := frameColors(); bg != nil || fg != nil {
		t.Errorf("frameColors() = %v/%v with no answer and a bare scheme", bg, fg)
	}
	restoreTermColor(&buf)
	if got := buf.String(); got != "" {
		t.Errorf("restore wrote %q with no answer to go on", got)
	}
}

func TestTheDebugPageSaysWhatTheTerminalReported(t *testing.T) {
	before := termColor
	t.Cleanup(func() { termColor = before })

	termColor = termColorState{}
	if got := termColorLine(); !strings.Contains(got, "asking") {
		t.Errorf("before an answer the line reads %q", got)
	}
	termColor = termColorState{probed: true}
	if got := termColorLine(); !strings.Contains(got, "did not answer") {
		t.Errorf("with no answer the line reads %q", got)
	}
	termColor = termColorState{probed: true, hexBg: "#1a1b26", hexFg: "#c0caf5"}
	if got := termColorLine(); !strings.Contains(got, "#1a1b26") || !strings.Contains(got, "#c0caf5") {
		t.Errorf("the line reads %q, want both colors", got)
	}
}

func colorOf(r, g, b uint8) color.Color {
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}
