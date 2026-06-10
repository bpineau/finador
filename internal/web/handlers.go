package web

import (
	"net/http"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

// pageData carries what base.html needs plus the page payload.
type pageData struct {
	Aujourdhui domain.Date
	Val        portfolio.Valuation
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	scope, err := portfolio.ParseScope(b, "")
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	today := domain.Today()
	val, err := portfolio.Value(b, scope, today, displayCurrency(b), market.Converter{FX: b.Market.FX})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, http.StatusOK, "dashboard.html", pageData{Aujourdhui: today, Val: val})
}

// displayCurrency mirrors the CLI rule: config "currency" else EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}
