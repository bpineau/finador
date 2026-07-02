package chart

import (
	"strings"
	"testing"
	"time"

	"finador/internal/domain"
	"finador/internal/perf"
)

func pts(vals ...float64) []perf.Point {
	out := make([]perf.Point, len(vals))
	d := domain.Date{Year: 2026, Month: 1, Day: 1}
	for i, v := range vals {
		out[i] = perf.Point{Date: d.AddDays(i), Value: v}
	}
	return out
}

func TestSVGMinimalDialect(t *testing.T) {
	svg := SVG([]Line{
		{Label: "gross", Color: "#1c1914", Points: pts(100, 105, 103, 110)},
		{Label: "net", Color: "#1e6e4e", Points: pts(100, 104, 102, 108)},
	}, 860, 280)
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not a standalone svg: %.60q", svg)
	}
	if strings.Contains(svg, "<rect width=") {
		t.Error("finador charts have no background rect")
	}
	if !strings.Contains(svg, "fill-opacity") {
		t.Error("the first curve should carry an area fill")
	}
	if !strings.Contains(svg, ">net</text>") {
		t.Error("multi-line charts keep a legend")
	}
	if !strings.Contains(svg, "2026-01-01") {
		t.Error("corner dates missing")
	}
	if SVG(nil, 860, 280) != "" {
		t.Error("no line, no chart")
	}
}

func TestIntradaySVGShowsClockTimes(t *testing.T) {
	base := domain.Date{Year: 2026, Month: 6, Day: 1}.Time().Add(9 * time.Hour)
	points := make([]TimePoint, 12)
	for i := range points {
		points[i] = TimePoint{Time: base.Add(time.Duration(i) * 5 * time.Minute), Value: 100 + float64(i%3)}
	}
	svg := IntradaySVG(points, 860, 280, "price EUR", "#1c1914")
	if !strings.Contains(svg, "09:00") {
		t.Errorf("intraday charts label clock times:\n%.400s", svg)
	}
	if IntradaySVG(points[:1], 860, 280, "x", "#000") != "" {
		t.Error("a single tick is not a curve")
	}
}

func TestSparkline(t *testing.T) {
	svg := Sparkline(pts(1, 3, 2), 72, 20, "#1c1914")
	if !strings.Contains(svg, "<polyline") || strings.Contains(svg, "<text") {
		t.Fatalf("sparklines are bare polylines: %q", svg)
	}
	if Sparkline(pts(1), 72, 20, "#1c1914") != "" {
		t.Error("fewer than 2 points yields nothing")
	}
}

func TestPieBareDonut(t *testing.T) {
	svg := Pie([]float64{3, 1}, []string{"#111111", "#222222"}, 190)
	if strings.Count(svg, "<path") != 2 {
		t.Fatalf("wedges: %q", svg)
	}
	if strings.Contains(svg, "<text") {
		t.Error("the legend is HTML, not SVG")
	}
	if !strings.Contains(svg, "#111111") {
		t.Error("caller colors are honored")
	}
	if Pie([]float64{0, -1}, nil, 190) != "" {
		t.Error("nothing positive, no donut")
	}
}

func TestBraille(t *testing.T) {
	out := Braille(pts(100, 101, 102, 103, 104, 105, 110, 120, 115, 118), 40, 6)
	braille := false
	for _, r := range out {
		if r > 0x2800 && r <= 0x28FF {
			braille = true
		}
	}
	if !braille {
		t.Fatalf("no braille runes:\n%s", out)
	}
	if !strings.Contains(out, "2026") {
		t.Error("the date axis is part of the chart")
	}
	if Braille(nil, 40, 6) != "" {
		t.Error("no points, no chart")
	}
}
