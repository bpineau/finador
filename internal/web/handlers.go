package web

import (
	"html/template"
	"net/http"

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
	Today    domain.Date
	Val      portfolio.Valuation
	Curve    template.HTML // SVG from chart.SVG — never raw user data
	Rows     []perf.Row
	Met      perf.Metrics
	Mode     string // group | account | asset
	Tabs     []tab
	Tree     []node
	Flat     []node
	Warnings []string
	Flash    string
	Error    string
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
	case "account", "asset":
		// valid modes
	default:
		mode = "group"
	}

	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := dashData{
		Today: today,
		Val:   val,
		Flash: r.URL.Query().Get("flash"),
		Error: r.URL.Query().Get("error"),
		Mode:  mode,
		Tabs: []tab{
			{"by group", "/", mode == "group"},
			{"by account", "/?by=account", mode == "account"},
			{"by asset", "/?by=asset", mode == "asset"},
		},
	}

	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "gross", Color: couleurEncre, Points: res.PerfPoints(false)},
			{Label: "net", Color: couleurVert, Points: res.PerfPoints(true)},
		}, 860, 300))
		data.Rows, data.Met = perf.Report(res.PerfPoints(false), res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
	}

	lines, err := portfolio.Breakdown(b, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mode == "asset" {
		data.Flat = flatAssets(lines)
	} else {
		data.Tree = buildTree(lines, mode)
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
