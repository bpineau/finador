package market

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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
		from := priceFetchFrom(b, asset.ID, series)
		data, err := src.Daily(ctx, Ref{Symbol: asset.Ticker, ISIN: asset.ISIN}, from)
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", asset.Ticker, err))
			continue
		}
		// A currency mismatch means the source served a twin listing on
		// another exchange: one such point poisons the series (valuations
		// and day moves wrong by the FX rate). Skip the merge and leave
		// FetchedAt unstamped so a later run can try again.
		if data.Currency != "" && data.Currency != asset.Currency {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s quotes in %s but the asset is declared in %s: quotes ignored",
				asset.Ticker, data.Currency, asset.Currency))
			continue
		}
		series.Merge(data.Closes)
		series.FetchedAt = today
		if series.HistFrom.IsZero() || from.Before(series.HistFrom) {
			series.HistFrom = from // remember how deep we have fetched
		}
		mergeDividends(&b.Market, asset.ID, data.Dividends)
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

// SpotSummary reports what a spot pass observed: the freshest quote per
// asset (so the UI can show how live each price is) and warnings.
type SpotSummary struct {
	Quotes   map[domain.AssetID]Quote
	Warnings []string
}

// SpotRefresh updates today's price of every quoted security and FX rate
// from the source's latest quotes: one batched call when the source supports
// it, individual fallbacks otherwise, no history depth, no dividends. It
// complements Refresh (which must still run once a day) and keeps valuations
// live between two daily refreshes. It never fails hard: a failed quote
// degrades to a warning, and an instrument the source does not cover at all
// is silently skipped (its last daily close already stands).
func SpotRefresh(ctx context.Context, b *domain.Book, src Source) SpotSummary {
	sum := SpotSummary{Quotes: map[domain.AssetID]Quote{}}
	// Stamped even when quotes fail: an outage must not turn every command
	// into a hammering retry - the next hour tries again.
	b.Market.SpotAt = time.Now()

	type target struct {
		ref   Ref
		apply func(Quote)
	}
	var targets []target
	for _, asset := range b.Assets {
		if asset.Kind != domain.Security || asset.Ticker == "" {
			continue
		}
		id, ccy, ticker := asset.ID, asset.Currency, asset.Ticker
		targets = append(targets, target{
			ref: Ref{Symbol: asset.Ticker, ISIN: asset.ISIN},
			apply: func(q Quote) {
				// Same guard as the daily fetch: a quote in another currency
				// is a twin listing, one merged point would poison the series.
				if q.Currency != "" && q.Currency != ccy {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf(
						"%s spot in %s but the asset is declared in %s: quote ignored", ticker, q.Currency, ccy))
					return
				}
				b.Market.Price(id).Merge([]domain.PricePoint{{Date: domain.DateOf(q.Time), Close: q.Price}})
				sum.Quotes[id] = q
			},
		})
	}
	for _, ccy := range neededCurrencies(b) {
		series := b.Market.FXSeries(ccy)
		targets = append(targets, target{
			ref: Ref{Symbol: string(ccy) + "USD=X"},
			apply: func(q Quote) {
				series.Merge([]domain.PricePoint{{Date: domain.DateOf(q.Time), Close: q.Price}})
			},
		})
	}

	batched := map[Ref]Quote{}
	bs, isBatch := src.(BatchSource)
	if isBatch && len(targets) > 0 {
		refs := make([]Ref, len(targets))
		for i, t := range targets {
			refs[i] = t.ref
		}
		batched = bs.LatestBatch(ctx, refs)
	}
	for _, t := range targets {
		q, ok := batched[t.ref]
		if !ok {
			// A batch answer is authoritative: its misses already exhausted
			// the source's own fallbacks, re-asking one by one would only
			// repeat the slow path for the same result. The instrument's
			// last daily close stands until the next pass.
			if isBatch {
				continue
			}
			var err error
			if q, err = src.Latest(ctx, t.ref); err != nil {
				if !errors.Is(err, ErrNotCovered) {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", refLabel(t.ref), err))
				}
				continue
			}
		}
		t.apply(q)
	}
	return sum
}

// refLabel names a ref in warnings: the symbol when there is one.
func refLabel(r Ref) string {
	if r.Symbol != "" {
		return r.Symbol
	}
	return r.ISIN
}

// priceHistoryYears is how far back a price series reaches, so the asset page's
// price chart shows years of quotes even for a recently-bought security.
const priceHistoryYears = 10

// priceFetchFrom picks the start of a fetch. We want each price series to cover
// a deep history floor (for the price chart), then refresh incrementally:
//   - not yet back-filled to the floor (HistFrom) → fetch from the floor;
//   - already deep enough → fetch from the last close (it moves intraday).
//
// HistFrom (the floor already requested, not the earliest data point) guards
// against re-fetching deep history forever when a security is younger than the
// floor.
func priceFetchFrom(b *domain.Book, id domain.AssetID, s *domain.PriceSeries) domain.Date {
	floor := priceHistoryFloor(b, id)
	if s.HistFrom.IsZero() || floor.Before(s.HistFrom) {
		return floor // (back-)fill deep history once
	}
	if last, ok := s.Last(); ok {
		return last.Date
	}
	return floor
}

// priceHistoryFloor is the earliest date we want a price series to cover: a
// generous lookback, but reaching at least a week before the first transaction
// when that is older.
func priceHistoryFloor(b *domain.Book, id domain.AssetID) domain.Date {
	deep := domain.Today().Time().AddDate(-priceHistoryYears, 0, 0)
	floor := domain.DateOf(deep)
	if first, ok := firstTxDate(b, func(t *domain.Transaction) bool { return t.Asset == id }); ok {
		if early := first.AddDays(-7); early.Before(floor) {
			return early
		}
	}
	return floor
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
