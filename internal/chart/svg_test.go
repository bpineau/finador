package chart

import (
	"strings"
	"testing"
	"time"
)

func intradayRamp(n int) []TimePoint {
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	pts := make([]TimePoint, n)
	for i := range pts {
		pts[i] = TimePoint{Time: base.Add(time.Duration(i) * 5 * time.Minute), Value: float64(100 + i)}
	}
	return pts
}

func TestSVGStructure(t *testing.T) {
	gross := Line{Label: "brut", Color: "#0a7d4b", Points: ramp(60)}
	net := Line{Label: "net", Color: "#888888", Points: ramp(60)}
	out := SVG([]Line{gross, net}, 800, 300)

	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"`, `viewBox="0 0 800 300"`,
		`<polyline`, `stroke="#0a7d4b"`, `stroke="#888888"`,
		"2026-01-01", "2026-03-01", // dates at the corners
		"159", "100", // scale labels (max/min)
		"brut", "net", // legend
		"</svg>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q absent du SVG:\n%s", want, out[:min(len(out), 600)])
		}
	}
	if strings.Count(out, "<polyline") != 2 {
		t.Errorf("polylines = %d, attendu 2", strings.Count(out, "<polyline"))
	}
	// no NaN or Inf in the coordinates
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s dans le SVG", bad)
		}
	}
}

func TestSVGEmpty(t *testing.T) {
	if out := SVG(nil, 800, 300); out != "" {
		t.Errorf("SVG vide: %q", out)
	}
	if out := SVG([]Line{{Label: "x", Points: nil}}, 800, 300); out != "" {
		t.Errorf("SVG sans points: %q", out)
	}
}

func TestSVGDeterministic(t *testing.T) {
	lines := []Line{{Label: "brut", Color: "#0a7d4b", Points: ramp(30)}}
	a := SVG(lines, 800, 300)
	b := SVG(lines, 800, 300)
	if a != b {
		t.Error("sortie SVG non déterministe")
	}
}

func TestIntradaySVGStructure(t *testing.T) {
	pts := intradayRamp(20)
	out := IntradaySVG(pts, 800, 300, "price EUR", "#1c1914")
	// HH:MM labels
	first := pts[0].Time.Format("15:04")
	last := pts[len(pts)-1].Time.Format("15:04")
	if !strings.Contains(out, first) {
		t.Errorf("first time label %q missing:\n%s", first, out[:min(len(out), 400)])
	}
	if !strings.Contains(out, last) {
		t.Errorf("last time label %q missing:\n%s", last, out[:min(len(out), 400)])
	}
	// legend
	if !strings.Contains(out, "price EUR") {
		t.Errorf("legend missing:\n%s", out[:min(len(out), 400)])
	}
	// polyline present
	if !strings.Contains(out, "<polyline") {
		t.Errorf("polyline missing")
	}
	// no date strings (no YYYY-MM-DD)
	if strings.Contains(out, "2026-") {
		t.Errorf("date string must not appear in intraday SVG")
	}
	// no NaN/Inf
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s in intraday SVG", bad)
		}
	}
}

func TestIntradaySVGEmpty(t *testing.T) {
	if out := IntradaySVG(nil, 800, 300, "price EUR", "#000"); out != "" {
		t.Errorf("empty: %q", out)
	}
	if out := IntradaySVG(intradayRamp(1), 800, 300, "price EUR", "#000"); out != "" {
		t.Errorf("single point: %q", out)
	}
}

func TestSVGEscapesLabels(t *testing.T) {
	out := SVG([]Line{{Label: "a<b>&c", Color: "#000000", Points: ramp(3)}}, 800, 300)
	if strings.Contains(out, "a<b>") {
		t.Error("label non échappé dans le SVG")
	}
	if !strings.Contains(out, "a&lt;b&gt;&amp;c") {
		t.Errorf("échappement attendu absent:\n%s", out)
	}
}
