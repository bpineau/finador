package perf

import (
	"math"
	"strings"
	"testing"

	"finador/internal/domain"
)

// syntheticSeries builds a flat daily series then +2% over N days.
func syntheticSeries(start domain.Date, flatDays int, riseDay domain.Date) []Point {
	var pts []Point
	v := 100.0
	for i := range flatDays + 1 {
		pts = append(pts, Point{Date: start.AddDays(i), Value: v})
	}
	// last point: riseDay with +2%
	pts = append(pts, Point{Date: riseDay, Value: v * 1.02})
	return pts
}

func TestReportOrigineHasTWR(t *testing.T) {
	// series: 100 flat days at 100, then +2% the next day
	start := d("2026-01-01")
	evalTo := d("2026-04-11") // > 30 days
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

// TestReportSkipsFlatDuplicates: a book flat for months then active one
// month must not print 3m/ytd/1y rows identical to the 1m row - they would
// read as "one year measured" when only one month really moved.
func TestReportSkipsFlatDuplicates(t *testing.T) {
	today := domain.Date{Year: 2026, Month: 7, Day: 3}
	var pts []Point
	// 400 flat days at 1000, then a single +5% step ~20 days ago: every
	// today-anchored window from 1m up measures exactly the same move.
	for d := today.AddDays(-400); !today.Before(d); d = d.AddDays(1) {
		v := 1000.0
		if today.AddDays(-20).Before(d) {
			v = 1050.0
		}
		pts = append(pts, Point{Date: d, Value: v})
	}
	rows, _ := Report(pts, nil, today, 0)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	got := strings.Join(names, ",")
	for _, dup := range []string{"3m", "ytd", "1y"} {
		if strings.Contains(got, dup) {
			t.Fatalf("flat duplicate %q kept: %s", dup, got)
		}
	}
	if !strings.Contains(got, "1m") || !strings.Contains(got, "inception") {
		t.Fatalf("informative rows missing: %s", got)
	}
}

// Gain is the value change net of contributions: declaring/adding money is
// never a gain, only what that money then earns.
func TestReportGainNetsOutContributions(t *testing.T) {
	start := d("2026-01-01")
	var pts []Point
	for i := range 41 {
		pts = append(pts, Point{Date: start.AddDays(i), Value: 1000 + float64(i)*2.5}) // 1000 → 1100
	}
	evalTo := start.AddDays(40)

	rows, _ := Report(pts, nil, evalTo, 0)
	var inc *Row
	for i := range rows {
		if rows[i].Name == "inception" {
			inc = &rows[i]
		}
	}
	if inc == nil || !inc.HasGain {
		t.Fatal("inception gain missing")
	}
	approx(t, "inception gain", inc.Gain, 100, 1e-6)

	// A mid-window contribution of +500 lifts the value but is NOT a gain.
	flows := []Flow{{Date: start.AddDays(20), Amount: 500}}
	pts2 := make([]Point, len(pts))
	copy(pts2, pts)
	for i := 20; i < len(pts2); i++ {
		pts2[i].Value += 500
	}
	rows2, _ := Report(pts2, flows, evalTo, 0)
	for i := range rows2 {
		if rows2[i].Name == "inception" {
			approx(t, "gain net of contribution", rows2[i].Gain, 100, 1e-6)
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

// rampSeries: a gently rising series with a daily sawtooth so daily returns
// genuinely vary (non-zero vol) regardless of which weekday the window ends on.
func rampSeries(start domain.Date, days int) ([]Point, domain.Date) {
	var pts []Point
	for i := range days + 1 {
		v := 100.0 + float64(i)*0.1 + float64(i%3) // upward trend + 3-day sawtooth
		pts = append(pts, Point{Date: start.AddDays(i), Value: v})
	}
	return pts, start.AddDays(days)
}

// Annualized figures stay hidden until the track record is long enough; the
// cumulative inception return and span are always reported.
func TestReportMetricsGating(t *testing.T) {
	const rf = 0.02

	// < 90 days: no annualized stats, but inception TWR + span are present.
	pts, evalTo := rampSeries(d("2026-01-01"), 40)
	_, m := Report(pts, nil, evalTo, rf)
	if m.HasRisk || m.HasCAGR {
		t.Errorf("40d: expected no annualized stats, got HasRisk=%v HasCAGR=%v", m.HasRisk, m.HasCAGR)
	}
	if m.Days != 40 || m.Since != d("2026-01-01") {
		t.Errorf("40d: Days=%d Since=%v, want 40 / 2026-01-01", m.Days, m.Since)
	}
	if m.InceptionTWR == 0 {
		t.Error("40d: InceptionTWR should be reported even when annualized stats are hidden")
	}

	// 90–365 days: risk stats appear, CAGR still hidden (sub-year).
	pts, evalTo = rampSeries(d("2025-01-01"), 200)
	_, m = Report(pts, nil, evalTo, rf)
	if !m.HasRisk {
		t.Error("200d: HasRisk should be true")
	}
	if m.HasCAGR {
		t.Error("200d: CAGR should stay hidden under a year")
	}
	if m.Vol == 0 {
		t.Error("200d: Vol should be non-zero on a varying series")
	}

	// ≥ 365 days: CAGR appears too.
	pts, evalTo = rampSeries(d("2024-01-01"), 400)
	_, m = Report(pts, nil, evalTo, rf)
	if !m.HasRisk || !m.HasCAGR {
		t.Errorf("400d: want both annualized blocks, got HasRisk=%v HasCAGR=%v", m.HasRisk, m.HasCAGR)
	}
	if m.RiskFree != rf {
		t.Errorf("RiskFree = %v, want %v", m.RiskFree, rf)
	}
}

// Periods whose window starts before the first data point are omitted (the
// portfolio didn't exist then); the inception row covers the real span.
func TestReportSkipsPreInceptionPeriods(t *testing.T) {
	pts, evalTo := rampSeries(d("2026-04-01"), 40) // born 2026-04-01, ~40 days
	rows, _ := Report(pts, nil, evalTo, 0)

	names := map[string]bool{}
	for _, r := range rows {
		names[r.Name] = true
	}
	if !names["inception"] {
		t.Error("inception row must always be present")
	}
	for _, gone := range []string{"1y", "ytd", "prev-yr", "3m"} {
		if names[gone] {
			t.Errorf("period %q predates inception and should be omitted (rows: %v)", gone, names)
		}
	}
	if !names["1m"] { // 1 month fits inside ~40 days of history
		t.Errorf("1m fits the track record and should be present (rows: %v)", names)
	}
}
