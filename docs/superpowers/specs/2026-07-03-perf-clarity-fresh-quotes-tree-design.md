# Perf clarity, fresh quotes, and tree views - design

**Goal:** Make `finador perf` read like Yahoo Finance / Finary: plain numbers a
non-quant trusts at a glance, computed on quotes fresh to the hour, plus a
detailed per-envelope tree view (`perf --tree`). Aligns CLI option surfaces
while we are at it.

**Context:** The default perf table shows three columns (TWR, XIRR, GAIN).
User feedback: XIRR is opaque (an annualized, money-weighted rate reads as
"+41.98%" on one good month and looks like a bug), period rows duplicate each
other when the active track record is shorter than the window (1m = 3m = ytd =
1y, all +1.96%), and CLI quotes are refreshed at most once a day with no way
to tell how old they are. Confidence in the displayed numbers is the product's
core promise; these three issues undermine it.

## A. Perf table cleanup

**A1 - drop XIRR from the report.** The period table becomes two columns: TWR
and GAIN. `perf.Row` loses its XIRR fields, `perf.Report` stops computing it,
and the `--from` custom-window row follows. The web perf table (CLI and web
must stay behaviourally identical) drops the column too. The XIRR math itself
(`perf.XIRR`) is deleted if nothing else uses it after this change; git
remembers. README recipes mentioning XIRR are updated.

TWR keeps its name: it is now the only rate column, and it is the honest
"growth %" figure (deposits/withdrawals neutralized), i.e. exactly what
Yahoo/Finary display.

**A2 - honest period rows.** A period row is printed only when it adds
information: if a longer window has exactly the same TWR and GAIN
(float-exact comparison, the signature of a flat series stretch, e.g. a book
whose positions were all declared one month ago) as the previously kept
shorter window, the longer row is skipped. The `inception` row is always
shown, and `tracking since <date>` already gives the real measurement span.
Same rule in the web table. Add a DECISIONS.md entry (the rule, and why
float-exact equality is the right trigger).

## B. Fresh quotes (the "1d means now" semantics)

Target behaviour: any CLI command that values the portfolio works on quotes at
most 1 hour old. No staleness caption; the data is simply fresh, like the web
UI (which already runs `market.SpotRefresh`). Outside market hours the spot
price is the regular session's last price (that is what Yahoo itself shows,
and what pofo's `Latest` already returns with `Live=true`, `Time` at the
close), so "1d" naturally reads "last session's move".

**B1 - pofo: batch spot quotes.** pofo's `marketdata.Latest` is one HTTP call
per instrument. Add a batch API to pofo, e.g.
`(*Client) LatestBatch(ctx, ids []string) map[string]Quote`, that fetches all
Yahoo-quoted instruments in one request (fewer calls, less throttling), then
falls back to the existing per-id `Latest` path for ids the batch could not
serve (non-Yahoo funds via FT/Morningstar NAV, outages), inheriting all its
resilience. Primary endpoint candidate: `/v7/finance/quote?symbols=A,B,C`,
which now requires the Yahoo cookie+crumb dance (fetch cookie, then
`/v1/test/getcrumb`, cache both, refresh once on 401/403);
`/v8/finance/spark?symbols=...` is the crumb-free alternative if it proves to
carry `regularMarketPrice`/`regularMarketTime`. Decide with a live probe at
implementation time; both are behind the same `LatestBatch` signature either
way. Godoc documents the closed-market behaviour explicitly.

**B2 - finador: batch-aware Source.** `market.Source` gains a batch-latest
method (fake sources in tests implement it trivially); the pofo-backed source
wires it to `LatestBatch`. `market.SpotRefresh` switches from its serial
per-asset loop to one batch call (FX crosses included).

**B3 - CLI freshness rule.** The encrypted sidecar cache records `SpotAt`
(a `time.Time`, unlike the day-granular `FetchedAt`). `ensureFresh` keeps its
daily deep refresh, and additionally runs `SpotRefresh` when `SpotAt` is
older than 1 hour (skipped by `--offline`, as today). Spot points merge into
today's `PricePoint` as the web already does, so `Series`, `Value` and the
perf windows pick the live price up with no further change.

## C. `perf --tree`

`finador perf --tree` renders the same envelope-grouped tree as
`export --tree` (same grouping, ordering, single-item collapse, TOTAL row),
with columns: **NET** (after-tax value, from `portfolio.Breakdown`) then
**1d, 5d, 1m, 3m** as signed TWR percentages, tinted green/red like the flat
perf table, `-` where history does not cover the window.

- Per-line returns are flow-neutralized TWRs: for each (account, asset) pair,
  `portfolio.Series` over a new single-pair scope (constructor
  `portfolio.PairScope(acc, asset)`, reusing the `ByLabel` pair machinery),
  then `perf.Report` supplies the period rows. Envelope rows use a
  `ByAccount` scope (cash included); TOTAL uses the command's scope. This
  keeps every displayed number consistent with `finador perf` and the D8
  invariant (a buy is never a gain).
- Cash lines show their value and `-` for the period columns (their FX effect
  is captured by the envelope row).
- Cost: one series replay per line; negligible at personal-portfolio scale.
- Flag interactions: compatible with `[scope]`, `--label`, `--exclude`,
  `--ccy`, `--to`; `--tree` with `--from` is an error (the custom window has
  no column in the tree).

## D. CLI option consistency

Aligning the read commands on the same surface where it makes sense:

| command | gains | rationale |
|---------|-------|-----------|
| `perf` | `--tree` | this design |
| `export` | `[scope]`, `--label`, `--exclude` | today it is all-or-nothing; every other read command scopes |
| `value` | `--tree` | envelope-grouped gross/net tree, i.e. `export --tree` honoring scope/label/exclude at `--at` |
| `chart` | `--label` | the only read command missing it |

`chart --tree` makes no sense and is not added. README gets the new recipes;
`export --tree` and `value --tree` share one implementation.

## E. Ledger cleanup (data, not code)

The "Nessa Dollars -15k" line is a data-entry artifact (money-market fund
bought and mostly withdrawn through the USD pocket without the matching
deposits). No new feature needed: fix with `tx list` / `tx edit` / `tx rm`,
then `finador compact` to drop the superseded records from the journal. Done
interactively with the user on his live file once the code work lands; not
part of the implementation plan.

## Testing

- Table-driven stdlib tests, colocated, as usual; no network: fake
  `market.Source` grows the batch method.
- pofo `LatestBatch`: unit tests against a stubbed HTTP client (cookie/crumb
  renewal path included), plus its existing live-probe test conventions.
- perf table: golden-ish assertions that XIRR is gone and duplicate flat
  windows are skipped (CLI and web).
- `perf --tree`: fixture book with two envelopes, a multi-asset one and a
  single-line one, cash tracked in one; assert values, dashes on short
  history, TOTAL consistency with `finador perf`.
- `make race` after touching `web`/`store` (SpotAt lives in the store cache).

## Out of scope

- Intraday curve of the current day (roadmap: next iteration, "Brique 2").
- Any change to the on-disk ledger format (the sidecar cache is not the
  synced ledger; `SpotAt` stays out of FORMAT.md scope).
- Renaming TWR (kept, per user decision, as the single rate column).
