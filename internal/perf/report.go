package perf

import (
	"strconv"
	"strings"

	"finador/internal/domain"
)

// RiskFreeFromConfig reads the annualized risk-free rate from a book config map.
// Expected key: "risk-free", value: "2.4" or "2.4%" → returns 0.024.
// Returns 0 if absent or unparseable.
func RiskFreeFromConfig(cfg map[string]string) float64 {
	s := strings.TrimSuffix(strings.TrimSpace(cfg["risk-free"]), "%")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v / 100
}

// Row is one period line of a performance report.
type Row struct {
	Name    string
	TWR     float64
	HasTWR  bool
	XIRR    float64
	HasXIRR bool
	Gain    float64 // money gained/lost over the window, net of contributions (display ccy)
	HasGain bool
}

// Minimum track records before annualized figures mean anything: annualizing a
// return earned over a few days compounds noise into absurdity. Below these
// spans the report exposes the cumulative since-inception return instead, and
// the Has* flags tell the renderer to hide the annualized cells.
const (
	MinDaysForRisk = 90  // vol/Sharpe/Sortino — about a quarter of daily returns
	MinDaysForCAGR = 365 // CAGR is a *compound annual* rate: needs at least a year
)

// Metrics holds the summary statistics computed over the full origin window.
// InceptionTWR/Since/Days always describe the track record; the annualized
// figures (CAGR, Vol, Sharpe, Sortino) are only set — and HasCAGR/HasRisk only
// true — once enough history backs them.
type Metrics struct {
	InceptionTWR                         float64 // cumulative TWR since the first point
	Since                                domain.Date
	Days                                 int
	CAGR, Vol, Sharpe, Sortino, RiskFree float64
	HasCAGR, HasRisk                     bool
	Drawdown                             Drawdown
}

// Report builds the standard period table + metrics for a daily series.
// It covers each period returned by Names() that the track record actually
// spans, plus the "inception" row. Points and flows are expressed in display
// currency (raw series output). rf is the annualized risk-free rate.
func Report(points []Point, flows []Flow, evalTo domain.Date, rf float64) ([]Row, Metrics) {
	if len(points) == 0 {
		return nil, Metrics{}
	}
	origin := points[0].Date

	var rows []Row
	for _, name := range Names() {
		pf, pt, err := PeriodRange(name, evalTo)
		if err != nil {
			continue
		}
		if pf.Before(origin) {
			continue // window predates the track record — the inception row covers it
		}
		rows = append(rows, periodRow(name, points, flows, pf, pt))
	}
	rows = append(rows, periodRow("inception", points, flows, origin, evalTo))

	// Métriques sur la fenêtre complète depuis l'origine
	allPts, allFlows := windowSlice(points, flows, origin, evalTo)
	twrTotal := TWR(allPts, allFlows)
	returns := DailyReturns(allPts, allFlows)
	days := 0
	if len(allPts) >= 2 {
		days = int(allPts[len(allPts)-1].Date.Time().Sub(allPts[0].Date.Time()).Hours() / 24)
	}
	m := Metrics{
		InceptionTWR: twrTotal,
		Since:        origin,
		Days:         days,
		RiskFree:     rf,
		Drawdown:     MaxDrawdown(allPts),
	}
	if days >= MinDaysForRisk && len(returns) >= 2 {
		m.Vol = Vol(returns)
		m.Sharpe = Sharpe(returns, rf)
		m.Sortino = Sortino(returns, rf)
		m.HasRisk = true
	}
	if days >= MinDaysForCAGR {
		m.CAGR = CAGR(twrTotal, days)
		m.HasCAGR = true
	}
	return rows, m
}

// periodRow builds a single Row for the window [from, to].
func periodRow(name string, points []Point, flows []Flow, from, to domain.Date) Row {
	pts, fls := windowSlice(points, flows, from, to)
	row := Row{Name: name}
	if len(pts) >= 2 {
		row.TWR = TWR(pts, fls)
		row.HasTWR = true
		// Money P&L over the window: the value change NOT explained by money you
		// put in or took out. Declaring an existing holding (a contribution) is
		// neutralized via the flows, so it never reads as a gain.
		var netFlow float64
		for _, f := range fls {
			netFlow += f.Amount
		}
		row.Gain = pts[len(pts)-1].Value - pts[0].Value - netFlow
		row.HasGain = true
	}
	// XIRR : fenêtres < 30 jours ou V0 ≤ 0 → tiret
	if to.Time().Sub(from.Time()).Hours() >= 30*24 && len(pts) >= 2 && pts[0].Value > 0 {
		cfs := []Flow{{Date: pts[0].Date, Amount: -pts[0].Value}}
		for _, fl := range fls {
			cfs = append(cfs, Flow{Date: fl.Date, Amount: -fl.Amount})
		}
		cfs = append(cfs, Flow{Date: pts[len(pts)-1].Date, Amount: pts[len(pts)-1].Value})
		if r, err := XIRR(cfs); err == nil {
			row.XIRR = r
			row.HasXIRR = true
		}
	}
	return row
}

// windowSlice extrait les points dans [from, to] et les flux strictement
// après from et ≤ to (les flux du jour de base sont dans V0).
func windowSlice(points []Point, flows []Flow, from, to domain.Date) ([]Point, []Flow) {
	var pts []Point
	for _, p := range points {
		if p.Date.Before(from) || to.Before(p.Date) {
			continue
		}
		pts = append(pts, p)
	}
	var fls []Flow
	for _, fl := range flows {
		if to.Before(fl.Date) || !from.Before(fl.Date) {
			continue
		}
		fls = append(fls, fl)
	}
	return pts, fls
}
