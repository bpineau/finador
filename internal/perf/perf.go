// Package perf computes performance figures on value series: pure math,
// no I/O, no market access. Inputs come from portfolio.Series; the math
// itself is pofo's metrics package, adapted to finador's domain types
// (its 0-instead-of-NaN convention for undefined ratios included).
//
// Every figure but the money gains reads pofo's flow-neutral growth index
// (metrics.FlowIndex), fed with the raw values and the real flows: a day's
// flows netted, money in booked at the start of the day, money out at its
// close (D50, since finador values a sale at the day's close), a day with
// nothing held before nor after counting as no return (D52), and the
// drawdown measured on that index rather than on the value, where a sale is
// a fall (D49). finador keeps no growth arithmetic of its own (D54).
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

// Flow is an external in/out of the measured scope (positive = money in).
// The day's flows are netted: money that comes in is booked at the start of
// its day (it earns that day), money that goes out at its end (a sale is
// valued at the day's close, so the shares earned the day before leaving).
// A flow dated between two points counts on the next one; see
// metrics.FlowIndex.
type Flow struct {
	Date   domain.Date
	Amount float64
}

// toSeries converts points and flows to the parallel slices pofo's metrics
// operate on (dates as midnight UTC, matching domain.Date.Time).
func toSeries(points []Point, flows []Flow) ([]time.Time, []float64, []metrics.Flow) {
	dates := make([]time.Time, len(points))
	values := make([]float64, len(points))
	for i, p := range points {
		dates[i] = p.Date.Time()
		values[i] = p.Value
	}
	var mflows []metrics.Flow
	if len(flows) > 0 {
		mflows = make([]metrics.Flow, len(flows))
		for i, f := range flows {
			mflows[i] = metrics.Flow{Date: f.Date.Time(), Amount: f.Amount}
		}
	}
	return dates, values, mflows
}

// orZero maps pofo's NaN (undefined) convention to finador's 0.
func orZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// TWR chain-links the daily returns of the series, external flows
// neutralized (metrics.TWR): it measures the strategy, not the saver. 0 for
// fewer than two points.
func TWR(points []Point, flows []Flow) float64 {
	twr, _ := metrics.TWR(toSeries(points, flows))
	return twr
}

// DailyReturns yields the flow-adjusted weekday returns of a calendar-daily
// series, and their cadence (returns per year) to annualize them with
// (metrics.FlowReturns). Week-end points are folded into Monday's return:
// forward-filled flats would dilute the volatility, and a calendar-daily
// series then reads at metrics.TradingDaysPerYear (252). The cadence is
// measured on the dates, not assumed, so a sparser series is never
// annualized as a daily one; it is NaN when no return survives. A day with
// nothing to earn on (no base, or nothing held before nor after) is no
// return at all.
func DailyReturns(points []Point, flows []Flow) (returns []float64, periodsPerYear float64) {
	return metrics.FlowReturns(toSeries(points, flows))
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
// its flow-neutral growth (metrics.FlowIndex): a withdrawal or a sale lowers
// the value and loses nothing, so it is never a drawdown (D49).
func MaxDrawdown(points []Point, flows []Flow) Drawdown {
	dates, values, mflows := toSeries(points, flows)
	return toDrawdown(metrics.MaxDrawdown(dates, metrics.FlowIndex(dates, values, mflows)))
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
