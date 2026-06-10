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

type onglet struct {
	Label, URL string
	Actif      bool
}

type dashData struct {
	Aujourdhui domain.Date
	Val        portfolio.Valuation
	Curve      template.HTML // SVG généré par chart.SVG — jamais de donnée brute utilisateur
	Rows       []perf.Row
	Met        perf.Metrics
	Mode       string // groupe | enveloppe | actif
	Onglets    []onglet
	Tree       []node
	Flat       []node
	Warnings   []string
	Flash      string
	Erreur     string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	scope, _ := portfolio.ParseScope(b, "")
	fx := market.Converter{FX: b.Market.FX}
	ccy := displayCurrency(b)

	mode := r.URL.Query().Get("par")
	switch mode {
	case "enveloppe", "actif":
		// valid modes
	default:
		mode = "groupe"
	}

	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := dashData{
		Aujourdhui: today,
		Val:        val,
		Flash:      r.URL.Query().Get("flash"),
		Erreur:     r.URL.Query().Get("erreur"),
		Mode:       mode,
		Onglets: []onglet{
			{"par groupe", "/", mode == "groupe"},
			{"par enveloppe", "/?par=enveloppe", mode == "enveloppe"},
			{"par actif", "/?par=actif", mode == "actif"},
		},
	}

	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "brut", Color: couleurEncre, Points: res.PerfPoints(false)},
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
	if mode == "actif" {
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
