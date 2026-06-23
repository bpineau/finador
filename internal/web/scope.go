package web

import (
	"errors"
	"html/template"
	"net/http"
	"slices"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

type txRow struct {
	ID      domain.TxID
	Date    domain.Date
	Kind    string
	Account string
	Asset   string
	Qty     string
	Amount  string
	Note    string
}

type scopeData struct {
	Today      domain.Date
	Label      string
	Val        portfolio.Valuation
	Curve      template.HTML
	Rows       []perf.Row
	Met        perf.Metrics
	RangeLinks []tab
	Range      string
	Warnings   []string
	Txs        []txRow

	// Price history: only for a single-asset scope (a security with a quote).
	IsAsset         bool
	PriceCurve      template.HTML
	PriceRange      string
	PriceRangeLinks []tab
}

func (s *Server) scopePage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref := r.PathValue("ref")
	scope, err := portfolio.ParseScope(s.file.Book, ref)
	if err != nil {
		status := http.StatusNotFound
		if !errors.Is(err, domain.ErrNotFound) {
			status = http.StatusBadRequest
		}
		s.renderError(w, status, "unknown scope: "+ref)
		return
	}
	s.renderScope(w, r, scope)
}

func (s *Server) intersectPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	acc, err := b.Account(r.PathValue("ref"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "unknown account: "+r.PathValue("ref"))
		return
	}
	scope := portfolio.IntersectScope(acc, r.PathValue("gpath"))
	s.renderScope(w, r, scope)
}

// rangeLabels are the period shortcuts shown under each scope chart.
var rangeLabels = []string{"1m", "3m", "1y", "all"}

// rangeLinks builds a chart's period selector, preserving every OTHER query
// param so the page's two selectors (value via ?range, price via ?prange) stay
// independent. key is the param it drives; def is the label mapped to "no
// param" (the default view).
func rangeLinks(r *http.Request, key, active, def string) []tab {
	links := make([]tab, len(rangeLabels))
	for i, lab := range rangeLabels {
		q := r.URL.Query() // a fresh copy each call
		if lab == def {
			q.Del(key)
		} else {
			q.Set(key, lab)
		}
		u := "?"
		if enc := q.Encode(); enc != "" {
			u += enc
		}
		links[i] = tab{Label: lab, URL: u, Active: lab == active}
	}
	return links
}

// priceRange resolves ?prange= for the price chart. Default (absent or "1y") is
// one year; "all" is the whole series. No "1d" yet: a daily series is a single
// point over a day - that shortcut waits for intraday data.
func priceRange(r *http.Request, today domain.Date) (domain.Date, string) {
	switch r.URL.Query().Get("prange") {
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), "1m"
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), "3m"
	case "all":
		return domain.Date{}, "all"
	}
	return domain.DateOf(today.Time().AddDate(-1, 0, 0)), "1y"
}

// pricePoints converts a cached daily close series to chart points, keeping only
// those at or after `from` (zero from = the whole series).
func pricePoints(series *domain.PriceSeries, from domain.Date) []perf.Point {
	if series == nil {
		return nil
	}
	pts := make([]perf.Point, 0, len(series.Points))
	for _, p := range series.Points {
		if !from.IsZero() && p.Date.Before(from) {
			continue
		}
		pts = append(pts, perf.Point{Date: p.Date, Value: p.Close})
	}
	return pts
}

// renderScope renders the scope.html view for any Scope. The caller must hold
// at least s.mu.RLock.
func (s *Server) renderScope(w http.ResponseWriter, r *http.Request, scope portfolio.Scope) {
	b := s.file.Book
	today := domain.Today()
	fx := market.Converter{FX: b.Market.FX}
	ccy := b.DisplayCurrency()
	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	from, rangeName := chartRange(r, today)

	data := scopeData{
		Today:      today,
		Label:      scope.Label,
		Val:        val,
		Range:      rangeName,
		RangeLinks: rangeLinks(r, "range", rangeName, "all"),
	}
	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		grossAll := res.PerfPoints(false)
		netAll := res.PerfPoints(true)
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "gross", Color: couleurEncre, Points: slicePoints(grossAll, from)},
			{Label: "net", Color: couleurVert, Points: slicePoints(netAll, from)},
		}, 860, 280))
		// perf.Report always uses the full series
		data.Rows, data.Met = perf.Report(grossAll, res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
	}
	// Price history is asset-specific: a single security has a quote series; an
	// account or a group does not.
	if scope.Kind == portfolio.ByAsset && scope.Asset != nil {
		pfrom, pname := priceRange(r, today)
		data.IsAsset = true
		data.PriceRange = pname
		data.PriceRangeLinks = rangeLinks(r, "prange", pname, "1y")
		if pts := pricePoints(b.Market.Price(scope.Asset.ID), pfrom); len(pts) >= 2 {
			data.PriceCurve = template.HTML(chart.SVG([]chart.Line{
				{Label: "price " + string(scope.Asset.Currency), Color: couleurEncre, Points: pts},
			}, 860, 280))
		}
	}
	data.Txs = scopeTxs(b, scope, 15)
	s.render(w, http.StatusOK, "scope.html", data)
}

// scopeTxs lists the scope's most recent ledger lines, newest first.
func scopeTxs(b *domain.Book, scope portfolio.Scope, limit int) []txRow {
	txs := portfolio.Sorted(b)
	slices.Reverse(txs)
	var out []txRow
	for _, t := range txs {
		if len(out) == limit {
			break
		}
		if !txInScope(b, scope, t) {
			continue
		}
		row := txRow{ID: t.ID, Date: t.Date, Kind: t.Kind.String(),
			Account: accountName(b, t.Account), Qty: t.Quantity.String(),
			Amount: t.Amount.String(), Note: t.Note}
		if t.Asset != "" {
			if asset, err := b.Asset(string(t.Asset)); err == nil {
				row.Asset = asset.Name
			}
		}
		out = append(out, row)
	}
	return out
}

func txInScope(b *domain.Book, scope portfolio.Scope, t *domain.Transaction) bool {
	acc, err := b.Account(string(t.Account))
	if err != nil {
		return false
	}
	if t.Asset == "" {
		return scope.HasCash(acc)
	}
	asset, err := b.Asset(string(t.Asset))
	if err != nil {
		return false
	}
	return scope.HasAsset(acc, asset)
}

func accountName(b *domain.Book, id domain.AccountID) string {
	if acc, err := b.Account(string(id)); err == nil {
		return acc.Name
	}
	return string(id)
}
