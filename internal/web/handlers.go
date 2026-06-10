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

type part struct {
	Label   string
	URL     string // vide : pas de lien (liquidités, sans groupe)
	Amount  float64
	Percent int
}

type dashData struct {
	Aujourdhui domain.Date
	Val        portfolio.Valuation
	Curve      template.HTML // SVG généré par chart.SVG — jamais de donnée brute utilisateur
	Rows       []perf.Row
	Met        perf.Metrics
	Parts      []part
	Warnings   []string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	scope, _ := portfolio.ParseScope(b, "")
	fx := market.Converter{FX: b.Market.FX}
	ccy := displayCurrency(b)

	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := dashData{Aujourdhui: today, Val: val}

	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "brut", Color: couleurEncre, Points: res.PerfPoints(false)},
			{Label: "net", Color: couleurVert, Points: res.PerfPoints(true)},
		}, 860, 300))
		data.Rows, data.Met = perf.Report(res.PerfPoints(false), res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
	}

	for _, l := range val.Lines {
		p := part{Label: l.Label, Amount: l.Gross}
		if val.Gross > 0 && l.Gross > 0 {
			p.Percent = int(l.Gross/val.Gross*100 + 0.5)
		}
		if l.Label != "liquidités" && l.Label != "(sans groupe)" {
			p.URL = "/group/" + url.PathEscape(l.Label)
		}
		data.Parts = append(data.Parts, p)
	}
	s.render(w, http.StatusOK, "dashboard.html", data)
}

// displayCurrency mirrors the CLI rule: config "currency" else EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}
