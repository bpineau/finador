package chart

import (
	"fmt"
	"math"
	"strings"
)

// PiePalette is the muted, paper-friendly palette of the allocation donut.
// Handlers cycle through it and reuse the same colors in the HTML legend.
// No ink-black here: slices sort by weight, and a dominant near-black wedge
// reads as a rendering bug on paper.
var PiePalette = []string{
	"#1e6e4e", "#9a6b1f", "#a3332e", "#48657a",
	"#7d5a4f", "#6e5a7e", "#5d564a", "#8a8466",
}

// Pie renders a donut: one path per positive value, colors supplied by the
// caller (cycled by the handler from PiePalette). No labels — the legend is
// HTML. Returns "" when nothing is positive.
func Pie(values []float64, colors []string, size int) string {
	total := 0.0
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	if total == 0 {
		return ""
	}
	cx, cy := float64(size)/2, float64(size)/2
	rOut := float64(size)/2 - 2
	rIn := rOut * 0.55

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d">`, size, size, size, size)
	angle := -math.Pi / 2 // départ à midi
	for i, v := range values {
		if v <= 0 {
			continue
		}
		frac := v / total
		color := PiePalette[i%len(PiePalette)]
		if i < len(colors) {
			color = colors[i]
		}
		if frac >= 0.9999 { // anneau complet : deux demi-anneaux pour éviter l'arc dégénéré
			fmt.Fprintf(&b, `<path d="%s" fill="%s"/>`, ringHalf(cx, cy, rOut, rIn, angle, angle+math.Pi), color)
			fmt.Fprintf(&b, `<path d="%s" fill="%s"/>`, ringHalf(cx, cy, rOut, rIn, angle+math.Pi, angle+2*math.Pi), color)
			break
		}
		a1 := angle + frac*2*math.Pi
		fmt.Fprintf(&b, `<path d="%s" fill="%s"/>`, ringHalf(cx, cy, rOut, rIn, angle, a1), color)
		angle = a1
	}
	b.WriteString("</svg>")
	return b.String()
}

// coord formats one SVG coordinate.
func coord(v float64) string { return fmt.Sprintf("%.2f", v) }

// ringHalf builds one donut sector path between two angles.
func ringHalf(cx, cy, rOut, rIn, a0, a1 float64) string {
	laf := "0"
	if a1-a0 > math.Pi {
		laf = "1"
	}
	f := coord
	x0o, y0o := cx+rOut*math.Cos(a0), cy+rOut*math.Sin(a0)
	x1o, y1o := cx+rOut*math.Cos(a1), cy+rOut*math.Sin(a1)
	x1i, y1i := cx+rIn*math.Cos(a1), cy+rIn*math.Sin(a1)
	x0i, y0i := cx+rIn*math.Cos(a0), cy+rIn*math.Sin(a0)
	return "M" + f(x0o) + "," + f(y0o) +
		" A" + f(rOut) + "," + f(rOut) + " 0 " + laf + " 1 " + f(x1o) + "," + f(y1o) +
		" L" + f(x1i) + "," + f(y1i) +
		" A" + f(rIn) + "," + f(rIn) + " 0 " + laf + " 0 " + f(x0i) + "," + f(y0i) + " Z"
}
