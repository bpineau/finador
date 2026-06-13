package market

import (
	"context"
	"fmt"
	"slices"

	"finador/internal/domain"
)

// Summary reports what a refresh fetched and what went wrong. Refresh never
// fails hard: a network problem degrades to warnings and the cache stays
// usable (stale values are flagged by the valuation layer).
type Summary struct {
	Fetched  []string
	Warnings []string
}

// Refresh updates the market cache for everything the book needs: one price
// series per security with a ticker, one FX series per currency in use.
// Series already fetched today are skipped unless force.
func Refresh(ctx context.Context, b *domain.Book, src Source, force bool) Summary {
	var sum Summary
	today := domain.Today()

	for _, asset := range b.Assets {
		if asset.Kind != domain.Security || asset.Ticker == "" {
			continue
		}
		series := b.Market.Price(asset.ID)
		if !force && !series.FetchedAt.Before(today) {
			continue
		}
		data, err := src.Daily(ctx, Ref{Symbol: asset.Ticker, ISIN: asset.ISIN}, priceFetchFrom(b, asset.ID, series))
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", asset.Ticker, err))
			continue
		}
		series.Merge(data.Closes)
		series.FetchedAt = today
		mergeDividends(&b.Market, asset.ID, data.Dividends)
		if data.Currency != "" && data.Currency != asset.Currency {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s quotes in %s but the asset is declared in %s", asset.Ticker, data.Currency, asset.Currency))
		}
		sum.Fetched = append(sum.Fetched, asset.Ticker)
	}

	for _, ccy := range neededCurrencies(b) {
		series := b.Market.FXSeries(ccy)
		if !force && !series.FetchedAt.Before(today) {
			continue
		}
		from := fxFetchFrom(b, series)
		symbol := string(ccy) + "USD=X"
		data, err := src.Daily(ctx, Ref{Symbol: symbol}, from)
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", symbol, err))
			continue
		}
		series.Merge(data.Closes)
		series.FetchedAt = today
		sum.Fetched = append(sum.Fetched, "fx "+string(ccy))
	}
	return sum
}

// priceFetchFrom picks the start of an incremental fetch: the last cached
// close (it may have moved during the session), else a week before the
// asset's first transaction, else a month back.
func priceFetchFrom(b *domain.Book, id domain.AssetID, s *domain.PriceSeries) domain.Date {
	if last, ok := s.Last(); ok {
		return last.Date
	}
	if first, ok := firstTxDate(b, func(t *domain.Transaction) bool { return t.Asset == id }); ok {
		return first.AddDays(-7)
	}
	return domain.Today().AddDays(-30)
}

func fxFetchFrom(b *domain.Book, s *domain.PriceSeries) domain.Date {
	if last, ok := s.Last(); ok {
		return last.Date
	}
	if first, ok := firstTxDate(b, func(*domain.Transaction) bool { return true }); ok {
		return first.AddDays(-7)
	}
	return domain.Today().AddDays(-30)
}

func firstTxDate(b *domain.Book, match func(*domain.Transaction) bool) (domain.Date, bool) {
	var first domain.Date
	found := false
	for _, t := range b.Transactions {
		if match(t) && (!found || t.Date.Before(first)) {
			first, found = t.Date, true
		}
	}
	return first, found
}

// neededCurrencies lists every currency the book uses except the USD pivot,
// sorted for determinism.
func neededCurrencies(b *domain.Book) []domain.Currency {
	set := map[domain.Currency]bool{}
	for _, acc := range b.Accounts {
		set[acc.Currency] = true
	}
	for _, a := range b.Assets {
		set[a.Currency] = true
	}
	delete(set, domain.USD)
	delete(set, "")
	ccys := make([]domain.Currency, 0, len(set))
	for c := range set {
		ccys = append(ccys, c)
	}
	slices.Sort(ccys)
	return ccys
}

// mergeDividends upserts events by ex-date, kept sorted.
func mergeDividends(m *domain.MarketData, id domain.AssetID, events []domain.DividendEvent) {
	if len(events) == 0 {
		return
	}
	if m.Dividends == nil {
		m.Dividends = map[domain.AssetID][]domain.DividendEvent{}
	}
	existing := m.Dividends[id]
	for _, ev := range events {
		i, found := slices.BinarySearchFunc(existing, ev.ExDate, func(e domain.DividendEvent, d domain.Date) int {
			return e.ExDate.Time().Compare(d.Time())
		})
		if found {
			existing[i] = ev
		} else {
			existing = slices.Insert(existing, i, ev)
		}
	}
	m.Dividends[id] = existing
}
