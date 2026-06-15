package perf

import (
	"math"
	"strings"
	"testing"

	"finador/internal/domain"
)

func d(s string) domain.Date {
	dd, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func approx(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, attendu %.6f (±%.6f)", what, got, want, tol)
	}
}

func TestTWRNoFlows(t *testing.T) {
	pts := []Point{
		{d("2026-06-01"), 100}, {d("2026-06-02"), 110}, {d("2026-06-03"), 99},
	}
	// 100→110: +10%; 110→99: −10%; compounded: 0.99 − 1 = −1%
	approx(t, "TWR", TWR(pts, nil), -0.01, 1e-9)
}

func TestTWRNeutralizesFlows(t *testing.T) {
	// Day 2: a contribution of 100 just before the open lifts the value to 210,
	// then the market gains +10% → 231. TWR must see only day 1's +5%
	// (100→105) and day 2's +10% ((231−100... no: (V2−F2)/V1.
	pts := []Point{{d("2026-06-01"), 100}, {d("2026-06-02"), 105}, {d("2026-06-03"), 215.5}}
	flows := []Flow{{d("2026-06-03"), 100}}
	// r3 = (215.5 − 100)/105 = 1.10 → +10%. TWR = 1.05×1.10 − 1 = 15.5%
	approx(t, "TWR", TWR(pts, flows), 0.155, 1e-9)
}

func TestTWRSkipsZeroBase(t *testing.T) {
	pts := []Point{{d("2026-06-01"), 0}, {d("2026-06-02"), 100}, {d("2026-06-03"), 110}}
	flows := []Flow{{d("2026-06-02"), 100}}
	// day 2: zero base → skipped; day 3: +10%
	approx(t, "TWR", TWR(pts, flows), 0.10, 1e-9)
}

func TestDailyReturnsWeekdaysOnly(t *testing.T) {
	// Friday June 5 2026, Saturday 6, Sunday 7, Monday 8
	pts := []Point{
		{d("2026-06-04"), 100}, {d("2026-06-05"), 102},
		{d("2026-06-06"), 102}, {d("2026-06-07"), 102}, {d("2026-06-08"), 104},
	}
	rs := DailyReturns(pts, nil)
	// Friday (+2%) and Monday (104/102 − 1) kept; Saturday/Sunday dropped
	if len(rs) != 2 {
		t.Fatalf("returns = %v, attendu 2 valeurs", rs)
	}
	approx(t, "r[0]", rs[0], 0.02, 1e-9)
	approx(t, "r[1]", rs[1], 104.0/102.0-1, 1e-9)
}

func TestXIRRKnownValue(t *testing.T) {
	// Verifiable reference: −1000 on Jan 1, +1100 on Dec 31 2026
	// (364 days). 1100/1000 = 1.10 over 364/365.25 years → r ≈ 10.03%
	r, err := XIRR([]Flow{{d("2026-01-01"), -1000}, {d("2026-12-31"), 1100}})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "XIRR", r, math.Pow(1.10, 365.25/364)-1, 1e-6)
}

func TestXIRRWithIntermediateFlow(t *testing.T) {
	// −1000 at the start, −500 mid-year, +1600 after a year.
	// Independent ground truth: NPV(r)=0; check that NPV(XIRR)≈0.
	flows := []Flow{{d("2026-01-01"), -1000}, {d("2026-07-01"), -500}, {d("2027-01-01"), 1600}}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	npv := 0.0
	for _, f := range flows {
		days := f.Date.Time().Sub(d("2026-01-01").Time()).Hours() / 24
		npv += f.Amount * math.Pow(1+r, -days/365.25)
	}
	approx(t, "NPV(XIRR)", npv, 0, 1e-6)
	if r < 0.05 || r > 0.10 {
		t.Errorf("XIRR = %v, hors de la plage plausible [5%%, 10%%]", r)
	}
}

func TestXIRRNoSolution(t *testing.T) {
	if _, err := XIRR([]Flow{{d("2026-01-01"), -100}, {d("2026-06-01"), -50}}); err == nil ||
		!strings.Contains(err.Error(), "XIRR") {
		t.Fatalf("err = %v", err)
	}
}

func TestCAGR(t *testing.T) {
	// +21% over 2 years → 10% annual
	approx(t, "CAGR", CAGR(0.21, 731), math.Pow(1.21, 365.25/731)-1, 1e-9)
	approx(t, "CAGR 1an", CAGR(0.10, 365), math.Pow(1.10, 365.25/365)-1, 1e-9)
}

func TestVolSharpeSortino(t *testing.T) {
	rs := []float64{0.01, -0.005, 0.002, 0.007, -0.003}
	mean := (0.01 - 0.005 + 0.002 + 0.007 - 0.003) / 5
	var ss float64
	for _, r := range rs {
		ss += (r - mean) * (r - mean)
	}
	wantVol := math.Sqrt(ss/4) * math.Sqrt(252) // sample standard deviation, annualized
	approx(t, "Vol", Vol(rs), wantVol, 1e-9)

	wantSharpe := (mean*252 - 0.02) / wantVol
	approx(t, "Sharpe", Sharpe(rs, 0.02), wantSharpe, 1e-9)

	// Sortino: only returns below rf/252 count toward the denominator
	rfDaily := 0.02 / 252
	var dss float64
	n := 0
	for _, r := range rs {
		if r < rfDaily {
			dss += (r - rfDaily) * (r - rfDaily)
			n++
		}
	}
	_ = n
	wantDown := math.Sqrt(dss/float64(len(rs))) * math.Sqrt(252)
	approx(t, "Sortino", Sortino(rs, 0.02), (mean*252-0.02)/wantDown, 1e-9)
}

func TestVolEmptyAndSingle(t *testing.T) {
	if v := Vol(nil); v != 0 {
		t.Errorf("Vol(nil) = %v", v)
	}
	if v := Vol([]float64{0.01}); v != 0 {
		t.Errorf("Vol(1 point) = %v", v)
	}
	if s := Sharpe(nil, 0.02); s != 0 {
		t.Errorf("Sharpe(nil) = %v", s)
	}
}

func TestMaxDrawdown(t *testing.T) {
	pts := []Point{
		{d("2026-01-01"), 100}, {d("2026-02-01"), 120}, {d("2026-03-01"), 90},
		{d("2026-04-01"), 100}, {d("2026-05-01"), 125},
	}
	dd := MaxDrawdown(pts)
	approx(t, "depth", dd.Depth, -0.25, 1e-9) // 120 → 90
	if dd.Peak != d("2026-02-01") || dd.Trough != d("2026-03-01") {
		t.Errorf("pic/creux = %s/%s", dd.Peak, dd.Trough)
	}
	if dd.Recovered == nil || *dd.Recovered != d("2026-05-01") {
		t.Errorf("récupération = %v", dd.Recovered)
	}
}

func TestMaxDrawdownNotRecovered(t *testing.T) {
	pts := []Point{{d("2026-01-01"), 100}, {d("2026-02-01"), 80}}
	dd := MaxDrawdown(pts)
	approx(t, "depth", dd.Depth, -0.20, 1e-9)
	if dd.Recovered != nil {
		t.Errorf("récupération = %v, attendu nil", dd.Recovered)
	}
}

func TestMaxDrawdownReanchorsOnExactRetouch(t *testing.T) {
	pts := []Point{
		{d("2026-01-01"), 100}, {d("2026-02-01"), 120}, {d("2026-03-01"), 90},
		{d("2026-04-01"), 120}, {d("2026-05-01"), 60}, {d("2026-06-01"), 121},
	}
	dd := MaxDrawdown(pts)
	approx(t, "depth", dd.Depth, -0.5, 1e-9)
	if dd.Peak != d("2026-04-01") || dd.Trough != d("2026-05-01") {
		t.Errorf("pic/creux = %s/%s, attendu 2026-04-01/2026-05-01", dd.Peak, dd.Trough)
	}
	if dd.Recovered == nil || *dd.Recovered != d("2026-06-01") {
		t.Errorf("récupération = %v", dd.Recovered)
	}
}

func TestDailyReturnsAdjustsFlows(t *testing.T) {
	// Thursday June 4, Friday 5: contribution of 100 on Friday, value 210 → r = (210−100)/100 − 1 = +10%
	pts := []Point{{d("2026-06-04"), 100}, {d("2026-06-05"), 210}}
	rs := DailyReturns(pts, []Flow{{d("2026-06-05"), 100}})
	if len(rs) != 1 {
		t.Fatalf("returns = %v", rs)
	}
	approx(t, "r", rs[0], 0.10, 1e-9)
}

func TestCAGRGuards(t *testing.T) {
	if CAGR(0.10, 0) != 0 || CAGR(-1.5, 100) != 0 {
		t.Error("les gardes de CAGR doivent retourner 0")
	}
}
