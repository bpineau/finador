package chart

import (
	"strings"
	"testing"

	"finador/internal/domain"
	"finador/internal/perf"
)

func d(s string) domain.Date {
	dd, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func ramp(n int) []perf.Point {
	pts := make([]perf.Point, n)
	for i := range pts {
		pts[i] = perf.Point{Date: d("2026-01-01").AddDays(i), Value: float64(100 + i)}
	}
	return pts
}

func TestBrailleShape(t *testing.T) {
	out := Braille(ramp(60), 30, 8)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 8 curve lines + 1 date line
	if len(lines) != 9 {
		t.Fatalf("lignes = %d\n%s", len(lines), out)
	}
	// each curve line contains at least one non-empty braille character
	brailleCount := 0
	for _, l := range lines[:8] {
		for _, r := range l {
			if r > 0x2800 && r <= 0x28FF {
				brailleCount++
			}
		}
	}
	if brailleCount == 0 {
		t.Fatalf("aucun point braille:\n%s", out)
	}
	// labels: max on the first line, min on the last curve line
	if !strings.Contains(lines[0], "159") {
		t.Errorf("max absent de la 1re ligne: %q", lines[0])
	}
	if !strings.Contains(lines[7], "100") {
		t.Errorf("min absent de la dernière ligne: %q", lines[7])
	}
	if !strings.Contains(lines[8], "2026-01-01") || !strings.Contains(lines[8], "2026-03-01") {
		t.Errorf("dates absentes: %q", lines[8])
	}
}

func TestBrailleRampGoesUp(t *testing.T) {
	out := Braille(ramp(60), 30, 8)
	lines := strings.Split(out, "\n")
	first := firstBrailleCol(lines[0]) // the end of the ramp (top) is on the right
	last := firstBrailleCol(lines[7])  // the start (bottom) is on the left
	if last == -1 || first == -1 || last >= first {
		t.Errorf("rampe non croissante: bas à col %d, haut à col %d\n%s", last, first, out)
	}
}

func firstBrailleCol(line string) int {
	for i, r := range []rune(line) {
		if r > 0x2800 && r <= 0x28FF {
			return i
		}
	}
	return -1
}

func TestBrailleFlatAndEmpty(t *testing.T) {
	if out := Braille(nil, 30, 8); out != "" {
		t.Errorf("série vide: %q", out)
	}
	flat := []perf.Point{{Date: d("2026-01-01"), Value: 50}, {Date: d("2026-01-02"), Value: 50}}
	out := Braille(flat, 10, 4)
	if out == "" || !strings.Contains(out, "50") {
		t.Errorf("série plate:\n%s", out)
	}
}

func TestFormatCompact(t *testing.T) {
	for v, want := range map[float64]string{
		1234567.0: "1.23M",
		473890.0:  "473.9k",
		1500.0:    "1.5k",
		999.0:     "999.00",
		-4230.5:   "-4.2k",
	} {
		if got := formatCompact(v); got != want {
			t.Errorf("formatCompact(%v) = %q, attendu %q", v, got, want)
		}
	}
}

func TestBrailleClampsBadDimensions(t *testing.T) {
	for _, dims := range [][2]int{{0, 0}, {-5, -5}, {1, 0}, {0, 1}} {
		out := Braille(ramp(10), dims[0], dims[1])
		if out == "" || !strings.Contains(out, "2026-01-01") {
			t.Errorf("Braille(%v) devrait rendre une courbe minimale", dims)
		}
	}
}
