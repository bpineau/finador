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
}

// Metrics holds the summary statistics computed over the full origin window.
type Metrics struct {
	CAGR, Vol, Sharpe, Sortino, RiskFree float64
	Drawdown                             Drawdown
}

// Report builds the standard period table + metrics for a daily series.
// It covers each period returned by Names() plus the "inception" row.
// Points and flows are expressed in display currency (raw series output).
// rf is the annualized risk-free rate (e.g. 0.024 for 2.4 %).
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
		CAGR:     CAGR(twrTotal, days),
		Vol:      Vol(returns),
		Sharpe:   Sharpe(returns, rf),
		Sortino:  Sortino(returns, rf),
		RiskFree: rf,
		Drawdown: MaxDrawdown(allPts),
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
