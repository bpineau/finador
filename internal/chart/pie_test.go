package chart

import (
	"strings"
	"testing"
)

func TestPie(t *testing.T) {
	out := Pie([]float64{50, 30, 20}, []string{"#1c1914", "#1e6e4e", "#9a6b1f"}, 180)
	if strings.Count(out, "<path") != 3 {
		t.Fatalf("paths = %d, want 3:\n%s", strings.Count(out, "<path"), out)
	}
	for _, want := range []string{`viewBox="0 0 180 180"`, `fill="#1e6e4e"`, "</svg>"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing", want)
		}
	}
	for _, bad := range []string{"NaN", "Inf", "<text"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s should not appear", bad)
		}
	}
}

func TestPieSingleAndEmpty(t *testing.T) {
	if out := Pie(nil, nil, 180); out != "" {
		t.Errorf("empty: %q", out)
	}
	if out := Pie([]float64{0, 0}, []string{"#000", "#000"}, 180); out != "" {
		t.Errorf("all-zero: %q", out)
	}
	// a single non-zero slice → a full ring (two arcs, not a degenerate path)
	out := Pie([]float64{0, 42}, []string{"#000", "#1c1914"}, 180)
	if !strings.Contains(out, "<path") && !strings.Contains(out, "<circle") {
		t.Errorf("single slice should render: %q", out)
	}
}

func TestPieDeterministic(t *testing.T) {
	a := Pie([]float64{3, 2, 1}, []string{"#a", "#b", "#c"}, 120)
	b := Pie([]float64{3, 2, 1}, []string{"#a", "#b", "#c"}, 120)
	if a != b {
		t.Error("non deterministic")
	}
}

func TestNoGridLines(t *testing.T) {
	out := SVG([]Line{{Label: "g", Color: "#1c1914", Points: ramp(30)}}, 800, 300)
	if strings.Contains(out, "<line") {
		t.Error("grid lines should be gone")
	}
	// the scale labels stay
	if !strings.Contains(out, "129") && !strings.Contains(out, "100") {
		t.Error("scale labels should remain")
	}
}
