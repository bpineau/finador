package domain

import "slices"

// PricePoint is one daily close. Closes are analytics data: float64 is fine —
// decimal exactness lives in the ledger, not in market quotes.
type PricePoint struct {
	Date  Date    `json:"d"`
	Close float64 `json:"c"`
}

// PriceSeries is a date-sorted daily close series with forward-fill lookup.
// FetchedAt records the last refresh day, even when no new point appeared
// (week-ends) — staleness is judged on it, not on the last point.
type PriceSeries struct {
	Points    []PricePoint `json:"points"`
	FetchedAt Date         `json:"fetchedAt"`
}

// At returns the last close at or before d (forward-fill), with its date.
func (s *PriceSeries) At(d Date) (float64, Date, bool) {
	if s == nil {
		return 0, Date{}, false
	}
	i, found := slices.BinarySearchFunc(s.Points, d, func(p PricePoint, t Date) int {
		return p.Date.Time().Compare(t.Time())
	})
	if found {
		return s.Points[i].Close, s.Points[i].Date, true
	}
	if i == 0 {
		return 0, Date{}, false
	}
	p := s.Points[i-1]
	return p.Close, p.Date, true
}

// Merge upserts points, keeping the series sorted and deduplicated by date.
func (s *PriceSeries) Merge(pts []PricePoint) {
	for _, p := range pts {
		i, found := slices.BinarySearchFunc(s.Points, p.Date, func(q PricePoint, t Date) int {
			return q.Date.Time().Compare(t.Time())
		})
		if found {
			s.Points[i] = p
		} else {
			s.Points = slices.Insert(s.Points, i, p)
		}
	}
}

func (s *PriceSeries) Last() (PricePoint, bool) {
	if s == nil || len(s.Points) == 0 {
		return PricePoint{}, false
	}
	return s.Points[len(s.Points)-1], true
}

// DividendEvent is one gross per-share distribution.
type DividendEvent struct {
	ExDate Date    `json:"exDate"`
	Amount float64 `json:"amount"`
}

// MarketData is the cached public market state. It lives inside the encrypted
// Book: the list of held tickers is sensitive metadata. Everything here is
// refetchable — losing it costs one refresh, never user data.
type MarketData struct {
	Prices    map[AssetID]*PriceSeries    `json:"prices,omitempty"`
	FX        map[Currency]*PriceSeries   `json:"fx,omitempty"` // valeur de 1 unité en USD
	Dividends map[AssetID][]DividendEvent `json:"dividends,omitempty"`
}

// Price returns the price series of an asset, creating it lazily.
func (m *MarketData) Price(id AssetID) *PriceSeries {
	if m.Prices == nil {
		m.Prices = map[AssetID]*PriceSeries{}
	}
	if m.Prices[id] == nil {
		m.Prices[id] = &PriceSeries{}
	}
	return m.Prices[id]
}

// FXSeries returns the USD-value series of a currency, creating it lazily.
func (m *MarketData) FXSeries(c Currency) *PriceSeries {
	if m.FX == nil {
		m.FX = map[Currency]*PriceSeries{}
	}
	if m.FX[c] == nil {
		m.FX[c] = &PriceSeries{}
	}
	return m.FX[c]
}
