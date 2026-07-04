// Package perf computes performance figures on value series: pure math,
// no I/O, no market access. Inputs come from portfolio.Series; the math
// itself is pofo's metrics package, adapted to finador's domain types
// (its 0-instead-of-NaN convention for undefined ratios included).
package perf

import (
	"math"
	"time"

	"finador/internal/domain"

	"github.com/bpineau/pofo/pkg/metrics"
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

// toSeries converts points to the parallel dates/values slices pofo's
// metrics operate on (dates as midnight UTC, matching domain.Date.Time).
func toSeries(points []Point) ([]time.Time, []float64) {
	dates := make([]time.Time, len(points))
	values := make([]float64, len(points))
	for i, p := range points {
		dates[i] = p.Date.Time()
		values[i] = p.Value
	}
	return dates, values
}

func toFlows(flows []Flow) []metrics.Flow {
	out := make([]metrics.Flow, len(flows))
	for i, f := range flows {
		out[i] = metrics.Flow{Date: f.Date.Time(), Amount: f.Amount}
	}
	return out
}

// orZero maps pofo's NaN (undefined) convention to finador's 0.
func orZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// TWR chain-links daily returns r_t = (V_t − F_t)/V_{t−1}, neutralizing
// external flows: it measures the strategy, not the saver.
func TWR(points []Point, flows []Flow) float64 {
	dates, values := toSeries(points)
	r, _ := metrics.TWR(dates, values, toFlows(flows))
	return r
}

// DailyReturns yields the flow-adjusted weekday returns of a calendar-daily
// series. Week-ends are forward-filled flats: keeping them would dilute the
// volatility, so they are dropped (≈252 returns a year, annualized with √252).
func DailyReturns(points []Point, flows []Flow) []float64 {
	dates, values := toSeries(points)
	return metrics.FlowReturns(dates, values, toFlows(flows))
}

// CAGR annualizes a total return over a calendar-day span.
func CAGR(totalReturn float64, days int) float64 {
	return metrics.Annualize(totalReturn, days)
}

// Vol is the annualized sample standard deviation of daily returns.
func Vol(returns []float64) float64 {
	return orZero(metrics.Volatility(returns))
}

// Sharpe uses arithmetic annualization of the mean daily excess return -
// the usual simple convention, documented in the plan.
func Sharpe(returns []float64, rfAnnual float64) float64 {
	return orZero(metrics.Sharpe(returns, rfAnnual))
}

// Sortino replaces the denominator with the downside deviation against the
// daily risk-free target.
func Sortino(returns []float64, rfAnnual float64) float64 {
	return orZero(metrics.Sortino(returns, rfAnnual))
}

// Drawdown describes the worst peak-to-trough loss of a series.
type Drawdown struct {
	Depth        float64 // negative: −0.25 = −25%
	Peak, Trough domain.Date
	Recovered    *domain.Date // nil if the peak is never regained
}

// MaxDrawdown returns the deepest drawdown episode of the series.
func MaxDrawdown(points []Point) Drawdown {
	dates, values := toSeries(points)
	return toDrawdown(metrics.MaxDrawdown(dates, values))
}

// toDrawdown maps a pofo episode to the domain-dated Drawdown; the zero
// Episode (a series that never declines) maps to the zero Drawdown.
func toDrawdown(ep metrics.Episode) Drawdown {
	if ep.PeakDate.IsZero() {
		return Drawdown{}
	}
	dd := Drawdown{Depth: ep.Depth, Peak: domain.DateOf(ep.PeakDate), Trough: domain.DateOf(ep.TroughDate)}
	if !ep.Ongoing && !ep.RecoverDate.IsZero() {
		rec := domain.DateOf(ep.RecoverDate)
		dd.Recovered = &rec
	}
	return dd
}
