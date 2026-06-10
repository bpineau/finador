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
	Today    domain.Date
	Label    string
	Val      portfolio.Valuation
	Curve    template.HTML
	Rows     []perf.Row
	Met      perf.Metrics
	Warnings []string
	Txs      []txRow
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
	s.renderScope(w, scope)
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
	s.renderScope(w, scope)
}

// renderScope renders the scope.html view for any Scope. The caller must hold
// at least s.mu.RLock.
func (s *Server) renderScope(w http.ResponseWriter, scope portfolio.Scope) {
	b := s.file.Book
	today := domain.Today()
	fx := market.Converter{FX: b.Market.FX}
	ccy := displayCurrency(b)
	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := scopeData{Today: today, Label: scope.Label, Val: val}
	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "gross", Color: couleurEncre, Points: res.PerfPoints(false)},
			{Label: "net", Color: couleurVert, Points: res.PerfPoints(true)},
		}, 860, 280))
		data.Rows, data.Met = perf.Report(res.PerfPoints(false), res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
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
