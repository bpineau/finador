package chart

import (
	"strings"
	"testing"
)

func TestSparkline(t *testing.T) {
	out := Sparkline(ramp(30), 90, 24, "#1e6e4e")
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 90 24"`,
		`<polyline`, `stroke="#1e6e4e"`, `fill="none"`, `</svg>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing in:\n%s", want, out)
		}
	}
	// pas d'axes, pas de texte, pas de NaN
	for _, bad := range []string{"<text", "<line", "NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s should not appear in a sparkline", bad)
		}
	}
}

func TestSparklineDegenerate(t *testing.T) {
	if out := Sparkline(nil, 90, 24, "#000"); out != "" {
		t.Errorf("empty input: %q", out)
	}
	if out := Sparkline(ramp(1), 90, 24, "#000"); out != "" {
		t.Errorf("single point: %q", out)
	}
	// série plate : rendue sans division par zéro
	flat := ramp(5)
	for i := range flat {
		flat[i].Value = 42
	}
	if out := Sparkline(flat, 90, 24, "#000"); !strings.Contains(out, "<polyline") {
		t.Errorf("flat series should render: %q", out)
	}
}

func TestSparklineDeterministic(t *testing.T) {
	if Sparkline(ramp(15), 90, 24, "#000") != Sparkline(ramp(15), 90, 24, "#000") {
		t.Error("non deterministic")
	}
}
