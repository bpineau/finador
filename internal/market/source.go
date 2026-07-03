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

// Quote is the most recent known price of an instrument. Live reports how
// fresh it is: true means a real-time market price whose Time is an intraday
// instant; false means the last daily close (a fund NAV), whose Time is that
// close's date.
type Quote struct {
	Price    float64
	Time     time.Time
	Currency domain.Currency
	Live     bool
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
	// Latest returns the freshest available price: live when the market
	// quotes one, otherwise the last daily close.
	Latest(ctx context.Context, ref Ref) (Quote, error)
}

// BatchSource is an optional Source capability: the freshest price of many
// instruments in one call. The answer is authoritative - a ref absent from
// the result means the source, all its fallbacks included, could not serve
// it - so SpotRefresh never re-asks per instrument behind a batch.
type BatchSource interface {
	LatestBatch(ctx context.Context, refs []Ref) map[Ref]Quote
}

// SymbolInfo is what Resolve learns about a free query: the canonical
// quotable symbol and the instrument's full name.
type SymbolInfo struct {
	Symbol string
	Name   string
}

// DailyData carries one instrument's raw daily history from a Source.
type DailyData struct {
	Currency  domain.Currency // quote currency (exchange metadata)
	Closes    []domain.PricePoint
	Dividends []domain.DividendEvent
}
