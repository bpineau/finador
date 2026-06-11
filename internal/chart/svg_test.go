package chart

import (
	"strings"
	"testing"
)

func TestSVGStructure(t *testing.T) {
	gross := Line{Label: "brut", Color: "#0a7d4b", Points: ramp(60)}
	net := Line{Label: "net", Color: "#888888", Points: ramp(60)}
	out := SVG([]Line{gross, net}, 800, 300)

	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"`, `viewBox="0 0 800 300"`,
		`<polyline`, `stroke="#0a7d4b"`, `stroke="#888888"`,
		"2026-01-01", "2026-03-01", // dates aux coins
		"159", "100", // étiquettes d'échelle (max/min)
		"brut", "net", // légende
		"</svg>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q absent du SVG:\n%s", want, out[:min(len(out), 600)])
		}
	}
	if strings.Count(out, "<polyline") != 2 {
		t.Errorf("polylines = %d, attendu 2", strings.Count(out, "<polyline"))
	}
	// pas de NaN ni d'Inf dans les coordonnées
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

func TestSVGEscapesLabels(t *testing.T) {
	out := SVG([]Line{{Label: "a<b>&c", Color: "#000000", Points: ramp(3)}}, 800, 300)
	if strings.Contains(out, "a<b>") {
		t.Error("label non échappé dans le SVG")
	}
	if !strings.Contains(out, "a&lt;b&gt;&amp;c") {
		t.Errorf("échappement attendu absent:\n%s", out)
	}
}
