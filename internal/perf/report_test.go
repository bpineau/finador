package perf

import (
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
		if rows[i].Name == "origine" {
			origRow = &rows[i]
		}
	}
	if origRow == nil {
		t.Fatal("ligne origine absente")
	}
	if !origRow.HasTWR {
		t.Fatal("origine.HasTWR doit être true")
	}
	approx(t, "TWR origine", origRow.TWR, 0.02, 1e-9)
}

func TestReportXIRRPresentOnLongWindow(t *testing.T) {
	start := d("2026-01-01")
	evalTo := d("2026-04-11") // > 30 jours
	pts := syntheticSeries(start, 99, evalTo)

	rows, _ := Report(pts, nil, evalTo, 0)

	var origRow *Row
	for i := range rows {
		if rows[i].Name == "origine" {
			origRow = &rows[i]
		}
	}
	if origRow == nil {
		t.Fatal("ligne origine absente")
	}
	if !origRow.HasXIRR {
		t.Fatal("XIRR attendu sur fenêtre longue")
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

	shortPeriods := map[string]bool{"1j": true, "2j": true, "5j": true, "7j": true}
	for _, row := range rows {
		if shortPeriods[row.Name] && row.HasXIRR {
			t.Errorf("période courte %q ne devrait pas avoir HasXIRR=true", row.Name)
		}
	}
	// les périodes longues (1m, 3m, 1a) doivent avoir XIRR
	longPeriods := map[string]bool{"1m": true, "3m": true, "1a": true}
	for _, row := range rows {
		if longPeriods[row.Name] && !row.HasXIRR {
			t.Errorf("période longue %q devrait avoir HasXIRR=true", row.Name)
		}
	}
}

func TestReportMetricsNotEmpty(t *testing.T) {
	start := d("2026-01-01")
	evalTo := d("2026-04-11")
	pts := syntheticSeries(start, 99, evalTo)

	_, metrics := Report(pts, nil, evalTo, 0.02)

	if metrics.CAGR == 0 && metrics.Vol == 0 {
		t.Error("CAGR et Vol ne peuvent pas être tous les deux à 0 sur une série non triviale")
	}
	if metrics.RiskFree != 0.02 {
		t.Errorf("RiskFree = %v, attendu 0.02", metrics.RiskFree)
	}
}
