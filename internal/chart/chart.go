// Package chart renders value curves in the finador dialect: braille for
// the terminal, minimal SVG for the web. All rendering is pofo's chart
// package; this façade owns the house style (bare backgrounds, monospace,
// area fill, corner dates, the muted pie palette) and the mapping from
// finador's domain-typed series.
package chart

import (
	"time"

	"finador/internal/perf"

	pofochart "github.com/bpineau/pofo/pkg/chart"
)

// TimePoint is one tick in an intraday chart (time-stamped, not date-stamped).
type TimePoint struct {
	Time  time.Time
	Value float64
}

// Line is one curve of an SVG chart. Label is escaped by the renderer;
// Color is written verbatim and must stay a trusted constant.
type Line struct {
	Label  string
	Color  string
	Points []perf.Point
}

// PiePalette is the muted, paper-friendly palette of the allocation donut.
// Handlers cycle through it and reuse the same colors in the HTML legend.
// No ink-black here: slices sort by weight, and a dominant near-black wedge
// reads as a rendering bug on paper.
var PiePalette = []string{
	"#1e6e4e", "#9a6b1f", "#a3332e", "#48657a",
	"#7d5a4f", "#6e5a7e", "#5d564a", "#8a8466",
}

// style is the finador SVG dialect: pofo's minimal preset.
func style() pofochart.Style { return pofochart.StyleMinimal() }

// SVG renders self-contained markup: no CSS, no JS, no background. The
// first line gets a light area fill; a single-line chart shows its label
// as a small title, several lines share a legend row.
func SVG(lines []Line, w, h int) string {
	series := make([]pofochart.Series, 0, len(lines))
	for _, l := range lines {
		if len(l.Points) == 0 {
			continue
		}
		dates := make([]time.Time, len(l.Points))
		values := make([]float64, len(l.Points))
		for i, p := range l.Points {
			dates[i] = p.Date.Time()
			values[i] = p.Value
		}
		series = append(series, pofochart.Series{Name: l.Label, Dates: dates, Values: values, Color: l.Color})
	}
	if len(series) == 0 {
		return ""
	}
	opt := pofochart.Options{Width: w, Height: h, Style: style()}
	if len(series) == 1 {
		opt.Title = series[0].Name
	}
	return pofochart.Line(opt, series)
}

// IntradaySVG renders a single intraday price curve; the x-axis labels
// show HH:MM of the first and last point rather than dates.
func IntradaySVG(points []TimePoint, w, h int, label, color string) string {
	if len(points) < 2 {
		return ""
	}
	dates := make([]time.Time, len(points))
	values := make([]float64, len(points))
	for i, p := range points {
		dates[i] = p.Time
		values[i] = p.Value
	}
	opt := pofochart.Options{Width: w, Height: h, Title: label, Style: style()}
	return pofochart.Line(opt, []pofochart.Series{{Name: label, Dates: dates, Values: values, Color: color}})
}

// Sparkline renders a bare inline curve: one polyline, no axes, no labels.
// Returns "" when there is nothing to draw (fewer than 2 points).
func Sparkline(points []perf.Point, w, h int, color string) string {
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}
	return pofochart.Sparkline(pofochart.SparkOptions{Width: w, Height: h, Color: color}, values)
}

// Pie renders a bare donut: one wedge per positive value, colors supplied
// by the caller (cycled from PiePalette by the handler; the legend is
// HTML). Returns "" when nothing is positive.
func Pie(values []float64, colors []string, size int) string {
	slices := make([]pofochart.Slice, len(values))
	for i, v := range values {
		s := pofochart.Slice{Value: v}
		if i < len(colors) {
			s.Color = colors[i]
		} else {
			s.Color = PiePalette[i%len(PiePalette)]
		}
		slices[i] = s
	}
	return pofochart.Pie(pofochart.PieOptions{Width: size, Hole: 0.55, HideLegend: true}, slices)
}

// Braille draws points as a width-column braille chart (each cell holds
// 2x4 dots) with value labels in the gutter and a time axis underneath.
func Braille(points []perf.Point, width, height int) string {
	if len(points) == 0 {
		return ""
	}
	dates := make([]time.Time, len(points))
	values := make([]float64, len(points))
	for i, p := range points {
		dates[i] = p.Date.Time()
		values[i] = p.Value
	}
	// pofo's Term width includes its 10-column label gutter; finador's
	// historical convention counted plot columns only.
	const gutter = 10
	return pofochart.Term(pofochart.TermOptions{Width: width + gutter, Height: height, Braille: true},
		[]pofochart.Series{{Dates: dates, Values: values}})
}
