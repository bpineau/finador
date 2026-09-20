package market

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// Summary reports what a refresh fetched and what went wrong. Refresh never
// fails hard: a network problem degrades to warnings and the cache stays
// usable (stale values are flagged by the valuation layer).
type Summary struct {
	Fetched  []string
	Warnings []string
	// Actions are ready-to-paste finador commands the caller should run: a
	// warning that names a problem the tool can spell out owes the user the
	// fix, not a hint. Today they record a share split the price source has
	// already applied to its history and the ledger has not (D40, D47).
	Actions []string
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
		data, err := src.Daily(ctx, Ref{Symbol: asset.Ticker, ISIN: asset.ISIN, Currency: asset.Currency}, from)
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", asset.Ticker, err))
			continue
		}
		// Contract enforcement, not business logic: the Pofo source already
		// guarantees the declared currency (it retries the authoritative
		// ticker and rejects twin listings itself); a third-party Source
		// must not be able to poison the persisted series either. Skipping
		// leaves FetchedAt unstamped so a later run can try again.
		if data.Currency != "" && data.Currency != asset.Currency {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s quotes in %s but the asset is declared in %s: quotes ignored",
				asset.Ticker, data.Currency, asset.Currency))
			continue
		}
		// A source that RESTATES its history - a share split, a currency
		// redenomination, a class merge - answers the overlap day with a
		// different close. An incremental fetch starts at the last cached
		// point, so merging such an answer leaves the old scale in front of
		// the new one: a permanent cliff, which the value series reads as a
		// session that never happened (a 4:1 split shows as -75% in the chart
		// and in the TWR). Rebuild the whole series from the source instead.
		if factor, on, yes := restated(series, data.Closes); yes {
			deep := priceHistoryFloor(b, asset.ID)
			full, ferr := src.Daily(ctx, Ref{Symbol: asset.Ticker, ISIN: asset.ISIN, Currency: asset.Currency}, deep)
			if ferr != nil || len(full.Closes) == 0 ||
				(full.Currency != "" && full.Currency != asset.Currency) {
				sum.Warnings = append(sum.Warnings, fmt.Sprintf(
					"%s: the source restated its history (split or redenomination) and the deep re-fetch failed: quotes ignored",
					asset.Ticker))
				continue
			}
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s: history restated by the source on %s (split or redenomination) - series rebuilt from %s; check the ledger quantities",
				asset.Ticker, on, deep))
			// Name the event when it looks like a split, and hand over the
			// commands that record it: the price series is now split-adjusted
			// over its whole history and the ledger is not, so the position
			// reads at 1/N of reality until a record fixes it.
			if head, cmds := splitAdvice(b, asset, factor, on); head != "" {
				sum.Warnings = append(sum.Warnings, head)
				sum.Actions = append(sum.Actions, cmds...)
			}
			series.Points = nil
			data, from = full, deep
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
		data, err := src.Daily(ctx, Ref{Symbol: symbol, Currency: domain.USD}, from)
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", symbol, err))
			continue
		}
		// FX series hold the USD value of one unit: a cross served in any
		// other currency would corrupt every conversion.
		if data.Currency != "" && data.Currency != domain.USD {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s quotes in %s but USD expected: quotes ignored", symbol, data.Currency))
			continue
		}
		series.Merge(data.Closes)
		series.FetchedAt = today
		if series.HistFrom.IsZero() || from.Before(series.HistFrom) {
			series.HistFrom = from // remember how deep we have fetched
		}
		sum.Fetched = append(sum.Fetched, "fx "+string(ccy))
	}
	return sum
}

// restatedTolerance is how far a re-served close may sit from the cached one
// before the history counts as restated: 2%, far above a provider correcting
// a close to the cent and far below the smallest share split (3:2, -33%).
const restatedTolerance = 0.02

// restated reports whether incoming closes contradict the cached series on a
// date both cover - the signature of a source that re-scaled its history. It
// compares the FIRST shared date: an incremental fetch starts at the last
// cached point, so that date is the junction the merge would glue. On a hit
// it also returns that date and the FACTOR the source applied (cached close
// divided by the re-served one), which is the split ratio when the cause is a
// split: a 4:1 split serves every close at a quarter, so the factor is 4.
func restated(s *domain.PriceSeries, incoming []domain.PricePoint) (factor float64, on domain.Date, yes bool) {
	if s == nil || len(s.Points) == 0 {
		return 0, domain.Date{}, false
	}
	for _, p := range incoming {
		i, found := slices.BinarySearchFunc(s.Points, p.Date, func(q domain.PricePoint, d domain.Date) int {
			return q.Date.Time().Compare(d.Time())
		})
		if !found {
			continue
		}
		cached := s.Points[i].Close
		if cached <= 0 || p.Close <= 0 {
			return 0, domain.Date{}, false
		}
		if math.Abs(p.Close-cached) <= restatedTolerance*cached {
			return 0, domain.Date{}, false
		}
		return cached / p.Close, p.Date, true
	}
	return 0, domain.Date{}, false
}

// splitRatios are the share splits a restatement factor is matched against,
// as (new shares, old shares): a 4:1 split multiplies the quantity by 4 and
// divides the price by 4. Reverses are the same list inverted. Anything else
// - a currency redenomination, a class merge, a provider correcting a long
// stretch of closes - matches nothing, and nothing is then claimed.
var splitRatios = [][2]int{
	{2, 1}, {3, 1}, {4, 1}, {5, 1}, {6, 1}, {7, 1}, {8, 1}, {10, 1}, {15, 1}, {20, 1}, {50, 1}, {100, 1},
	{3, 2}, {4, 3}, {5, 2}, {5, 3}, {5, 4}, {7, 2}, {7, 5}, {9, 5},
}

// splitRatioTolerance is how far the measured factor may sit from a ratio and
// still be named: 1%, which absorbs closes rounded to the cent while leaving
// the ratios of splitRatios (the closest pair being 1.25 and 1.333) far apart.
const splitRatioTolerance = 0.01

// splitRatioFor matches a restatement factor against the usual split ratios.
// It returns the ratio as it is written ("4:1", "1:10" for a reverse split)
// and the multiplier the LEDGER quantities owe, or ok=false when the factor
// looks like no split at all.
func splitRatioFor(factor float64) (label string, quantity decimal.Decimal, ok bool) {
	if factor <= 0 || math.IsInf(factor, 0) || math.IsNaN(factor) {
		return "", decimal.Zero, false
	}
	for _, r := range splitRatios {
		n, d := float64(r[0]), float64(r[1])
		if math.Abs(factor-n/d) <= splitRatioTolerance*(n/d) {
			return fmt.Sprintf("%d:%d", r[0], r[1]),
				decimal.NewFromInt(int64(r[0])).Div(decimal.NewFromInt(int64(r[1]))), true
		}
		if math.Abs(factor-d/n) <= splitRatioTolerance*(d/n) {
			return fmt.Sprintf("%d:%d", r[1], r[0]),
				decimal.NewFromInt(int64(r[1])).Div(decimal.NewFromInt(int64(r[0]))), true
		}
	}
	return "", decimal.Zero, false
}

// splitAdvice turns a measured restatement into the sentence and the commands
// the user can act on. A split moves the POSITION as well as the price, and
// nothing but a ledger record moves the position: the source has already
// re-scaled its whole history, so every trade of the asset predating the
// restatement owes the same re-scaling.
func splitAdvice(b *domain.Book, asset *domain.Asset, factor float64, on domain.Date) (string, []string) {
	label, mult, ok := splitRatioFor(factor)
	if !ok {
		return "", nil
	}
	head := fmt.Sprintf("%s: the factor is %s, a %s split", asset.Ticker, trimRatio(factor), label)
	var cmds []string
	for _, t := range b.Transactions {
		if t.Asset != asset.ID || (t.Kind != domain.Buy && t.Kind != domain.Sell) || !t.Date.Before(on) {
			continue
		}
		q := t.Quantity.Mul(mult)
		cmds = append(cmds, fmt.Sprintf("finador tx edit %s --qty %s   # %s %s of %s on %s, was %s",
			t.ID, q.String(), t.Kind, q.String(), asset.Name, t.Date, t.Quantity.String()))
	}
	if len(cmds) == 0 {
		return head + " - no trade of this security predates it, so no quantity to restate", nil
	}
	return head + fmt.Sprintf(" - the ledger still holds the pre-split quantities of %s; restate them with:", asset.Name), cmds
}

// trimRatio renders a measured factor with at most three decimals.
func trimRatio(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// SpotSummary reports what a spot pass observed: the freshest quote per
// asset (so the UI can show how live each price is), warnings, and the
// instruments whose "current" price is not current at all.
//
// Warnings are failures - something asked for could not be had. Stale is
// the quieter, more useful signal: the pass succeeded, but the price it
// brought back is a past close, so every figure derived from it is a past
// figure. Only an explicit refresh reports Stale; a market shut for the
// weekend would otherwise make every command shout.
type SpotSummary struct {
	// Quotes is the freshest quote observed per asset. An entry may be a
	// display-only quote (Quote.DisplayOnly): an off-hours print under the
	// extended-hours opt-in (Quote.Extended), or an estimate of a fund
	// nobody has priced yet (Quote.Estimated, always). Neither was merged
	// into the price series: the caller shows it labelled, as a throwaway
	// valuation override, and the stored history keeps published prices
	// only.
	Quotes   map[domain.AssetID]Quote
	Warnings []string
	Stale    []string
}

// stale records label's quote as not-current when it is one, naming the two
// cases apart: a market that has not traded today (a weekend, a holiday, a
// halt) still answers with a live field, and calling that "no live quote"
// would be plainly false.
func (s *SpotSummary) stale(label string, q Quote) {
	d := domain.DateOf(q.Time)
	switch {
	case !q.Live:
		s.Stale = append(s.Stale, fmt.Sprintf("%s: close of %s, no live quote", label, d))
	case d.Before(domain.Today()):
		s.Stale = append(s.Stale, fmt.Sprintf("%s: last traded %s", label, d))
	}
}

// SpotRefresh updates today's price of every quoted security and FX rate
// from the source's latest quotes: one batched call when the source supports
// it, individual fallbacks otherwise, no history depth, no dividends. It
// complements Refresh (which must still run once a day) and keeps valuations
// live between two daily refreshes. It never fails hard: a failed quote
// degrades to a warning, and an instrument the source does not cover at all
// is silently skipped (its last daily close already stands).
//
// An estimate is the one quote it reports without storing: a fund priced once
// a day and published with a lag is nowcast from a proxy (Quote.Estimated),
// and such a number would otherwise sit in the cache for good and be read as
// the close of a day the fund never published. It is served in Quotes for the
// valuation to use as a labelled override, exactly like an off-hours print.
func SpotRefresh(ctx context.Context, b *domain.Book, src Source) SpotSummary {
	return spotRefresh(ctx, b, src, false)
}

// SpotRefreshExtended is SpotRefresh with the extended-hours opt-in: when the
// source supports it (ExtendedSource), a venue's pre-market or after-hours
// print is accepted for display when it is newer than the regular session's
// last price.
//
// Such a print is DISPLAYED, NEVER STORED: it is a thinner trade than a close,
// and its instant belongs to a session the persisted daily series does not
// model (an after-hours print in New York already falls on the next civil day
// in Paris). It is reported in Quotes, and nothing merges it into the price
// series, so no caller can persist it by accident.
func SpotRefreshExtended(ctx context.Context, b *domain.Book, src Source) SpotSummary {
	return spotRefresh(ctx, b, src, true)
}

func spotRefresh(ctx context.Context, b *domain.Book, src Source, extended bool) SpotSummary {
	sum := SpotSummary{Quotes: map[domain.AssetID]Quote{}}
	// Stamped even when quotes fail: an outage must not turn every command
	// into a hammering retry - the next pass tries again.
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
			ref: Ref{Symbol: asset.Ticker, ISIN: asset.ISIN, Currency: ccy},
			apply: func(q Quote) {
				// Same contract enforcement as the daily fetch: the Source
				// promises the declared currency (converting a last-resort
				// spot itself); an off-currency answer is dropped, never
				// merged.
				if q.Currency != "" && q.Currency != ccy {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf(
						"%s spot in %s but the asset is declared in %s: quote ignored", ticker, q.Currency, ccy))
					return
				}
				if q.Time.IsZero() {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: undated quote ignored", ticker))
					return
				}
				if q.DisplayOnly() {
					// Shown, never merged: an off-hours print (see
					// SpotRefreshExtended) or an estimate (see SpotSummary).
					sum.Quotes[id] = q
					return
				}
				sum.stale(ticker, q)
				b.Market.Price(id).Merge([]domain.PricePoint{{Date: domain.DateOf(q.Time), Close: q.Price}})
				sum.Quotes[id] = q
			},
		})
	}
	for _, ccy := range neededCurrencies(b) {
		series := b.Market.FXSeries(ccy)
		symbol := string(ccy) + "USD=X"
		targets = append(targets, target{
			ref: Ref{Symbol: symbol, Currency: domain.USD},
			apply: func(q Quote) {
				if q.Currency != "" && q.Currency != domain.USD {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf(
						"%s spot in %s but USD expected: quote ignored", symbol, q.Currency))
					return
				}
				if q.Time.IsZero() {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: undated quote ignored", symbol))
					return
				}
				if q.DisplayOnly() {
					return // a display-only quote never enters an FX series
				}
				sum.stale(symbol, q)
				series.Merge([]domain.PricePoint{{Date: domain.DateOf(q.Time), Close: q.Price}})
			},
		})
	}

	var batched BatchQuotes
	bs, isBatch := src.(BatchSource)
	es, isExtended := src.(ExtendedSource)
	// The opt-in degrades quietly: a source that cannot serve off-hours
	// prints keeps its regular batch rather than losing batching.
	useExtended := extended && isExtended
	if (isBatch || useExtended) && len(targets) > 0 {
		refs := make([]Ref, len(targets))
		for i, t := range targets {
			refs[i] = t.ref
		}
		if useExtended {
			batched = es.LatestBatchExtended(ctx, refs)
		} else {
			batched = bs.LatestBatch(ctx, refs)
		}
	}
	for _, t := range targets {
		q, ok := batched.Quotes[t.ref]
		if !ok {
			// A batch answer is authoritative: its misses already exhausted
			// the source's own fallbacks, re-asking one by one would only
			// repeat the slow path for the same result. The instrument's
			// last daily close stands until the next pass - but say so,
			// because a price that stopped moving looks exactly like a
			// market that stopped moving.
			if isBatch || useExtended {
				if err := batched.Errs[t.ref]; err != nil && !errors.Is(err, ErrNotCovered) {
					sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", refLabel(t.ref), err))
				}
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

// fxFetchFrom picks the start of an FX fetch on the same two-step rule as
// priceFetchFrom: (back-)fill down to the floor once, then refresh
// incrementally from the last close. The HistFrom guard matters here too - a
// record typed today may be dated years back (an old fee, a historical
// deposit), and its amount is crossed at the rate OF ITS DATE, not today's.
func fxFetchFrom(b *domain.Book, s *domain.PriceSeries) domain.Date {
	floor := fxHistoryFloor(b)
	if s.HistFrom.IsZero() || floor.Before(s.HistFrom) {
		return floor // (back-)fill down to the floor once
	}
	if last, ok := s.Last(); ok {
		return last.Date
	}
	return floor
}

// fxHistoryFloor is the earliest date an FX series must cover: a week before
// the oldest record of the book, since any record may be denominated in any
// currency. A book with no history at all gets a short window.
func fxHistoryFloor(b *domain.Book) domain.Date {
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
//
// A currency reaches the book three ways, and all three need a rate: an
// account is denominated in one, an asset quotes in one, and a RECORD may be
// written in a fourth - a fee charged in JPY, a deposit made in CHF, a
// dividend paid in USD on a EUR line, a statement declaring a balance. Listing
// only the first two left those amounts with no rate to cross at, which the
// valuation could only refuse or count as zero.
func neededCurrencies(b *domain.Book) []domain.Currency {
	set := map[domain.Currency]bool{}
	for _, acc := range b.Accounts {
		set[acc.Currency] = true
	}
	for _, a := range b.Assets {
		set[a.Currency] = true
	}
	for _, t := range b.Transactions {
		set[t.Amount.Currency] = true
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
