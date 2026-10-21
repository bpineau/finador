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
// series, and their cadence (returns per year) to annualize them with.
// Week-ends are forward-filled flats: keeping them would dilute the
// volatility, so they are dropped, and a calendar-daily series then reads
// at metrics.TradingDaysPerYear (252). The cadence is measured on the dates,
// not assumed, so a sparser series is never annualized as a daily one; it is
// NaN when no return survives.
func DailyReturns(points []Point, flows []Flow) (returns []float64, periodsPerYear float64) {
	dates, values := toSeries(points)
	return metrics.FlowReturns(dates, values, toFlows(flows))
}

// CAGR annualizes a total return over a calendar-day span.
func CAGR(totalReturn float64, days int) float64 {
	return metrics.Annualize(totalReturn, days)
}

// Vol is the annualized sample standard deviation of per-period returns,
// periodsPerYear being their cadence (as DailyReturns reports it).
func Vol(returns []float64, periodsPerYear float64) float64 {
	return orZero(metrics.Volatility(returns, periodsPerYear))
}

// Sharpe uses arithmetic annualization of the mean per-period excess return
// at periodsPerYear, the usual simple convention.
func Sharpe(returns []float64, rfAnnual, periodsPerYear float64) float64 {
	return orZero(metrics.Sharpe(returns, rfAnnual, periodsPerYear))
}

// Sortino replaces the denominator with the downside deviation against the
// per-period risk-free target rfAnnual/periodsPerYear.
func Sortino(returns []float64, rfAnnual, periodsPerYear float64) float64 {
	return orZero(metrics.Sortino(returns, rfAnnual, periodsPerYear))
}

// Drawdown describes the worst peak-to-trough loss of a series.
type Drawdown struct {
	Depth        float64 // negative: −0.25 = −25%
	Peak, Trough domain.Date
	Recovered    *domain.Date // nil if the peak is never regained
}

// MaxDrawdown returns the deepest drawdown episode of the series, measured on
// its flow-neutral growth (see growth): a withdrawal or a sale lowers the
// value and loses nothing, so it is never a drawdown.
func MaxDrawdown(points []Point, flows []Flow) Drawdown {
	dates, index := growth(points, flows)
	return toDrawdown(metrics.MaxDrawdown(dates, index))
}

// growth chain-links the flow-adjusted daily factors of a value series into
// an index that starts at 1: what one unit held from the first point became,
// contributions and withdrawals neutralized - the TWR read day by day. The
// factor of a day is pofo's, V_t/(V_{t-1} + F_t) with the day's flows booked
// at its start, and a day whose base is not positive keeps the index flat.
//
// Every path-dependent statistic reads this index rather than the value: the
// value falls by the whole amount of a withdrawal or a sale, which is money
// leaving the scope, not money lost.
//
// Between two flows the index is the value times a constant scale, never a
// running product, so a flowless stretch reads exactly the value's ratios and
// an exact retouch of a peak stays exact.
func growth(points []Point, flows []Flow) ([]time.Time, []float64) {
	dates, values := toSeries(points)
	byDay := map[domain.Date]float64{}
	for _, f := range flows {
		byDay[f.Date] += f.Amount
	}
	index := make([]float64, len(values))
	scale := 0.0 // index per unit of value; 0 until a positive value sets it
	for i, v := range values {
		switch flow := byDay[points[i].Date]; {
		case i == 0:
			index[0] = 1
		case flow == 0 && scale > 0 && v > 0:
			index[i] = scale * v
			continue // the scale holds until the next flow
		default:
			index[i] = index[i-1]
			if base := values[i-1] + flow; base > 0 {
				index[i] *= v / base
			}
		}
		scale = 0
		if v > 0 {
			scale = index[i] / v
		}
	}
	return dates, index
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
