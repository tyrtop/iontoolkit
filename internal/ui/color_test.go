package ui

import (
	"math"
	"testing"
)

func TestRGBOfEveryFormAThemeCanHold(t *testing.T) {
	tests := []struct {
		name, spec string
		want       [3]uint8
		ok         bool
	}{
		{"six hex digits", "#89b4fa", [3]uint8{0x89, 0xb4, 0xfa}, true},
		{"three are doubled", "#abc", [3]uint8{0xaa, 0xbb, 0xcc}, true},
		{"an index under sixteen", "9", base16[9], true},
		{"a cube index", "16", [3]uint8{0, 0, 0}, true},
		{"the far corner of the cube", "231", [3]uint8{255, 255, 255}, true},
		{"the first grey", "232", [3]uint8{8, 8, 8}, true},
		{"the last grey", "255", [3]uint8{238, 238, 238}, true},
		{"unset", "", [3]uint8{}, false},
		{"a word", "chartreuse", [3]uint8{}, false},
		{"past the end", "256", [3]uint8{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RGBOf(tc.spec)
			if ok != tc.ok {
				t.Fatalf("RGBOf(%q) ok = %v, want %v", tc.spec, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("RGBOf(%q) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestTheCubeIndexMatchesItsComponents(t *testing.T) {
	got, ok := RGBOf("111")
	if !ok {
		t.Fatal("111 did not resolve")
	}
	want := [3]uint8{cubeLevels[2], cubeLevels[3], cubeLevels[5]}
	if got != want {
		t.Errorf("RGBOf(111) = %v, want %v", got, want)
	}
}

func TestHSVRoundTrips(t *testing.T) {
	for _, spec := range []string{"#89b4fa", "#ff0000", "#00ff00", "#1e1e2e", "#ffffff", "#808080"} {
		c, _ := RGBOf(spec)
		h, s, v := HSVOf(c)
		back := RGBFromHSV(h, s, v)
		for i := range 3 {
			if d := int(back[i]) - int(c[i]); d > 1 || d < -1 {
				t.Errorf("%s went to h=%.1f s=%.2f v=%.2f and came back %v", spec, h, s, v, back)
				break
			}
		}
	}
}

func TestGreyHasNoSaturation(t *testing.T) {
	c, _ := RGBOf("#808080")
	_, s, v := HSVOf(c)
	if s != 0 {
		t.Errorf("saturation = %v for a grey, want 0", s)
	}
	if v < 0.4 || v > 0.6 {
		t.Errorf("value = %v for mid grey", v)
	}
}

func TestTheWheelIsADisc(t *testing.T) {
	const cols, rows = 22, 11

	if got := WheelAt(0, 0, cols, rows, 1); got != "" {
		t.Errorf("the top left corner is %q, want nothing: it is outside the disc", got)
	}

	cx, cy := (cols-1)/2, (rows*2-1)/2
	mid := WheelAt(cx, cy, cols, rows, 1)
	c, ok := RGBOf(mid)
	if !ok {
		t.Fatalf("the middle of the disc is %q", mid)
	}
	if _, s, _ := HSVOf(c); s > 0.2 {
		t.Errorf("the middle has saturation %v, want near white", s)
	}

	right := WheelAt(cols-1, cy, cols, rows, 1)
	rc, ok := RGBOf(right)
	if !ok {
		t.Fatalf("the right edge is %q", right)
	}
	h, s, _ := HSVOf(rc)
	if s < 0.5 {
		t.Errorf("the rim has saturation %v, want it saturated", s)
	}
	if h > 20 && h < 340 {
		t.Errorf("the hue to the right is %v degrees, want red", h)
	}

	dim := WheelAt(cx+2, cy, cols, rows, 0.3)
	bright := WheelAt(cx+2, cy, cols, rows, 1)
	if dim == bright {
		t.Error("the brightness did not change the color")
	}
}

func TestWheelCellFindsTheColorItWasGiven(t *testing.T) {
	const cols, rows = 22, 11
	for _, spec := range []string{"#ff0000", "#00ff00", "#0000ff", "#89b4fa"} {
		x, y, v := WheelCell(spec, cols, rows)
		at := WheelAt(x, y, cols, rows, v)
		c, ok := RGBOf(at)
		if !ok {
			t.Errorf("WheelCell(%s) pointed at %q, which is off the disc", spec, at)
			continue
		}
		want, _ := RGBOf(spec)
		wh, _, _ := HSVOf(want)
		gh, _, _ := HSVOf(c)
		if d := math.Abs(wh - gh); d > 25 && d < 335 {
			t.Errorf("WheelCell(%s) landed on hue %.0f, want near %.0f", spec, gh, wh)
		}
	}
}

func TestTheChartHoldsEveryIndexOnce(t *testing.T) {
	seen := map[int]bool{}
	for _, row := range Grid256() {
		for _, idx := range row {
			if seen[idx] {
				t.Fatalf("index %d is in the chart twice", idx)
			}
			seen[idx] = true
		}
	}
	if len(seen) != 256 {
		t.Errorf("the chart holds %d colors, want 256", len(seen))
	}

	row, col, ok := GridCell("111")
	if !ok {
		t.Fatal("111 is not in the chart")
	}
	if got := Grid256()[row][col]; got != 111 {
		t.Errorf("GridCell(111) points at %d", got)
	}
	if _, _, ok := GridCell("#89b4fa"); ok {
		t.Error("a hex was found in the chart, which only holds indices")
	}
}

func TestContrastIsReadableOnTheSwatch(t *testing.T) {
	if got := Contrast("#ffffff"); got != "#000000" {
		t.Errorf("Contrast(white) = %q, want black", got)
	}
	if got := Contrast("#1e1e2e"); got != "#ffffff" {
		t.Errorf("Contrast(a dark blue) = %q, want white", got)
	}
}
