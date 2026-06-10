// Package perf computes performance figures on value series: pure math,
// no I/O, no market access. Inputs come from portfolio.Series.
package perf

import (
	"errors"
	"math"
	"time"

	"finador/internal/domain"
)

// Point is one day of a value series.
type Point struct {
	Date  domain.Date
	Value float64
}

// Flow is an external in/out of the measured scope (positive = money in),
// assumed to happen at the start of its day.
type Flow struct {
	Date   domain.Date
	Amount float64
}

// TWR chain-links daily returns r_t = (V_t − F_t)/V_{t−1}, neutralizing
// external flows: it measures the strategy, not the saver. Days with a
// non-positive base are skipped.
func TWR(points []Point, flows []Flow) float64 {
	byDay := flowsByDay(flows)
	total := 1.0
	for i := 1; i < len(points); i++ {
		prev := points[i-1].Value
		if prev <= 0 {
			continue
		}
		total *= (points[i].Value - byDay[points[i].Date]) / prev
	}
	return total - 1
}

// DailyReturns yields the flow-adjusted weekday returns of a calendar-daily
// series. Week-ends are forward-filled flats: keeping them would dilute the
// volatility, so they are dropped (≈252 returns a year, annualized with √252).
func DailyReturns(points []Point, flows []Flow) []float64 {
	byDay := flowsByDay(flows)
	var out []float64
	for i := 1; i < len(points); i++ {
		prev := points[i-1].Value
		if prev <= 0 {
			continue
		}
		if wd := points[i].Date.Time().Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		out = append(out, (points[i].Value-byDay[points[i].Date])/prev-1)
	}
	return out
}

func flowsByDay(flows []Flow) map[domain.Date]float64 {
	byDay := map[domain.Date]float64{}
	for _, f := range flows {
		byDay[f.Date] += f.Amount
	}
	return byDay
}

// XIRR solves the money-weighted annual rate by bisection. Cashflows follow
// the investor's convention: invested money negative, final value positive.
// Pour des flux à plusieurs changements de signe, la NPV peut avoir plusieurs
// racines : la bissection en retourne une — convention assumée pour des flux
// d'épargne classiques.
func XIRR(cashflows []Flow) (float64, error) {
	if len(cashflows) < 2 {
		return 0, errors.New("XIRR: au moins deux flux requis")
	}
	t0 := cashflows[0].Date.Time()
	npv := func(r float64) float64 {
		sum := 0.0
		for _, f := range cashflows {
			years := f.Date.Time().Sub(t0).Hours() / 24 / 365.25
			sum += f.Amount * math.Pow(1+r, -years)
		}
		return sum
	}
	lo, hi := -0.9999, 100.0
	flo, fhi := npv(lo), npv(hi)
	if math.IsNaN(flo) || math.IsNaN(fhi) || flo*fhi > 0 {
		return 0, errors.New("XIRR non défini pour ces flux (pas de changement de signe)")
	}
	for range 200 {
		mid := (lo + hi) / 2
		if fm := npv(mid); fm == 0 || hi-lo < 1e-10 {
			return mid, nil
		} else if fm*flo < 0 {
			hi = mid
		} else {
			lo, flo = mid, fm
		}
	}
	return (lo + hi) / 2, nil
}

// CAGR annualizes a total return over a calendar-day span.
func CAGR(totalReturn float64, days int) float64 {
	if days <= 0 || totalReturn <= -1 {
		return 0
	}
	return math.Pow(1+totalReturn, 365.25/float64(days)) - 1
}

// Vol is the annualized sample standard deviation of daily returns.
func Vol(returns []float64) float64 {
	if len(returns) < 2 {
		return 0
	}
	m := mean(returns)
	ss := 0.0
	for _, r := range returns {
		ss += (r - m) * (r - m)
	}
	return math.Sqrt(ss/float64(len(returns)-1)) * math.Sqrt(252)
}

// Sharpe uses arithmetic annualization of the mean daily excess return —
// the usual simple convention, documented in the plan.
func Sharpe(returns []float64, rfAnnual float64) float64 {
	v := Vol(returns)
	if v == 0 {
		return 0
	}
	return (mean(returns)*252 - rfAnnual) / v
}

// Sortino replaces the denominator with the downside deviation against the
// daily risk-free target.
func Sortino(returns []float64, rfAnnual float64) float64 {
	if len(returns) == 0 {
		return 0
	}
	target := rfAnnual / 252
	ss := 0.0
	for _, r := range returns {
		if r < target {
			ss += (r - target) * (r - target)
		}
	}
	down := math.Sqrt(ss/float64(len(returns))) * math.Sqrt(252)
	if down == 0 {
		return 0
	}
	return (mean(returns)*252 - rfAnnual) / down
}

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Drawdown describes the worst peak-to-trough loss of a series.
type Drawdown struct {
	Depth        float64 // négatif : −0.25 = −25 %
	Peak, Trough domain.Date
	Recovered    *domain.Date // nil si le pic n'est pas regagné
}

func MaxDrawdown(points []Point) Drawdown {
	var dd Drawdown
	if len(points) == 0 {
		return dd
	}
	peak := points[0]
	for _, p := range points[1:] {
		// une valeur qui regagne exactement le pic ré-ancre le pic :
		// un drawdown ne doit pas enjamber une récupération complète
		if p.Value >= peak.Value {
			peak = p
			continue
		}
		if peak.Value <= 0 {
			continue
		}
		if depth := (p.Value - peak.Value) / peak.Value; depth < dd.Depth {
			dd.Depth, dd.Peak, dd.Trough = depth, peak.Date, p.Date
			dd.Recovered = nil
		}
	}
	// première date où le pic du drawdown maximal est regagné
	inDD := false
	for _, p := range points {
		if p.Date == dd.Peak {
			inDD = true
			continue
		}
		if inDD && p.Date.Time().After(dd.Trough.Time()) && p.Value >= valueAt(points, dd.Peak) {
			rec := p.Date
			dd.Recovered = &rec
			break
		}
	}
	return dd
}

func valueAt(points []Point, d domain.Date) float64 {
	for _, p := range points {
		if p.Date == d {
			return p.Value
		}
	}
	return math.Inf(1)
}
