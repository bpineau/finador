package web

import (
	"html/template"
	"net/http"
	"net/url"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

const (
	couleurEncre = "#1c1914"
	couleurVert  = "#1e6e4e"
)

type tab struct {
	Label, URL string
	Active     bool
}

type dashData struct {
	Today      domain.Date
	Val        portfolio.Valuation
	Curve      template.HTML // SVG from chart.SVG - never raw user data
	Rows       []perf.Row
	Met        perf.Metrics
	Tabs       []tab
	RangeLinks []tab
	Range      string
	Tree       []node
	Pie        template.HTML // SVG donut - generated server-side, safe
	PieSlices  []pieSlice
	Warnings   []string
	DayTWR     float64
	HasDayTWR  bool
	Flash      string
	Error      string
}

// chartRange resolves ?range= into a start date (zero = inception) and its name.
func chartRange(r *http.Request, today domain.Date) (domain.Date, string) {
	switch r.URL.Query().Get("range") {
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), "1m"
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), "3m"
	case "1y":
		return domain.DateOf(today.Time().AddDate(-1, 0, 0)), "1y"
	}
	return domain.Date{}, "all"
}

// slicePoints keeps points dated at or after from (zero from = everything).
func slicePoints(pts []perf.Point, from domain.Date) []perf.Point {
	if from.IsZero() {
		return pts
	}
	for i, p := range pts {
		if !p.Date.Before(from) {
			return pts[i:]
		}
	}
	return nil
}

// dashRangeLinks builds the range selector links for the dashboard, preserving
// the current by= parameter and omitting defaults (by=group, range=all).
func dashRangeLinks(mode, activeRange string) []tab {
	labels := []string{"1m", "3m", "1y", "all"}
	links := make([]tab, len(labels))
	for i, r := range labels {
		v := url.Values{}
		if mode != "group" {
			v.Set("by", mode)
		}
		if r != "all" {
			v.Set("range", r)
		}
		u := "/"
		if len(v) > 0 {
			u = "/?" + v.Encode()
		}
		links[i] = tab{Label: r, URL: u, Active: r == activeRange}
	}
	return links
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	scope, _ := portfolio.ParseScope(b, "")
	fx := market.Converter{FX: b.Market.FX}
	ccy := b.DisplayCurrency()

	mode := r.URL.Query().Get("by")
	switch mode {
	case "account":
		// valid mode
	default:
		mode = "group"
	}

	from, rangeName := chartRange(r, today)

	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build tabs preserving the current range param.
	tabURL := func(byMode string) string {
		v := url.Values{}
		if byMode != "group" {
			v.Set("by", byMode)
		}
		if rangeName != "all" {
			v.Set("range", rangeName)
		}
		if len(v) == 0 {
			return "/"
		}
		return "/?" + v.Encode()
	}

	data := dashData{
		Today:      today,
		Val:        val,
		Flash:      r.URL.Query().Get("flash"),
		Error:      r.URL.Query().Get("error"),
		Range:      rangeName,
		RangeLinks: dashRangeLinks(mode, rangeName),
		Tabs: []tab{
			{"by group", tabURL("group"), mode == "group"},
			{"by account", tabURL("account"), mode == "account"},
		},
	}

	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		grossAll := res.PerfPoints(false)
		netAll := res.PerfPoints(true)
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "gross", Color: couleurEncre, Points: slicePoints(grossAll, from)},
			{Label: "net", Color: couleurVert, Points: slicePoints(netAll, from)},
		}, 860, 300))
		// perf.Report always uses the full series; windows end at the last settled
		// close, not calendar today (see perf.CloseAnchor).
		data.Rows, data.Met = perf.Report(grossAll, res.PerfFlows(), perf.CloseAnchor(&b.Market, today), perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
		for _, row := range data.Rows {
			if row.Name == "1d" && row.HasTWR {
				data.DayTWR = row.TWR
				data.HasDayTWR = true
				break
			}
		}
	}

	lines, err := portfolio.Breakdown(b, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data.Tree = buildTree(lines, mode, b.LabelsFor)
	data.Pie, data.PieSlices = buildPie(lines)

	s.render(w, http.StatusOK, "dashboard.html", data)
}
