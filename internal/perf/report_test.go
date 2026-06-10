package perf

import (
	"math"
	"testing"

	"finador/internal/domain"
)

// syntheticSeries construit une série journalière plate puis +2 % sur N jours.
func syntheticSeries(start domain.Date, flatDays int, riseDay domain.Date) []Point {
	var pts []Point
	v := 100.0
	for i := range flatDays + 1 {
		pts = append(pts, Point{Date: start.AddDays(i), Value: v})
	}
	// dernier point : riseDay avec +2 %
	pts = append(pts, Point{Date: riseDay, Value: v * 1.02})
	return pts
}

func TestReportOrigineHasTWR(t *testing.T) {
	// série : 100 jours plats à 100, puis +2 % le jour suivant
	start := d("2026-01-01")
	evalTo := d("2026-04-11") // > 30 jours
	pts := syntheticSeries(start, 99, evalTo)

	rows, _ := Report(pts, nil, evalTo, 0)

	var origRow *Row
	for i := range rows {
		if rows[i].Name == "inception" {
			origRow = &rows[i]
		}
	}
	if origRow == nil {
		t.Fatal("inception row absent")
	}
	if !origRow.HasTWR {
		t.Fatal("inception.HasTWR should be true")
	}
	approx(t, "TWR inception", origRow.TWR, 0.02, 1e-9)
}

func TestReportXIRRPresentOnLongWindow(t *testing.T) {
	start := d("2026-01-01")
	evalTo := d("2026-04-11") // > 30 jours
	pts := syntheticSeries(start, 99, evalTo)

	rows, _ := Report(pts, nil, evalTo, 0)

	var origRow *Row
	for i := range rows {
		if rows[i].Name == "inception" {
			origRow = &rows[i]
		}
	}
	if origRow == nil {
		t.Fatal("inception row absent")
	}
	if !origRow.HasXIRR {
		t.Fatal("XIRR expected on long window")
	}
}

func TestReportXIRRDashOnShortNamedPeriods(t *testing.T) {
	// Les périodes nommées "1j", "2j", "5j", "7j" durent < 30 jours →
	// HasXIRR doit être false (annualiser un micro-mouvement est sans sens).
	start := d("2025-06-01")
	evalTo := d("2026-06-01") // un an de données
	var pts []Point
	for i := range 366 {
		pts = append(pts, Point{Date: start.AddDays(i), Value: 100.0 + float64(i)*0.01})
	}

	rows, _ := Report(pts, nil, evalTo, 0)

	shortPeriods := map[string]bool{"1d": true, "2d": true, "5d": true, "7d": true}
	for _, row := range rows {
		if shortPeriods[row.Name] && row.HasXIRR {
			t.Errorf("short period %q should not have HasXIRR=true", row.Name)
		}
	}
	// long periods (1m, 3m, 1y) must have XIRR
	longPeriods := map[string]bool{"1m": true, "3m": true, "1y": true}
	for _, row := range rows {
		if longPeriods[row.Name] && !row.HasXIRR {
			t.Errorf("long period %q should have HasXIRR=true", row.Name)
		}
	}
}

func TestRiskFreeFromConfig(t *testing.T) {
	cases := []struct {
		cfg  map[string]string
		want float64
	}{
		{map[string]string{"risk-free": "2.4%"}, 0.024},
		{map[string]string{"risk-free": "2.4"}, 0.024},
		{map[string]string{"risk-free": " 3.0% "}, 0.030},
		{map[string]string{}, 0},
		{map[string]string{"risk-free": ""}, 0},
		{map[string]string{"risk-free": "abc%"}, 0},
	}
	for _, c := range cases {
		got := RiskFreeFromConfig(c.cfg)
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("RiskFreeFromConfig(%v) = %v, want %v", c.cfg, got, c.want)
		}
	}
}

func TestReportMetricsNotEmpty(t *testing.T) {
	start := d("2026-01-01")
	evalTo := d("2026-04-11")
	pts := syntheticSeries(start, 99, evalTo)

	_, metrics := Report(pts, nil, evalTo, 0.02)

	if metrics.CAGR == 0 && metrics.Vol == 0 {
		t.Error("CAGR and Vol cannot both be 0 on a non-trivial series")
	}
	if metrics.RiskFree != 0.02 {
		t.Errorf("RiskFree = %v, want 0.02", metrics.RiskFree)
	}
}
