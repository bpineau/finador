// Package market fetches and converts public market data: daily closes,
// dividends and FX, behind a pluggable Source.
package market

import (
	"context"
	"errors"
	"time"

	"finador/internal/domain"
)

// ErrNotCovered means the source cannot handle a given Ref (e.g. an
// intraday request without a quotable symbol).
var ErrNotCovered = errors.New("instrument not covered by this provider")

// Ref identifies an instrument to quote: the ISIN is preferred when both
// are set (most precise), the symbol otherwise.
type Ref struct {
	Symbol, ISIN string
}

// IntradayPoint is one 5-minute tick in an intraday price series.
type IntradayPoint struct {
	Time  time.Time
	Close float64
}

// IntradayData carries an intraday series for one instrument.
type IntradayData struct {
	Currency domain.Currency
	Points   []IntradayPoint
}

// Source provides daily market data. finador fetches serially, politely.
// The standard implementation is Pofo (see Default).
type Source interface {
	// Resolve finds the best symbol for a free query: ticker, ISIN or name.
	Resolve(ctx context.Context, query string) (SymbolInfo, error)
	// Daily returns raw closes and dividends from `from` (inclusive) to today.
	Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error)
	// Intraday returns 5-minute ticks for the current trading day.
	Intraday(ctx context.Context, ref Ref) (IntradayData, error)
}

type SymbolInfo struct {
	Symbol string
	Name   string
}

type DailyData struct {
	Currency  domain.Currency // quote currency (exchange metadata)
	Closes    []domain.PricePoint
	Dividends []domain.DividendEvent
}
