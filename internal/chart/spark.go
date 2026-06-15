package chart

import (
	"fmt"
	"math"
	"strings"

	"finador/internal/perf"
)

// Sparkline renders a bare inline curve: one polyline, no axes, no labels.
// Returns "" when there is nothing to draw (fewer than 2 points).
func Sparkline(points []perf.Point, w, h int, color string) string {
	if len(points) < 2 {
		return ""
	}
	w, h = max(w, 20), max(h, 8)
	lo, hi := bounds(points)
	if hi == lo {
		hi = lo + 1 // flat series: horizontal line in the middle
	}
	pad := 2.0
	x := func(i int) float64 {
		return pad + float64(i)/float64(len(points)-1)*(float64(w)-2*pad)
	}
	y := func(v float64) float64 {
		return pad + (hi-v)/(hi-lo)*(float64(h)-2*pad)
	}
	var pts strings.Builder
	for i, p := range points {
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		fmt.Fprintf(&pts, "%.1f,%.1f ", x(i), y(p.Value))
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" preserveAspectRatio="none">`+
			`<polyline points="%s" fill="none" stroke="%s" stroke-width="1.3"/></svg>`,
		w, h, w, h, strings.TrimSpace(pts.String()), color)
}
