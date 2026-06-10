package chart

import (
	"fmt"
	"html"
	"math"
	"strings"

	"finador/internal/perf"
)

// Line is one curve of an SVG chart. Label is escaped before rendering;
// Color is written verbatim and must stay a trusted constant.
type Line struct {
	Label  string
	Color  string
	Points []perf.Point
}

const (
	padL, padR, padT, padB = 64, 16, 18, 34
)

// SVG renders self-contained markup: inline attributes only, no CSS, no JS.
// The first line gets a light area fill; the scale covers every line.
func SVG(lines []Line, w, h int) string {
	// dimensions utilisateur : plancher pour éviter un rendu illisible
	w = max(w, 320)
	h = max(h, 120)
	lines = nonEmpty(lines)
	if len(lines) == 0 {
		return ""
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, l := range lines {
		llo, lhi := bounds(l.Points)
		lo, hi = math.Min(lo, llo), math.Max(hi, lhi)
	}
	if hi == lo {
		hi = lo + 1
	}
	plotW, plotH := float64(w-padL-padR), float64(h-padT-padB)
	x := func(i, n int) float64 { return float64(padL) + float64(i)/float64(max(n-1, 1))*plotW }
	y := func(v float64) float64 { return float64(padT) + (hi-v)/(hi-lo)*plotH }
	f := func(v float64) string { return fmt.Sprintf("%.1f", v) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" font-family="ui-monospace,monospace" font-size="11">`+"\n", w, h)
	// grille + étiquettes d'échelle
	for i := range 4 {
		gv := lo + (hi-lo)*float64(3-i)/3
		gy := y(gv)
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%d" y2="%s" stroke="#c9c0ad" stroke-width="1"/>`+"\n",
			f(float64(padL)), f(gy), w-padR, f(gy))
		fmt.Fprintf(&b, `<text x="%d" y="%s" text-anchor="end" fill="#666666">%s</text>`+"\n",
			padL-6, f(gy+4), formatCompact(gv))
	}
	// aire sous la première série
	first := lines[0]
	var area strings.Builder
	for i, p := range first.Points {
		fmt.Fprintf(&area, "%s,%s ", f(x(i, len(first.Points))), f(y(p.Value)))
	}
	fmt.Fprintf(&b, `<polygon points="%s%s,%s %s,%s" fill="%s" fill-opacity="0.07"/>`+"\n",
		area.String(),
		f(x(len(first.Points)-1, len(first.Points))), f(float64(padT)+plotH),
		f(x(0, len(first.Points))), f(float64(padT)+plotH),
		first.Color)
	// courbes
	for _, l := range lines {
		var pts strings.Builder
		for i, p := range l.Points {
			fmt.Fprintf(&pts, "%s,%s ", f(x(i, len(l.Points))), f(y(p.Value)))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="1.8"/>`+"\n",
			strings.TrimSpace(pts.String()), l.Color)
	}
	// légende + dates
	lx := padL
	for _, l := range lines {
		fmt.Fprintf(&b, `<rect x="%d" y="4" width="10" height="3" fill="%s"/><text x="%d" y="10" fill="#444444">%s</text>`+"\n",
			lx, l.Color, lx+14, html.EscapeString(l.Label))
		lx += 14 + 8*len(l.Label) + 16
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666666">%s</text>`+"\n", padL, h-8, first.Points[0].Date)
	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="end" fill="#666666">%s</text>`+"\n",
		w-padR, h-8, first.Points[len(first.Points)-1].Date)
	b.WriteString("</svg>\n")
	return b.String()
}

func nonEmpty(lines []Line) []Line {
	var out []Line
	for _, l := range lines {
		if len(l.Points) > 0 {
			out = append(out, l)
		}
	}
	return out
}
