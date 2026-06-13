// Package market fetches and converts public market data: daily closes,
// dividends and FX, behind a pluggable Source.
package market

import (
	"context"
	"errors"

	"finador/internal/domain"
)

// ErrNotCovered means a provider cannot handle a given Ref (e.g. Yahoo with no
// Symbol, or FT with no ISIN): the Multi chain skips to the next provider.
var ErrNotCovered = errors.New("instrument not covered by this provider")

// Ref identifies an instrument to quote. Ticker-based providers (Yahoo) use
// Symbol; fund providers (Financial Times) use ISIN. A given fetch may carry
// both: each provider picks the field it understands.
type Ref struct {
	Symbol, ISIN string
}

// Source provides daily market data. finador fetches serially, politely.
type Source interface {
	// Resolve finds the best symbol for a free query: ticker, ISIN or name.
	Resolve(ctx context.Context, query string) (SymbolInfo, error)
	// Daily returns closes and dividends from `from` (inclusive) to today.
	Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error)
}

// Provider supplies a daily series for the Refs it understands. It returns
// ErrNotCovered when a Ref falls outside its scope so the chain can fall
// through to the next provider.
type Provider interface {
	Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error)
	Name() string
}

type SymbolInfo struct {
	Symbol string
	Name   string
}

type DailyData struct {
	Currency  domain.Currency // devise de cotation (meta de la place)
	Closes    []domain.PricePoint
	Dividends []domain.DividendEvent
}
