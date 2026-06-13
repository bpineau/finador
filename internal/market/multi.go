package market

import (
	"context"

	"finador/internal/domain"
)

// Multi is a Source that resolves symbols via one resolver and quotes daily
// series by trying an ordered chain of providers: the first that returns a
// non-empty series wins. Providers signalling ErrNotCovered (or any other
// error) are skipped so a later one can take over.
type Multi struct {
	resolver  Source
	providers []Provider
}

// Default is finador's standard market source: Yahoo for ticker symbols and
// resolution, with Financial Times then Morningstar (via Boursorama) as ISIN
// fallbacks for funds Yahoo lacks. Chain: Yahoo → FT → Morningstar.
func Default() *Multi {
	return &Multi{
		resolver:  NewYahoo(),
		providers: []Provider{NewYahoo(), NewFT(), NewMorningstar()},
	}
}

// Resolve delegates symbol resolution to the configured resolver (Yahoo).
func (m *Multi) Resolve(ctx context.Context, query string) (SymbolInfo, error) {
	return m.resolver.Resolve(ctx, query)
}

// Daily walks the provider chain and returns the first usable series. It
// remembers the last meaningful error (anything but ErrNotCovered) and
// returns it if no provider produces data; ErrNotFound when none even tried.
func (m *Multi) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	var lastErr error
	for _, p := range m.providers {
		d, err := p.Daily(ctx, ref, from)
		if err == nil && len(d.Closes) > 0 {
			return d, nil
		}
		if err != nil && err != ErrNotCovered {
			lastErr = err
		}
	}
	if lastErr != nil {
		return DailyData{}, lastErr
	}
	return DailyData{}, domain.ErrNotFound
}
