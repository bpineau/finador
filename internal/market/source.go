// Package market fetches and converts public market data: daily closes,
// dividends and FX, behind a pluggable Source.
package market

import (
	"context"

	"finador/internal/domain"
)

// Source provides daily market data. finador fetches serially, politely.
type Source interface {
	// Resolve finds the best symbol for a free query: ticker, ISIN or name.
	Resolve(ctx context.Context, query string) (SymbolInfo, error)
	// Daily returns closes and dividends from `from` (inclusive) to today.
	Daily(ctx context.Context, symbol string, from domain.Date) (DailyData, error)
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
