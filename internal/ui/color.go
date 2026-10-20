package ui

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
)

var base16 = [16][3]uint8{
	{0x00, 0x00, 0x00}, {0x80, 0x00, 0x00}, {0x00, 0x80, 0x00}, {0x80, 0x80, 0x00},
	{0x00, 0x00, 0x80}, {0x80, 0x00, 0x80}, {0x00, 0x80, 0x80}, {0xc0, 0xc0, 0xc0},
	{0x80, 0x80, 0x80}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
	{0x00, 0x00, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
}

var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

func RGBOf(spec string) ([3]uint8, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return [3]uint8{}, false
	}
	if strings.HasPrefix(spec, "#") {
		h := spec[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return [3]uint8{}, false
		}
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return [3]uint8{}, false
		}
		return [3]uint8{uint8(n >> 16), uint8(n >> 8 & 0xff), uint8(n & 0xff)}, true
	}

	n, err := strconv.Atoi(spec)
	if err != nil || n < 0 || n > 255 {
		return [3]uint8{}, false
	}
	switch {
	case n < 16:
		return base16[n], true
	case n < 232:
		i := n - 16
		return [3]uint8{cubeLevels[i/36], cubeLevels[i/6%6], cubeLevels[i%6]}, true
	default:
		g := uint8(8 + 10*(n-232))
		return [3]uint8{g, g, g}, true
	}
}

func HexOf(spec string) string {
	c, ok := RGBOf(spec)
	if !ok {
		return ""
	}
	return HexFromRGB(c)
}

func HexFromRGB(c [3]uint8) string {
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

func Contrast(spec string) string {
	c, ok := RGBOf(spec)
	if !ok {
		return "15"
	}
	y := 0.299*float64(c[0]) + 0.587*float64(c[1]) + 0.114*float64(c[2])
	if y > 140 {
		return "#000000"
	}
	return "#ffffff"
}

func HSVOf(c [3]uint8) (h, s, v float64) {
	r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
	maxc := math.Max(r, math.Max(g, b))
	minc := math.Min(r, math.Min(g, b))
	v = maxc
	d := maxc - minc
	if maxc > 0 {
		s = d / maxc
	}
	if d == 0 {
		return 0, s, v
	}
	switch maxc {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, v
}

func RGBFromHSV(h, s, v float64) [3]uint8 {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	s = clamp01(s)
	v = clamp01(v)

	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c

	var r, g, b float64
	switch int(h / 60) {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return [3]uint8{round8(r + m), round8(g + m), round8(b + m)}
}

func HexFromHSV(h, s, v float64) string { return HexFromRGB(RGBFromHSV(h, s, v)) }

func clamp01(f float64) float64 { return math.Max(0, math.Min(1, f)) }

func round8(f float64) uint8 { return uint8(math.Round(clamp01(f) * 255)) }

func WheelAt(x, y, cols, rows int, value float64) string {
	nx, ny, ok := wheelNorm(x, y, cols, rows)
	if !ok {
		return ""
	}
	r := math.Hypot(nx, ny)
	if r > 1+rimSlack {
		return ""
	}
	h := math.Atan2(ny, nx) * 180 / math.Pi
	return HexFromHSV(h, math.Min(1, r), value)
}

const rimSlack = 0.05

func wheelNorm(x, y, cols, rows int) (nx, ny float64, ok bool) {
	if cols < 3 || rows < 2 {
		return 0, 0, false
	}
	pixels := rows * 2
	cx, cy := float64(cols-1)/2, float64(pixels-1)/2
	if x < 0 || x >= cols || y < 0 || y >= pixels {
		return 0, 0, false
	}
	return (float64(x) - cx) / cx, (float64(y) - cy) / cy, true
}

func WheelCell(spec string, cols, rows int) (x, y int, value float64) {
	c, ok := RGBOf(spec)
	pixels := rows * 2
	cx, cy := float64(cols-1)/2, float64(pixels-1)/2
	if !ok {
		return int(math.Round(cx)), int(math.Round(cy)), 1
	}
	h, s, v := HSVOf(c)
	if v == 0 {
		return int(math.Round(cx)), int(math.Round(cy)), 0
	}
	rad := h * math.Pi / 180
	x = int(math.Round(cx + s*math.Cos(rad)*cx))
	y = int(math.Round(cy + s*math.Sin(rad)*cy))
	return min(max(x, 0), cols-1), min(max(y, 0), pixels-1), v
}

func Grid256() [][]int {
	out := make([][]int, 0, 8)

	first := make([]int, 16)
	for i := range first {
		first[i] = i
	}
	out = append(out, first)

	for r := range 6 {
		row := make([]int, 36)
		for i := range row {
			row[i] = 16 + r*36 + i
		}
		out = append(out, row)
	}

	greys := make([]int, 24)
	for i := range greys {
		greys[i] = 232 + i
	}
	return append(out, greys)
}

func NearestIndex(spec string) int {
	c, ok := RGBOf(spec)
	if !ok {
		return 7
	}
	best, bestDist := 16, math.MaxFloat64
	for n := 16; n < 256; n++ {
		o, _ := RGBOf(strconv.Itoa(n))
		dr := float64(c[0]) - float64(o[0])
		dg := float64(c[1]) - float64(o[1])
		db := float64(c[2]) - float64(o[2])
		if d := dr*dr + dg*dg + db*db; d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

func GridCell(spec string) (row, col int, ok bool) {
	n, err := strconv.Atoi(strings.TrimSpace(spec))
	if err != nil || n < 0 || n > 255 {
		return 0, 0, false
	}
	for r, cells := range Grid256() {
		for c, v := range cells {
			if v == n {
				return r, c, true
			}
		}
	}
	return 0, 0, false
}

func RandomColor(onLight bool) string {
	h := rand.Float64() * 360
	s := 0.45 + rand.Float64()*0.5
	v := 0.62 + rand.Float64()*0.38
	if onLight {
		v = 0.28 + rand.Float64()*0.26
		s = 0.5 + rand.Float64()*0.5
	}
	return HexFromHSV(h, s, v)
}

func RandomBackground() (bg, fg string, onLight bool) {
	h := rand.Float64() * 360
	onLight = rand.IntN(4) == 0
	if onLight {
		return HexFromHSV(h, 0.04+rand.Float64()*0.1, 0.9+rand.Float64()*0.08),
			HexFromHSV(h, 0.1+rand.Float64()*0.2, 0.16+rand.Float64()*0.1), true
	}
	return HexFromHSV(h, 0.1+rand.Float64()*0.25, 0.07+rand.Float64()*0.09),
		HexFromHSV(h, 0.05+rand.Float64()*0.15, 0.82+rand.Float64()*0.14), false
}

func SimilarColor(spec string, spread float64) string {
	c, ok := RGBOf(spec)
	if !ok {
		return spec
	}
	h, s, v := HSVOf(c)
	h += jitter(28 * spread)
	s = clamp01(s + jitter(0.18*spread))
	v = clamp01(v + jitter(0.14*spread))

	if v < 0.12 {
		v = 0.12
	}
	return HexFromHSV(h, s, v)
}

func SimilarBackground(spec string, spread float64) string {
	c, ok := RGBOf(spec)
	if !ok {
		return spec
	}
	h, s, v := HSVOf(c)
	h += jitter(24 * spread)
	s = clamp01(s + jitter(0.08*spread))
	v = clamp01(v + jitter(0.05*spread))
	if orig := v; orig > 0.5 {
		v = math.Max(0.84, v)
	} else {
		v = math.Min(0.22, v)
	}
	return HexFromHSV(h, s, v)
}

func jitter(by float64) float64 { return (rand.Float64()*2 - 1) * by }

func RandomSpinner() string {
	names := SpinnerNames()
	return names[rand.IntN(len(names))]
}
