package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

const (
	sparkUp   = "#1e6e4e"
	sparkDown = "#a3332e"
	sparkFlat = "#1c1914"
)

type assetRow struct {
	Name, URL                 string
	Spark1W, Spark1M, Spark1Y template.HTML
	Gross, Net                float64
}

type assetSection struct {
	Group, URL string
	Gross, Net float64
	Rows       []assetRow
}

type assetsData struct {
	Today    domain.Date
	Ccy      domain.Currency
	Sections []assetSection
	Warnings []string
}

func (s *Server) assetsPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	ccy := displayCurrency(b)
	fx := market.Converter{FX: b.Market.FX}

	data := assetsData{Today: today, Ccy: ccy}
	bySection := map[string]*assetSection{}
	var rawWarnings []string

	for _, asset := range b.Assets {
		scope, err := portfolio.ParseScope(b, string(asset.ID))
		if err != nil {
			continue
		}
		val, err := portfolio.Value(b, scope, today, ccy, fx)
		if err != nil || val.Gross == 0 {
			continue
		}
		res, err := portfolio.Series(b, scope, today.AddDays(-365), today, ccy, fx)
		if err != nil {
			continue
		}
		pts := res.PerfPoints(false)
		row := assetRow{
			Name:    asset.Name,
			URL:     "/asset/" + url.PathEscape(string(asset.ID)),
			Spark1W: spark(lastN(pts, 8)),
			Spark1M: spark(lastN(pts, 31)),
			Spark1Y: spark(pts),
			Gross:   val.Gross,
			Net:     val.Net,
		}
		g := asset.Group
		if g == "" {
			g = "(ungrouped)"
		}
		sec, ok := bySection[g]
		if !ok {
			sec = &assetSection{Group: g}
			if g != "(ungrouped)" {
				sec.URL = "/group/" + escapeGroup(strings.ToLower(g))
			}
			bySection[g] = sec
		}
		sec.Gross += val.Gross
		sec.Net += val.Net
		sec.Rows = append(sec.Rows, row)
		rawWarnings = append(rawWarnings, res.Warnings...)
	}

	for _, sec := range bySection {
		slices.SortStableFunc(sec.Rows, func(a, b assetRow) int {
			switch {
			case a.Gross > b.Gross:
				return -1
			case a.Gross < b.Gross:
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		data.Sections = append(data.Sections, *sec)
	}
	slices.SortStableFunc(data.Sections, func(a, b assetSection) int {
		switch {
		case a.Gross > b.Gross:
			return -1
		case a.Gross < b.Gross:
			return 1
		}
		return strings.Compare(a.Group, b.Group)
	})
	data.Warnings = dedupeWarnings(rawWarnings)
	s.render(w, http.StatusOK, "assets.html", data)
}

// lastN keeps the trailing n points of a daily series.
func lastN(pts []perf.Point, n int) []perf.Point {
	if len(pts) <= n {
		return pts
	}
	return pts[len(pts)-n:]
}

// spark renders a window, colored by its own drift.
func spark(pts []perf.Point) template.HTML {
	if len(pts) < 2 {
		return ""
	}
	color := sparkFlat
	switch first, last := pts[0].Value, pts[len(pts)-1].Value; {
	case last > first:
		color = sparkUp
	case last < first:
		color = sparkDown
	}
	return template.HTML(chart.Sparkline(pts, 90, 24, color))
}

// dedupeWarnings removes duplicate warning strings while preserving first-seen order.
func dedupeWarnings(ws []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range ws {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
