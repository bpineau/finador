package web

import (
	"html/template"
	"math"
	"net/http"
	"net/url"
	"slices"

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

// pieSlice is one sector of the allocation donut legend.
type pieSlice struct {
	Label, URL string
	// Color comes from PiePalette (constant, safe) — declared template.CSS so
	// html/template does not escape it as an untrusted CSS value.
	Color   template.CSS
	Amount  float64
	Percent int
}

type dashData struct {
	Today      domain.Date
	Val        portfolio.Valuation
	Curve      template.HTML // SVG from chart.SVG — never raw user data
	Rows       []perf.Row
	Met        perf.Metrics
	Mode       string // group | account | asset
	Tabs       []tab
	RangeLinks []tab
	Range      string
	Tree       []node
	Pie        template.HTML // SVG donut — generated server-side, safe
	PieSlices  []pieSlice
	Warnings   []string
	Flash      string
	Error      string
}

// buildPie aggregates Breakdown lines into top-level group + cash slices,
// sorted by amount descending, and generates the SVG donut + legend data.
func buildPie(lines []portfolio.PositionLine) (template.HTML, []pieSlice) {
	// aggregate by top group; cash (l.Asset == nil) goes to "cash" key
	amounts := map[string]float64{}
	urls := map[string]string{}
	var order []string
	for _, l := range lines {
		var key, groupURL string
		if l.Asset == nil {
			key = "cash"
		} else {
			key = topGroup(l.Asset.Group)
			groupURL = "/group/" + escapeGroup(key)
		}
		if _, seen := amounts[key]; !seen {
			order = append(order, key)
			urls[key] = groupURL
		}
		amounts[key] += l.Gross
	}

	// collect non-zero slices
	type kv struct {
		key    string
		amount float64
	}
	var kvs []kv
	for _, k := range order {
		if amounts[k] > 0 {
			kvs = append(kvs, kv{k, amounts[k]})
		}
	}
	if len(kvs) == 0 {
		return "", nil
	}

	// sort descending by amount, stable alphabetic on tie
	slices.SortStableFunc(kvs, func(a, b kv) int {
		switch {
		case a.amount > b.amount:
			return -1
		case a.amount < b.amount:
			return 1
		}
		if a.key < b.key {
			return -1
		}
		if a.key > b.key {
			return 1
		}
		return 0
	})

	// assign colors after sorting (palette cycled)
	palette := chart.PiePalette
	total := 0.0
	for _, kv := range kvs {
		total += kv.amount
	}

	values := make([]float64, len(kvs))
	colors := make([]string, len(kvs))
	slices2 := make([]pieSlice, len(kvs))
	for i, kv := range kvs {
		color := palette[i%len(palette)]
		values[i] = kv.amount
		colors[i] = color
		pct := 0
		if total > 0 {
			pct = int(math.Round(kv.amount / total * 100))
		}
		slices2[i] = pieSlice{
			Label:   kv.key,
			URL:     urls[kv.key],
			Color:   template.CSS(color), // palette is constant — no user data
			Amount:  kv.amount,
			Percent: pct,
		}
	}

	svg := chart.Pie(values, colors, 190)
	return template.HTML(svg), slices2 // #nosec G203 — SVG from our own chart.Pie
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
	ccy := displayCurrency(b)

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
		Mode:       mode,
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
		// perf.Report always uses the full series
		data.Rows, data.Met = perf.Report(grossAll, res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
	}

	lines, err := portfolio.Breakdown(b, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data.Tree = buildTree(lines, mode)
	data.Pie, data.PieSlices = buildPie(lines)

	s.render(w, http.StatusOK, "dashboard.html", data)
}

// displayCurrency mirrors the CLI rule: config "currency" else EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}
