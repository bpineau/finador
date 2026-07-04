# Promoting finador's generic market/perf logic into pofo

Date: 2026-07-04. Status: validated in discussion, pending spec review.

finador grew three pieces of genuinely generic financial machinery that pofo
should own: currency-safe fetching (the twin-listing guard), ordered
multi-identifier fallback, and the standard performance period report. This
spec covers the pofo API additions and the matching finador diet. Two repos,
one change set each; pofo first, finador follows.

Guiding rule (user requirement): pofo is a very-high-quality, idiomatic,
do-what-I-mean library. No bolted-on parameters per edge case: fix causes at
the root (resolution), extend the existing option algebra (`FetchOptions`
zero-value-useful struct, `NoSim`/`Raw`-style flags), keep functions pure and
composable.

## Lot A - pofo/marketdata

### A1. Currency-aware resolution (fixes the twin-listing bug at the root)

Today `resolveBest` picks the candidate with the deepest history, which may
be a twin listing on another exchange in another currency. finador guards
against this in three places (daily, spot, batch), each time by re-fetching
the declared ticker and rejecting the merge. The cause lives in pofo, so the
fix does too:

- `FetchOptions.Currency` keeps its meaning ("serve the series in this
  currency") but becomes smarter: resolution first prefers candidates quoted
  natively in that currency; conversion through FX crosses remains the last
  resort (current behaviour), reported through `Logf`.
- New flag `FetchOptions.NoConvert bool`, parallel to `NoSim`: "the native
  quote line matters more than history depth". With `Currency` set and
  `NoConvert` true, resolution restricts candidates to that currency and a
  fetch that still lands off-currency fails with the new sentinel
  `ErrWrongCurrency` (wrapped with symbol, got and want) instead of serving
  poison. Without `Currency`, `NoConvert` is a no-op.
- Default behaviour without these options is unchanged: deepest history wins,
  as pofo's analytics callers want.

The same pair applies to quotes through a new
`QuoteOptions{Currency string; NoConvert bool}`, carried by `LatestAny`
below - no separate optioned single-id variant: one id is a one-element
list, and `Latest(ctx, id)` stays as the zero-option shorthand. An
off-currency quote converts via `FXRate` at quote time, or errors under
`NoConvert`.

### A2. Ordered multi-identifier fallback

Every pofo consumer that knows several identifiers for one instrument (ISIN,
ticker, name) rewrites the same loop; finador has it three times. Promote:

```go
// FetchAny returns the first identifier that answers with a non-empty
// series, tried in order (most authoritative first). When every id fails,
// the errors are joined so nothing is masked.
func (c *Client) FetchAny(ctx context.Context, ids []string, opt FetchOptions) (*Series, error)

// LatestAny is the quote-side sibling.
func (c *Client) LatestAny(ctx context.Context, ids []string, opt QuoteOptions) (*Quote, error)
```

No new `Ref` type in pofo: plain ordered `[]string` composes with everything
and stays DWIM ("try these, in this order"). Skipping an id whose answer is
off-currency (under `Currency`+`NoConvert`) is part of the contract: that is
exactly the twin retry, now internal and tested once.

### A3. Dividend merge helper

```go
// MergeDividends upserts events into dst by ex-date and returns dst sorted.
func MergeDividends(dst []Dividend, events ...Dividend) []Dividend
```

Pure function next to `Series`. Note: finador's own copy operates on domain
types and stays (thin, decimal-free, different types); the promotion serves
pofo's other consumers doing incremental dividend tracking, not finador's
line count.

### finador after Lot A

- `market/pofo.go`: `Daily`/`Latest` collapse to one `FetchAny`/`LatestAny`
  call each with `Currency: asset.Currency`; the ISIN-then-symbol loops go.
- `market/refresh.go`: both currency guards and the twin retry disappear;
  an `ErrWrongCurrency` maps to the existing warning strings. Daily history
  (merged, persisted) stays strict: `NoConvert: true`.
- Spot only: `NoConvert: false`, so a last-resort converted quote keeps the
  valuation alive for a day; it is overwritten by the next real close, no
  seam ever reaches the persisted daily series. (Decision to journal in
  DECISIONS.md.)
- `SpotRefresh` keeps its batch orchestration (batch answer is authoritative)
  since that policy is finador's; only the per-quote guard moves down.

## Lot B - pofo/metrics

### B1. Standard period report

The primitives (TWR, FlowReturns, DrawdownEpisodes, Annualize) are already in
pofo; the assembly lives in finador's `perf/report.go` and is pure
(dates, values, flows) math. Promote:

```go
// Window is one named report window; To is inclusive.
type Window struct {
    Name     string
    From, To time.Time
}

// StandardWindows returns the usual trailing windows ending at `to`:
// 1d, 7d, 1m, 3m, ytd, 1y, prev-yr. Callers slice or extend freely.
func StandardWindows(to time.Time) []Window

// ReportRow is one measured window; OK is false when the window holds
// fewer than two points (nothing measurable).
type ReportRow struct {
    Window
    TWR  float64
    Gain float64 // value change net of external flows, in series units
    OK   bool
}

// ReportSummary describes the whole track record. Annualized figures are
// gated: CAGR needs MinCAGRDays of track, risk figures MinRiskDays -
// annualizing a few days compounds noise into absurdity.
type ReportSummary struct {
    TWR        float64 // cumulative since the first point
    Since      time.Time
    Days       int
    CAGR, Vol, Sharpe, Sortino float64
    HasCAGR, HasRisk           bool
    MaxDrawdown Episode
}

type ReportOptions struct {
    Windows     []Window // nil: StandardWindows(last point date)
    RiskFree    float64  // annualized
    MinRiskDays int      // 0: 90
    MinCAGRDays int      // 0: 365
}

func Report(dates []time.Time, values []float64, flows []Flow, opt ReportOptions) ([]ReportRow, ReportSummary)
```

Semantics carried over from finador (they encode real convention knowledge):

- Window slicing keeps points in [From, To] and flows strictly after From
  (a flow on the base day is already in V0).
- Gain = V_end - V_start - net flows: money made, not money added.
- Windows starting before the first point are dropped (the summary covers
  them); the summary always describes the full series given.
- "Inception" semantics belong to the caller: the summary starts at the first
  point of the series *passed in*. finador passes the ownership window
  (since-we-hold-it); pofo's own callers pass full asset history. Documented
  on Report, no flag needed.
- The flat-window dedup (a longer window bit-equal to a shorter one adds no
  information) is presentation, not measurement: it stays in finador's
  facade, applied to the returned rows.

### B2. MaxDrawdown

```go
// MaxDrawdown returns the deepest drawdown episode, or a zero Episode
// when the series never declines.
func MaxDrawdown(dates []time.Time, values []float64) Episode
```

Thin selection over `DrawdownEpisodes`; used by Report's summary.

### finador after Lot B

- `perf/report.go` shrinks to the facade: domain.Date mapping, the
  0-instead-of-NaN convention, `Names()` display order, flat-window dedup.
- `perf/periods.go`: `PeriodRange` stays (CLI parsing of period names) but
  delegates window arithmetic where sensible.
- 5d is replaced by 7d in `Names()` and in pofo's `StandardWindows`:
  5 calendar days spans only 3-4 trading sessions; "a week" means 7 calendar
  days = 5 sessions (what Yahoo's "5D" actually is). README table updated.

## Out of scope, deliberately

- Valuation, tax envelopes, cash tracking, Scope, Breakdown: finador's core
  domain, not generic.
- The USD-pivot point-in-time FX Converter (`market/convert.go`): generic but
  small, and promoting it would pull `PriceSeries.At` out of finador's
  dependency-free domain package. Revisit if a second consumer appears.
- Incremental fetch planning (`HistFrom`, history floor): tied to the Book.
- finador's batch-refresh orchestration policy (authoritative batch answers).

## Testing

- pofo: table-driven tests per new function; twin-listing scenarios use a
  fake transport (existing pofo test conventions). `Report` gets golden
  cases lifted from finador's `report_test.go` (same numbers must come out).
- finador: existing `market` and `perf` tests must pass unchanged except
  where they asserted the in-house guard internals (now pofo's job) and the
  5d→7d display change. Fake `market.Source` tests keep covering the facade.
- Both repos: full local gates (`make check` in finador, pofo's equivalent).

## Sequencing

1. pofo Lot A (resolution + options + FetchAny/LatestAny + ErrWrongCurrency
   + MergeDividends), tests, docs.
2. finador consumes Lot A (refresh.go / pofo.go diet), DECISIONS.md entry for
   the strict-daily / convert-spot split.
3. pofo Lot B (Report, StandardWindows, MaxDrawdown), tests.
4. finador consumes Lot B (perf facade diet, 7d switch, README).

Each step lands green and committed on its own; pofo is a sibling checkout
(`replace` directive), so finador always builds against the local pofo.
