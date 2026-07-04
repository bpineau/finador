# pofo Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote finador's generic market/perf machinery into pofo (currency-safe fetching, multi-id fallback, period report), then put finador on the resulting pofo release.

**Architecture:** pofo gains a currency constraint threaded through its resolution (internal `fetchSpec`, public `FetchOptions.NoConvert`), two multi-id entry points (`FetchAny`, `LatestAny` with native-first preference), and a `metrics.Report` assembly. finador's `market` and `perf` packages shrink to facades. Spec: `docs/superpowers/specs/2026-07-04-pofo-promotion-design.md`.

**Tech Stack:** Go stdlib only; two sibling repos (`~/projects/pofo`, `~/projects/finador`); finador builds against local pofo via its existing `replace` until the final release task removes it.

## Global Constraints

- pofo is a very-high-quality, idiomatic, DWIM library: no positional parameter creep (thread an internal spec struct), zero-value-useful option structs, typed sentinel errors, complete godoc on everything public.
- Plain hyphens everywhere, never em-dashes (code, docs, commits).
- finador invariants hold: never merge quotes in the wrong currency into the book; ledger/series semantics unchanged except the documented 5d-to-7d display switch.
- Tests: table-driven stdlib only; pofo fakes sources with `httptest` (`newTestClient`/`stubAllBases` in `pkg/marketdata/client_test.go`); finador fakes `market.Source`, never the network.
- Commit after every task, in the repo the task touches. finador gate: `make check`. pofo gate: `gofmt -l . && go vet ./... && go test ./... -count=1`.
- Currency comparisons are case-insensitive (`strings.EqualFold`); an empty currency (source did not report) always passes a constraint.

---

### Task 1: pofo - internal fetchSpec (pure refactor)

**Files:**
- Modify: `~/projects/pofo/pkg/marketdata/client.go` (fetch, fetchISIN, fetchTicker, cachedResolutionHistory, resolveBest)
- Modify: `~/projects/pofo/pkg/marketdata/extended.go` (FetchExtended call sites of c.fetch)

**Interfaces:**
- Produces: `type fetchSpec struct { raw bool; wantCurrency string; nativeOnly bool }`, and these signatures used by Task 2:
  `fetch(ctx, id string, from time.Time, spec fetchSpec)`, `fetchISIN(ctx, isin string, from time.Time, spec fetchSpec)`, `fetchTicker(ctx, ticker string, from time.Time, spec fetchSpec)`, `cachedResolutionHistory(ctx, id string, from time.Time, spec fetchSpec)`, `resolveBest(ctx, query string, from time.Time, preferBase string, spec fetchSpec)`.

- [ ] **Step 1: Add the struct and rewire signatures**

In `client.go`, next to `resolution`:

```go
// fetchSpec carries the per-request constraints threaded through the
// internal fetch path (fetch, fetchISIN/fetchTicker, resolveBest). The
// public option surface stays FetchOptions; this is its internal shadow,
// so a new constraint never grows a positional parameter.
type fetchSpec struct {
	raw          bool   // unadjusted closes (FetchOptions.Raw)
	wantCurrency string // restrict resolution to this quote currency; "" = unconstrained
	nativeOnly   bool   // only meaningful with wantCurrency (FetchOptions.NoConvert)
}
```

Mechanical rewire, zero behaviour change: replace the `raw bool` parameter of `fetch`, `fetchISIN`, `fetchTicker`, `cachedResolutionHistory` and `resolveBest` with `spec fetchSpec`; inside them, `raw` becomes `spec.raw`. Functions below the selection layer (`historyView`, `history`, `historyFT`, `historyMS`, `historyForResolution`, `cachedHistory`) keep their `raw bool`. Callers:
- `Fetch`: `return c.fetch(ctx, id, from, fetchSpec{})`
- `extended.go` (both `c.fetch(ctx, base, opt.From, opt.Raw)` call sites): `c.fetch(ctx, base, opt.From, fetchSpec{raw: opt.Raw})`

- [ ] **Step 2: Verify no behaviour change**

Run: `cd ~/projects/pofo && gofmt -l pkg && go vet ./... && go test ./pkg/marketdata -count=1`
Expected: no gofmt output, PASS (identical test set, pure refactor).

- [ ] **Step 3: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "marketdata: thread an internal fetchSpec instead of positional flags"
```

---

### Task 2: pofo - ErrWrongCurrency and NoConvert

**Files:**
- Modify: `~/projects/pofo/pkg/marketdata/client.go` (resolveBest, cachedResolutionHistory, fetchISIN, fetchTicker)
- Modify: `~/projects/pofo/pkg/marketdata/extended.go` (FetchOptions, FetchExtended)
- Test: `~/projects/pofo/pkg/marketdata/currency_guard_test.go` (create)

**Interfaces:**
- Consumes: `fetchSpec` from Task 1.
- Produces: `var ErrWrongCurrency error`; `FetchOptions.NoConvert bool`. Contract used by Tasks 3, 4, 9: `FetchExtended(ctx, id, FetchOptions{Currency: "EUR", NoConvert: true})` returns either a series natively quoted in EUR (or of unknown currency) or an error matching `errors.Is(err, ErrWrongCurrency)`.

- [ ] **Step 1: Write the failing tests**

Create `currency_guard_test.go`. A currency-parameterized chart helper plus three tests: NoConvert picks the native line over the deeper twin, NoConvert with no native line fails with ErrWrongCurrency, and a cached off-currency resolution is bypassed.

```go
package marketdata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// chartJSONCcy is chartJSON with an explicit quote currency.
func chartJSONCcy(symbol, currency string, days []time.Time, closes []float64) string {
	return strings.Replace(chartJSON(symbol, days, closes),
		`"currency":"USD"`, fmt.Sprintf(`"currency":%q`, currency), 1)
}

// twinMux serves an ISIN whose Yahoo search returns a deep USD twin and a
// shallower native EUR listing.
func twinMux(t *testing.T, isin string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[
			{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"},
			{"symbol":"NATV.PA","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("TWIN.US", "USD", testDays(200), linear(200, 50)))
	})
	mux.HandleFunc("/v8/finance/chart/NATV.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("NATV.PA", "EUR", testDays(100), linear(100, 40)))
	})
	return mux
}

func linear(n int, base float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = base + float64(i)
	}
	return out
}

func TestNoConvertPrefersNativeListing(t *testing.T) {
	const isin = "FR0000000001"
	c, srv := newTestClient(t, t.TempDir(), twinMux(t, isin))
	defer srv.Close()
	from := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	// Unconstrained: depth wins, the USD twin is served (current behaviour).
	s, err := c.FetchExtended(context.Background(), isin, FetchOptions{From: from})
	if err != nil || s.Symbol != "TWIN.US" {
		t.Fatalf("unconstrained fetch: %+v, %v", s, err)
	}

	// NoConvert EUR: the native line wins despite its shallower history.
	c2, srv2 := newTestClient(t, t.TempDir(), twinMux(t, isin))
	defer srv2.Close()
	s, err = c2.FetchExtended(context.Background(), isin,
		FetchOptions{From: from, Currency: "EUR", NoConvert: true})
	if err != nil || s.Symbol != "NATV.PA" || s.Currency != "EUR" {
		t.Fatalf("NoConvert fetch: %+v, %v", s, err)
	}
}

func TestNoConvertFailsWithoutNativeLine(t *testing.T) {
	const isin = "FR0000000002"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("TWIN.US", "USD", testDays(200), linear(200, 50)))
	})
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()
	from := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := c.FetchExtended(context.Background(), isin,
		FetchOptions{From: from, Currency: "EUR", NoConvert: true})
	if !errors.Is(err, ErrWrongCurrency) {
		t.Fatalf("want ErrWrongCurrency, got: %v", err)
	}
}

func TestNoConvertBypassesOffCurrencyCachedResolution(t *testing.T) {
	const isin = "FR0000000003"
	dir := t.TempDir()
	c, srv := newTestClient(t, dir, twinMux(t, isin))
	from := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	// Adopt the USD twin unconstrained; the resolution is cached on disk.
	if s, err := c.Fetch(context.Background(), isin, from); err != nil || s.Symbol != "TWIN.US" {
		t.Fatalf("seeding fetch: %v", err)
	}
	srv.Close()

	// A NoConvert EUR call on a fresh client (same cache dir) must NOT
	// reuse the cached USD resolution: it re-resolves, restricted.
	mux := twinMux(t, isin)
	srv2 := newTestServerFor(t, mux)
	defer srv2.Close()
	c2 := NewClient(dir)
	stubAllBases(c2, srv2.URL)
	s, err := c2.FetchExtended(context.Background(), isin,
		FetchOptions{From: from, Currency: "EUR", NoConvert: true})
	if err != nil || s.Symbol != "NATV.PA" {
		t.Fatalf("cached twin resolution not bypassed: %+v, %v", s, err)
	}
}

func newTestServerFor(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	return httptest.NewServer(mux)
}
```

(Adjust the `httptest` import; if `newTestClient` already fits the third test, drop `newTestServerFor`.)

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -run 'NoConvert' -v`
Expected: compile FAIL - `undefined: ErrWrongCurrency`, unknown field `NoConvert`.

- [ ] **Step 3: Implement**

`extended.go`, in `FetchOptions` after `Currency`:

```go
	// NoConvert, with Currency set, demands the native quote line: the
	// resolution is restricted to listings quoted in Currency and the fetch
	// fails with ErrWrongCurrency when none exists, instead of converting a
	// twin listing through FX crosses. Callers that merge incremental
	// fetches into a persistent series want this: converted twin closes
	// never splice cleanly into a native history. Without Currency it is a
	// no-op.
	NoConvert bool
```

`client.go`, next to the other package errors (or a new `errors.go` if none groups them):

```go
// ErrWrongCurrency reports that no source could serve the instrument
// natively in the requested currency (FetchOptions.Currency combined with
// NoConvert, or QuoteOptions likewise). Detect it with errors.Is.
var ErrWrongCurrency = errors.New("no native quote line in the requested currency")
```

A shared helper in `client.go`:

```go
// currencyOK reports whether a quote currency satisfies the constraint:
// empty on either side always passes (a source that does not report its
// currency cannot be judged), comparison is case-insensitive.
func (spec fetchSpec) currencyOK(currency string) bool {
	return !spec.nativeOnly || spec.wantCurrency == "" || currency == "" ||
		strings.EqualFold(currency, spec.wantCurrency)
}
```

Wire it in four places:

1. `resolveBest`: in the candidate loop, right after each successful `historyView`/`historyFT`/`historyMS`, before `consider`:

```go
		if !spec.currencyOK(s.Currency) {
			failures = append(failures, fmt.Sprintf("%s: quotes in %s, want %s",
				s.Symbol, s.Currency, spec.wantCurrency))
			continue
		}
```

(for the FT and Morningstar branches, same guard around their `consider` calls).

2. `cachedResolutionHistory`: first thing, bypass an off-currency cached resolution:

```go
	res, ok := c.loadResolution(id)
	if !ok {
		return nil, false
	}
	if !spec.currencyOK(res.Currency) {
		c.Logf("cached resolution for %s is quoted in %s, want %s: resolving again…",
			id, res.Currency, spec.wantCurrency)
		return nil, false
	}
```

and after a successful `historyForResolution`, apply the same check to `s.Currency` (a legacy cache file may lack the currency).

3. `fetchISIN` and `fetchTicker`: final safety net before returning a series (covers the direct-ticker path, which skips resolveBest):

```go
	if s != nil && !spec.currencyOK(s.Currency) {
		return nil, fmt.Errorf("%s: %w: got %s, want %s",
			id, ErrWrongCurrency, s.Currency, spec.wantCurrency)
	}
```

In `fetchTicker` this replaces the bare `return direct, nil` fast path too.

4. `FetchExtended`: build the spec and skip conversion under NoConvert:

```go
	spec := fetchSpec{raw: opt.Raw}
	if opt.NoConvert && opt.Currency != "" {
		spec.wantCurrency, spec.nativeOnly = opt.Currency, true
	}
```

pass `spec` to both `c.fetch` call sites, and make `convertTo` a no-op when `opt.NoConvert` (the series is already native or the fetch failed).

- [ ] **Step 4: Run the new and old tests**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -count=1`
Expected: PASS, including the three new tests.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "marketdata: NoConvert restricts resolution to the native currency line"
```

---

### Task 3: pofo - FetchAny

**Files:**
- Create: `~/projects/pofo/pkg/marketdata/any.go`
- Test: `~/projects/pofo/pkg/marketdata/any_test.go`

**Interfaces:**
- Consumes: `FetchExtended`, `FetchOptions.NoConvert`, `ErrWrongCurrency` (Task 2).
- Produces: `func (c *Client) FetchAny(ctx context.Context, ids []string, opt FetchOptions) (*Series, error)` - used by finador in Task 9.

- [ ] **Step 1: Write the failing tests**

`any_test.go`:

```go
package marketdata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestFetchAnyTriesIdsInOrder(t *testing.T) {
	mux := http.NewServeMux()
	// The ISIN is unknown everywhere; the ticker answers directly.
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[]}`)
	})
	mux.HandleFunc("/v8/finance/chart/VOO", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSON("VOO", testDays(100), linear(100, 300)))
	})
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()
	from := time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)

	s, err := c.FetchAny(context.Background(), []string{"LU0000000009", "VOO"}, FetchOptions{From: from})
	if err != nil || s.Symbol != "VOO" {
		t.Fatalf("FetchAny: %+v, %v", s, err)
	}
}

func TestFetchAnyJoinsErrors(t *testing.T) {
	mux := http.NewServeMux() // nothing answers anything
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()

	_, err := c.FetchAny(context.Background(), []string{"LU0000000009", "NOPE"}, FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "LU0000000009") || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("joined errors should name every id: %v", err)
	}
}

func TestFetchAnyPrefersNativeAcrossIds(t *testing.T) {
	// The ISIN resolves to the deep USD twin only; the declared ticker
	// serves the native EUR line. With Currency set (and no NoConvert),
	// the native answer must win without conversion.
	const isin = "FR0000000004"
	mux := twinMuxISINOnlyUSD(t, isin) // search: TWIN.US only; chart NATV.PA served directly
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()
	from := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	s, err := c.FetchAny(context.Background(), []string{isin, "NATV.PA"},
		FetchOptions{From: from, Currency: "EUR"})
	if err != nil || s.Symbol != "NATV.PA" || s.Currency != "EUR" {
		t.Fatalf("native-first: %+v, %v", s, err)
	}
}

func twinMuxISINOnlyUSD(t *testing.T, isin string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("TWIN.US", "USD", testDays(200), linear(200, 50)))
	})
	mux.HandleFunc("/v8/finance/chart/NATV.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("NATV.PA", "EUR", testDays(100), linear(100, 40)))
	})
	return mux
}
```

(add `"strings"` to imports.)

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -run FetchAny -v`
Expected: compile FAIL - `c.FetchAny undefined`.

- [ ] **Step 3: Implement `any.go`**

```go
package marketdata

import (
	"context"
	"errors"
	"fmt"
)

// FetchAny fetches the first identifier that answers with a usable series,
// tried in order (most authoritative first) - the natural call when one
// instrument is known under several identifiers (ISIN, ticker, name).
//
// With opt.Currency set, the ids are first scanned for a series natively
// quoted in that currency: an ISIN resolution may favor a deeper twin
// listing on another exchange, and the caller's own ticker often is the
// native line. Only when no id answers natively does FetchAny fall back to
// the single-id semantics: ErrWrongCurrency under NoConvert, conversion of
// the most authoritative answer otherwise. The client's per-run
// memoization makes the second pass cheap.
//
// When every id fails, the errors are joined so no cause is masked.
func (c *Client) FetchAny(ctx context.Context, ids []string, opt FetchOptions) (*Series, error) {
	if len(ids) == 0 {
		return nil, errors.New("FetchAny: no identifier")
	}
	if opt.Currency == "" {
		return c.fetchFirst(ctx, ids, opt)
	}
	native := opt
	native.NoConvert = true
	s, nativeErr := c.fetchFirst(ctx, ids, native)
	if nativeErr == nil {
		return s, nil
	}
	if opt.NoConvert {
		return nil, nativeErr
	}
	return c.fetchFirst(ctx, ids, opt)
}

// fetchFirst returns the first id FetchExtended serves with at least one
// point, joining every failure otherwise.
func (c *Client) fetchFirst(ctx context.Context, ids []string, opt FetchOptions) (*Series, error) {
	var errs []error
	for _, id := range ids {
		s, err := c.FetchExtended(ctx, id, opt)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(s.Points) == 0 {
			errs = append(errs, fmt.Errorf("%s: empty series", id))
			continue
		}
		return s, nil
	}
	return nil, errors.Join(errs...)
}
```

- [ ] **Step 4: Run tests**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "marketdata: FetchAny, ordered multi-identifier fetch with native-first currency preference"
```

---

### Task 4: pofo - QuoteOptions and LatestAny

**Files:**
- Modify: `~/projects/pofo/pkg/marketdata/any.go`
- Test: `~/projects/pofo/pkg/marketdata/any_test.go` (append)

**Interfaces:**
- Consumes: `Latest`, `FXRate(ctx, from, to string, at time.Time) (float64, error)`, `ErrWrongCurrency`.
- Produces: `type QuoteOptions struct { Currency string; NoConvert bool }`; `func (c *Client) LatestAny(ctx context.Context, ids []string, opt QuoteOptions) (*Quote, error)` - used by finador in Task 9.

- [ ] **Step 1: Write the failing tests**

Append to `any_test.go` (the spot endpoint mirrors what `fetchYahooSpot` reads: the chart meta's `regularMarketPrice`; reuse the chart handlers, `Latest` falls back to the last daily close, which carries the series currency - that is all these tests need):

```go
func TestLatestAnySkipsOffCurrencyQuote(t *testing.T) {
	const isin = "FR0000000005"
	mux := twinMuxISINOnlyUSD(t, isin)
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()

	q, err := c.LatestAny(context.Background(), []string{isin, "NATV.PA"},
		QuoteOptions{Currency: "EUR"})
	if err != nil || q.Currency != "EUR" || q.Symbol != "NATV.PA" {
		t.Fatalf("LatestAny native-first: %+v, %v", q, err)
	}
}

func TestLatestAnyNoConvertRejects(t *testing.T) {
	const isin = "FR0000000006"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("TWIN.US", "USD", testDays(200), linear(200, 50)))
	})
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()

	_, err := c.LatestAny(context.Background(), []string{isin},
		QuoteOptions{Currency: "EUR", NoConvert: true})
	if !errors.Is(err, ErrWrongCurrency) {
		t.Fatalf("want ErrWrongCurrency, got: %v", err)
	}
}

func TestLatestAnyConvertsAsLastResort(t *testing.T) {
	const isin = "FR0000000007"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("TWIN.US", "USD", testDays(200), linear(200, 50)))
	})
	// Serve the FX cross under both spellings so the test does not depend
	// on FXRate's internal symbol choice.
	fx := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("USDEUR=X", "EUR", testDays(400), constant(400, 0.5)))
	}
	mux.HandleFunc("/v8/finance/chart/USDEUR=X", fx)
	mux.HandleFunc("/v8/finance/chart/EURUSD=X", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartJSONCcy("EURUSD=X", "USD", testDays(400), constant(400, 2)))
	})
	c, srv := newTestClient(t, t.TempDir(), mux)
	defer srv.Close()

	q, err := c.LatestAny(context.Background(), []string{isin}, QuoteOptions{Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	// Last USD close is 50+199 = 249; at 0.5 EUR per USD: 124.5.
	if q.Currency != "EUR" || q.Price != 124.5 {
		t.Fatalf("converted quote: %+v", q)
	}
}

func constant(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}
```

(The FX test days must cover the quote's Time; `testDays(400)` starts 2020-01-06, and the twin's last close is inside that range. If `FXRate` holds the earliest rate flat, a constant series makes the expectation date-proof.)

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -run LatestAny -v`
Expected: compile FAIL - `QuoteOptions`/`LatestAny` undefined.

- [ ] **Step 3: Implement in `any.go`**

```go
// QuoteOptions constrains LatestAny. The zero value keeps every default:
// first identifier that answers wins, in its native currency.
type QuoteOptions struct {
	// Currency demands the quote in this ISO 4217 currency: identifiers
	// answering natively in it win; otherwise the most authoritative
	// answer is converted through FXRate at the quote's time.
	Currency string
	// NoConvert, with Currency set, fails with ErrWrongCurrency instead
	// of converting an off-currency quote.
	NoConvert bool
}

// LatestAny returns the freshest price for the first identifier that
// answers, tried in order (most authoritative first). See FetchAny for the
// native-first currency contract; quotes convert at their own timestamp.
func (c *Client) LatestAny(ctx context.Context, ids []string, opt QuoteOptions) (*Quote, error) {
	if len(ids) == 0 {
		return nil, errors.New("LatestAny: no identifier")
	}
	var errs []error
	var offCurrency *Quote
	for _, id := range ids {
		q, err := c.Latest(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if opt.Currency == "" || q.Currency == "" || strings.EqualFold(q.Currency, opt.Currency) {
			return q, nil
		}
		if offCurrency == nil {
			offCurrency = q
		}
		errs = append(errs, fmt.Errorf("%s: %w: got %s, want %s",
			id, ErrWrongCurrency, q.Currency, opt.Currency))
	}
	if offCurrency != nil && !opt.NoConvert {
		rate, err := c.FXRate(ctx, offCurrency.Currency, opt.Currency, offCurrency.Time)
		if err != nil {
			return nil, errors.Join(append(errs, err)...)
		}
		q := *offCurrency
		q.Price *= rate
		q.Currency = opt.Currency
		return &q, nil
	}
	return nil, errors.Join(errs...)
}
```

(add `"strings"` to `any.go` imports.)

- [ ] **Step 4: Run tests**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "marketdata: LatestAny, multi-identifier quotes with currency assurance"
```

---

### Task 5: pofo - MergeDividends

**Files:**
- Modify: `~/projects/pofo/pkg/marketdata/types.go`
- Test: `~/projects/pofo/pkg/marketdata/types_test.go` (append)

**Interfaces:**
- Produces: `func MergeDividends(dst []Dividend, events ...Dividend) []Dividend`.

- [ ] **Step 1: Write the failing test**

```go
func TestMergeDividends(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC) }
	got := MergeDividends(nil, Dividend{Date: d(10), Amount: 1})
	got = MergeDividends(got,
		Dividend{Date: d(5), Amount: 2},        // insert before
		Dividend{Date: d(10), Amount: 1.5},     // upsert existing
		Dividend{Date: d(20), Amount: 3},       // append after
	)
	want := []Dividend{{d(5), 2}, {d(10), 1.5}, {d(20), 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

(add `"reflect"` to the test imports.)

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/marketdata -run MergeDividends -v`
Expected: compile FAIL - `MergeDividends undefined`.

- [ ] **Step 3: Implement in `types.go`**

```go
// MergeDividends upserts events into dst by ex-date (one event per date,
// the newcomer wins) and returns dst sorted ascending. dst may be nil; the
// natural building block for incremental dividend tracking alongside
// Series.Dividends.
func MergeDividends(dst []Dividend, events ...Dividend) []Dividend {
	for _, ev := range events {
		i, found := slices.BinarySearchFunc(dst, ev.Date, func(d Dividend, t time.Time) int {
			return d.Date.Compare(t)
		})
		if found {
			dst[i] = ev
		} else {
			dst = slices.Insert(dst, i, ev)
		}
	}
	return dst
}
```

(add `"slices"` to `types.go` imports.)

- [ ] **Step 4: Run tests, then the full pofo gate**

Run: `cd ~/projects/pofo && gofmt -l pkg && go vet ./... && go test ./... -count=1`
Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "marketdata: MergeDividends, sorted upsert of dividend events"
```

---

### Task 6: pofo - metrics.MaxDrawdown

**Files:**
- Modify: `~/projects/pofo/pkg/metrics/episodes.go`
- Test: `~/projects/pofo/pkg/metrics/episodes_test.go` (append; create if missing)

**Interfaces:**
- Consumes: `DrawdownEpisodes`, `Episode`.
- Produces: `func MaxDrawdown(dates []time.Time, values []float64) Episode` - used by Task 8 and finador Task 10.

- [ ] **Step 1: Write the failing test**

```go
func TestMaxDrawdown(t *testing.T) {
	d := func(i int) time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i) }
	dates := []time.Time{d(0), d(1), d(2), d(3), d(4), d(5)}
	// Two episodes: -10% (recovered), then -20% (ongoing). The deepest wins.
	values := []float64{100, 90, 101, 102, 90, 81.6}
	ep := MaxDrawdown(dates, values)
	if !ep.Ongoing || ep.PeakDate != d(3) || math.Abs(ep.Depth-(-0.2)) > 1e-9 {
		t.Fatalf("wrong episode: %+v", ep)
	}
	// A rising series has no drawdown: the zero Episode.
	if ep := MaxDrawdown(dates[:2], []float64{1, 2}); ep.Depth != 0 || !ep.PeakDate.IsZero() {
		t.Fatalf("expected zero episode, got %+v", ep)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/metrics -run MaxDrawdown -v`
Expected: compile FAIL.

- [ ] **Step 3: Implement in `episodes.go`**

```go
// MaxDrawdown returns the deepest drawdown episode of a value series, or
// the zero Episode when the series never declines. Equal depths keep the
// earlier episode.
func MaxDrawdown(dates []time.Time, values []float64) Episode {
	var worst Episode
	for _, ep := range DrawdownEpisodes(dates, values) {
		if ep.Depth < worst.Depth {
			worst = ep
		}
	}
	return worst
}
```

- [ ] **Step 4: Run tests**

Run: `cd ~/projects/pofo && go test ./pkg/metrics -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "metrics: MaxDrawdown, deepest episode selection"
```

---

### Task 7: pofo - metrics.Window and StandardWindows

**Files:**
- Create: `~/projects/pofo/pkg/metrics/report.go`
- Test: `~/projects/pofo/pkg/metrics/report_test.go` (create)

**Interfaces:**
- Produces: `type Window struct { Name string; From, To time.Time }`; `func StandardWindows(to time.Time) []Window` - used by Task 8 and finador Task 10.

- [ ] **Step 1: Write the failing test**

```go
package metrics

import (
	"testing"
	"time"
)

func TestStandardWindows(t *testing.T) {
	to := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	ws := StandardWindows(to)
	byName := map[string]Window{}
	for _, w := range ws {
		byName[w.Name] = w
	}
	day := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name     string
		from, to time.Time
	}{
		{"1d", day(2026, 7, 3), to},
		{"7d", day(2026, 6, 27), to},
		{"1m", day(2026, 6, 4), to},
		{"3m", day(2026, 4, 4), to},
		{"ytd", day(2025, 12, 31), to},
		{"1y", day(2025, 7, 4), to},
		{"prev-yr", day(2024, 12, 31), day(2025, 12, 31)},
	}
	if len(ws) != len(cases) {
		t.Fatalf("window count: %d", len(ws))
	}
	for _, c := range cases {
		w, ok := byName[c.name]
		if !ok || !w.From.Equal(c.from) || !w.To.Equal(c.to) {
			t.Errorf("%s: got %+v, want [%s, %s]", c.name, w, c.from, c.to)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/metrics -run StandardWindows -v`
Expected: compile FAIL.

- [ ] **Step 3: Implement `report.go`**

```go
package metrics

import "time"

// Window is one named window of a period report; To is inclusive. The
// value at From is the comparison base of the window, so a "ytd" window
// starts on Dec 31 of the previous year.
type Window struct {
	Name     string
	From, To time.Time
}

// StandardWindows returns the usual trailing report windows ending at to:
// 1d, 7d, 1m, 3m, ytd, 1y and prev-yr (the last full calendar year). 7d -
// one calendar week - covers five trading sessions, what "a week" means to
// a human (and what finance UIs label 5D). Month and year arithmetic
// follows Go's AddDate normalization. Callers slice, filter or extend the
// result freely before passing it to Report.
func StandardWindows(to time.Time) []Window {
	dec31 := func(year int) time.Time {
		return time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	}
	return []Window{
		{Name: "1d", From: to.AddDate(0, 0, -1), To: to},
		{Name: "7d", From: to.AddDate(0, 0, -7), To: to},
		{Name: "1m", From: to.AddDate(0, -1, 0), To: to},
		{Name: "3m", From: to.AddDate(0, -3, 0), To: to},
		{Name: "ytd", From: dec31(to.Year() - 1), To: to},
		{Name: "1y", From: to.AddDate(-1, 0, 0), To: to},
		{Name: "prev-yr", From: dec31(to.Year() - 2), To: dec31(to.Year() - 1)},
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd ~/projects/pofo && go test ./pkg/metrics -run StandardWindows -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "metrics: Window and StandardWindows, the standard report windows"
```

---

### Task 8: pofo - metrics.Report

**Files:**
- Modify: `~/projects/pofo/pkg/metrics/report.go`
- Test: `~/projects/pofo/pkg/metrics/report_test.go` (append)

**Interfaces:**
- Consumes: `TWR`, `FlowReturns`, `Volatility`, `Sharpe`, `Sortino`, `Annualize`, `MaxDrawdown`, `Flow`, `Window`.
- Produces (used by finador Task 10):

```go
type ReportRow struct { Window; TWR, Gain float64; OK bool }
type ReportSummary struct {
	TWR   float64
	Since time.Time
	Days  int
	CAGR, Vol, Sharpe, Sortino float64
	HasCAGR, HasRisk           bool
	MaxDrawdown                Episode
}
type ReportOptions struct {
	Windows     []Window
	RiskFree    float64
	MinRiskDays int
	MinCAGRDays int
}
func Report(dates []time.Time, values []float64, flows []Flow, opt ReportOptions) ([]ReportRow, ReportSummary)
```

- [ ] **Step 1: Write the failing tests**

Append to `report_test.go`:

```go
func TestReportRowsAndSummary(t *testing.T) {
	d0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(i int) time.Time { return d0.AddDate(0, 0, i) }
	// Three points, one 5-unit inflow on day 1:
	// r1 = (106-5)/100 = 1.01 ; r2 = 110/106 ; TWR = 1.01*110/106 - 1.
	dates := []time.Time{day(0), day(1), day(2)}
	values := []float64{100, 106, 110}
	flows := []Flow{{Date: day(1), Amount: 5}}
	w := Window{Name: "all", From: day(0), To: day(2)}

	rows, sum := Report(dates, values, flows, ReportOptions{Windows: []Window{w}})
	if len(rows) != 1 || !rows[0].OK {
		t.Fatalf("rows: %+v", rows)
	}
	wantTWR := 1.01*110/106 - 1
	if math.Abs(rows[0].TWR-wantTWR) > 1e-12 || rows[0].Gain != 5 {
		t.Fatalf("row: %+v, want TWR %v gain 5", rows[0], wantTWR)
	}
	if math.Abs(sum.TWR-wantTWR) > 1e-12 || !sum.Since.Equal(day(0)) || sum.Days != 2 {
		t.Fatalf("summary: %+v", sum)
	}
	// 2 days of track: neither risk nor CAGR figures are meaningful.
	if sum.HasRisk || sum.HasCAGR {
		t.Fatalf("short track must gate annualized figures: %+v", sum)
	}
}

func TestReportBaseDayFlowIsInV0(t *testing.T) {
	d0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(i int) time.Time { return d0.AddDate(0, 0, i) }
	dates := []time.Time{day(0), day(1)}
	values := []float64{100, 101}
	// A flow ON the window base day is already part of V0: not neutralized.
	flows := []Flow{{Date: day(0), Amount: 100}}
	rows, _ := Report(dates, values, flows,
		ReportOptions{Windows: []Window{{Name: "w", From: day(0), To: day(1)}}})
	if math.Abs(rows[0].TWR-0.01) > 1e-12 || rows[0].Gain != 1 {
		t.Fatalf("base-day flow must not be neutralized: %+v", rows[0])
	}
}

func TestReportDropsPreOriginWindowsAndGatesLongFigures(t *testing.T) {
	d0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 400 // enough for both MinRiskDays and MinCAGRDays defaults
	dates := make([]time.Time, n)
	values := make([]float64, n)
	for i := range dates {
		dates[i] = d0.AddDate(0, 0, i)
		values[i] = 100 * math.Pow(1.0005, float64(i))
	}
	rows, sum := Report(dates, values, nil, ReportOptions{})
	// Default windows end at the last point (2025-02-03). Six standard
	// windows fit; prev-yr [2023-12-31, 2024-12-31] pre-dates the origin
	// and must be dropped (the summary covers it).
	if len(rows) != 6 {
		t.Fatalf("expected 6 windows (prev-yr dropped), got %d: %+v", len(rows), rows)
	}
	if !sum.HasRisk || !sum.HasCAGR || !(sum.CAGR > 0) || !(sum.Vol >= 0) {
		t.Fatalf("long track must fill annualized figures: %+v", sum)
	}
	// Empty input: nothing measurable, no panic.
	if r, s := Report(nil, nil, nil, ReportOptions{}); r != nil || !s.Since.IsZero() {
		t.Fatalf("empty input must return zero values: %+v %+v", r, s)
	}
}
```

(`report_test.go` needs `"math"` added to its imports for these tests.)

- [ ] **Step 2: Run to verify failure**

Run: `cd ~/projects/pofo && go test ./pkg/metrics -run Report -v`
Expected: compile FAIL.

- [ ] **Step 3: Implement in `report.go`**

```go
// ReportRow is one measured window of a period report. OK is false when
// the window holds fewer than two points: nothing measurable, the zero
// figures are meaningless and must not be displayed.
type ReportRow struct {
	Window
	TWR  float64 // time-weighted return over the window
	Gain float64 // value change net of external flows, in series units
	OK   bool
}

// ReportSummary describes the whole track record of the series passed to
// Report - "inception" semantics therefore belong to the caller: pass the
// ownership window to measure a holding, the full history to measure the
// asset. The annualized figures are gated (HasCAGR, HasRisk): annualizing
// a return earned over days compounds noise into absurdity.
type ReportSummary struct {
	TWR   float64   // cumulative TWR since the first point
	Since time.Time // first point of the series
	Days  int       // calendar span of the series

	CAGR, Vol, Sharpe, Sortino float64
	HasCAGR, HasRisk           bool

	MaxDrawdown Episode
}

// ReportOptions parameterizes Report. The zero value keeps every default:
// StandardWindows of the last point, no risk-free rate, the customary
// track-record gates.
type ReportOptions struct {
	Windows     []Window // nil: StandardWindows(last point date)
	RiskFree    float64  // annualized risk-free rate for Sharpe and Sortino
	MinRiskDays int      // track needed for Vol/Sharpe/Sortino; 0: 90
	MinCAGRDays int      // track needed for CAGR; 0: 365
}

// Track-record floors under which annualized figures are hidden: about a
// quarter of daily returns for the risk statistics, a full year for a
// compound *annual* growth rate.
const (
	defaultMinRiskDays = 90
	defaultMinCAGRDays = 365
)

// Report builds the standard period table plus summary statistics for a
// daily value series with external flows. Windows whose From pre-dates the
// first point are dropped (the summary covers them); windows are otherwise
// reported even when flat. Empty or single-point input returns
// (nil, zero ReportSummary): nothing measurable, no error.
func Report(dates []time.Time, values []float64, flows []Flow, opt ReportOptions) ([]ReportRow, ReportSummary) {
	if len(dates) != len(values) || len(values) < 2 {
		return nil, ReportSummary{}
	}
	windows := opt.Windows
	if windows == nil {
		windows = StandardWindows(dates[len(dates)-1])
	}
	minRisk, minCAGR := opt.MinRiskDays, opt.MinCAGRDays
	if minRisk == 0 {
		minRisk = defaultMinRiskDays
	}
	if minCAGR == 0 {
		minCAGR = defaultMinCAGRDays
	}

	origin := dates[0]
	var rows []ReportRow
	for _, w := range windows {
		if w.From.Before(origin) {
			continue
		}
		rows = append(rows, reportRow(w, dates, values, flows))
	}

	twr, _ := TWR(dates, values, flows)
	returns := FlowReturns(dates, values, flows)
	days := int(math.Round(dates[len(dates)-1].Sub(origin).Hours() / 24))
	sum := ReportSummary{
		TWR:         twr,
		Since:       origin,
		Days:        days,
		MaxDrawdown: MaxDrawdown(dates, values),
	}
	if days >= minRisk && len(returns) >= 2 {
		sum.Vol = Volatility(returns)
		sum.Sharpe = Sharpe(returns, opt.RiskFree)
		sum.Sortino = Sortino(returns, opt.RiskFree)
		sum.HasRisk = true
	}
	if days >= minCAGR {
		sum.CAGR = Annualize(twr, days)
		sum.HasCAGR = true
	}
	return rows, sum
}

func reportRow(w Window, dates []time.Time, values []float64, flows []Flow) ReportRow {
	d, v, f := windowSlice(dates, values, flows, w.From, w.To)
	row := ReportRow{Window: w}
	twr, ok := TWR(d, v, f)
	if !ok {
		return row
	}
	net := 0.0
	for _, fl := range f {
		net += fl.Amount
	}
	row.TWR, row.Gain, row.OK = twr, v[len(v)-1]-v[0]-net, true
	return row
}

// windowSlice keeps the points dated in [from, to] and the flows strictly
// after from and at or before to: a flow on the base day is already part
// of V0 and must not be neutralized a second time.
func windowSlice(dates []time.Time, values []float64, flows []Flow, from, to time.Time) ([]time.Time, []float64, []Flow) {
	var d []time.Time
	var v []float64
	for i, t := range dates {
		if t.Before(from) || t.After(to) {
			continue
		}
		d = append(d, t)
		v = append(v, values[i])
	}
	var f []Flow
	for _, fl := range flows {
		if fl.Date.After(to) || !fl.Date.After(from) {
			continue
		}
		f = append(f, fl)
	}
	return d, v, f
}
```

(add `"math"` to `report.go` imports.)

- [ ] **Step 4: Run tests, then the full pofo gate**

Run: `cd ~/projects/pofo && gofmt -l pkg && go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/projects/pofo && git add -A && git commit -m "metrics: Report, the standard period table and summary over a flowed series"
```

---

### Task 9: finador - market facade diet (consumes pofo Lot A)

**Files:**
- Modify: `~/projects/finador/internal/market/source.go` (Ref gains Currency)
- Modify: `~/projects/finador/internal/market/pofo.go` (Daily/Latest/LatestBatch via FetchAny/LatestAny)
- Modify: `~/projects/finador/internal/market/refresh.go` (drop the twin retry; keep the final reject guard)
- Modify: `~/projects/finador/internal/market/refresh_test.go`, `pofo_test.go` (adjust)
- Modify: `~/projects/finador/docs/superpowers/DECISIONS.md` (new entry)

**Interfaces:**
- Consumes: `FetchAny`, `LatestAny`, `QuoteOptions`, `FetchOptions.NoConvert`, `marketdata.ErrWrongCurrency`.
- Produces: `market.Ref` gains `Currency domain.Currency`; the `Source` contract gains: "when ref.Currency is set, Daily must serve quotes natively in it or fail; Latest may convert as a last resort". `refresh.go` keeps a reject-only guard (never merges off-currency data) as enforcement of that contract for third-party Sources.

- [ ] **Step 1: Extend Ref and the Source contract**

`source.go`:

```go
// Ref identifies an instrument to quote: the ISIN is preferred when both
// are set (most precise), the symbol otherwise. Currency, when set, is the
// instrument's declared quote currency: Daily must serve quotes natively
// in it or fail; Latest may convert a last-resort quote into it (a spot
// point is overwritten by the next real close, so conversion never leaves
// a seam in the persisted history).
type Ref struct {
	Symbol, ISIN string
	Currency     domain.Currency
}
```

- [ ] **Step 2: Rewrite the Pofo facade fetches**

`pofo.go`: replace the two id-loops and add the option mapping.

```go
// ids lists the identifiers to try, most precise first.
func (r Ref) ids() []string {
	ids := make([]string, 0, 2)
	if r.ISIN != "" {
		ids = append(ids, r.ISIN)
	}
	if r.Symbol != "" {
		ids = append(ids, r.Symbol)
	}
	return ids
}
```

```go
// Daily returns closes and dividend events from `from` to today. Prices
// are RAW closes (dividends not reinvested): finador values holdings at
// market price and books dividends as cash, so adjusted closes would
// double-count income. The declared currency is enforced natively
// (NoConvert): converted twin closes never splice cleanly into the
// persisted history.
func (p *Pofo) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	ids := ref.ids()
	if len(ids) == 0 {
		return DailyData{}, ErrNotCovered
	}
	s, err := p.Client.FetchAny(ctx, ids, marketdata.FetchOptions{
		From:      from.Time(),
		NoSim:     true,
		Raw:       true,
		Currency:  string(ref.Currency),
		NoConvert: ref.Currency != "",
	})
	if err != nil {
		return DailyData{}, err
	}
	return toDailyData(s), nil
}
```

```go
// Latest returns the freshest available price: the live market quote when
// one exists, otherwise the last daily close (a fund NAV). A quote that
// only exists off-currency converts at its own timestamp - acceptable for
// a spot point, which the next real close overwrites.
func (p *Pofo) Latest(ctx context.Context, ref Ref) (Quote, error) {
	ids := ref.ids()
	if len(ids) == 0 {
		return Quote{}, ErrNotCovered
	}
	q, err := p.Client.LatestAny(ctx, ids, marketdata.QuoteOptions{
		Currency: string(ref.Currency),
	})
	if err != nil {
		return Quote{}, err
	}
	return Quote{
		Price:    q.Price,
		Time:     q.Time,
		Currency: domain.Currency(q.Currency),
		Live:     q.Live,
	}, nil
}
```

`LatestBatch` keeps its shape (live batch on exact symbols, then per-ref fallback) but the fallback now goes through the rewritten `Latest` above - delete nothing else.

- [ ] **Step 3: Slim refresh.go**

In `Refresh`: pass the currency in the ref and delete the twin retry block (the `if data.Currency != ... { if asset.ISIN != "" ... }` re-fetch); KEEP the final reject guard - it now enforces the Source contract for any third-party implementation (the book must never merge off-currency quotes, whatever the Source):

```go
		data, err := src.Daily(ctx, Ref{Symbol: asset.Ticker, ISIN: asset.ISIN, Currency: asset.Currency}, from)
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", asset.Ticker, err))
			continue
		}
		// Contract enforcement, not business logic: the Pofo source already
		// guarantees the currency (NoConvert); a third-party Source must
		// not be able to poison the persisted series either.
		if data.Currency != "" && data.Currency != asset.Currency {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s quotes in %s but the asset is declared in %s: quotes ignored",
				asset.Ticker, data.Currency, asset.Currency))
			continue
		}
```

Same treatment in the FX loop (`Currency: domain.USD` in the ref, keep the reject guard). In `SpotRefresh`, delete the per-quote twin retry (`if q.Currency != "" && q.Currency != ccy && ticker != "" { ... }`) and keep the reject guard; build targets with `Ref{Symbol: asset.Ticker, ISIN: asset.ISIN, Currency: ccy}` and `Ref{Symbol: symbol, Currency: domain.USD}`.

- [ ] **Step 4: Adjust the tests**

- `refresh_test.go`: the tests that asserted the *retry* behaviour (a second `Daily`/`Latest` call with the bare ticker) now assert the simpler contract: a fake Source returning off-currency data yields a warning and no merge (guard kept), and the fake records receiving `ref.Currency == asset.Currency`. Delete the retry-sequencing expectations.
- `pofo_test.go`: the ISIN-then-symbol loop tests move down a level (they now belong to pofo's `any_test.go`, already written in Task 3). Keep two facade tests against an `httptest`-backed `marketdata.Client` serving the Task 2 twin scenario (search: USD twin only, EUR line quotable by its ticker): `Daily(Ref{ISIN, Currency: EUR})` fails (strict NoConvert, no native line via the ISIN alone) while `Daily(Ref{ISIN, Symbol: "NATV.PA", Currency: EUR})` serves the native line; `Latest(Ref{ISIN, Currency: EUR})` succeeds with a converted EUR quote (spot tolerance).

- [ ] **Step 5: DECISIONS.md entry**

Append to `docs/superpowers/DECISIONS.md` (match the existing numbering, French, same style as the neighbours):

```markdown
## D<n> - Devise stricte sur l'historique, conversion tolérée sur le spot (2026-07-04)

La garde de devise et le retry twin-listing sont promus dans pofo
(FetchOptions.NoConvert, FetchAny/LatestAny). finador exige le natif sur le
daily (NoConvert : des closes convertis d'une jumelle ne se raccordent
jamais proprement à un historique natif persisté), mais accepte une cote
spot convertie en dernier recours : un point spot est écrasé par le
prochain vrai close, aucune couture ne peut atteindre la série persistée.
refresh.go garde une garde de rejet (jamais de merge off-currency) comme
contrat d'interface Source, pas comme logique métier.
```

- [ ] **Step 6: Full finador gate**

Run: `cd ~/projects/finador && make check`
Expected: PASS (fmt, vet, lint, tests, race).

- [ ] **Step 7: Commit**

```bash
cd ~/projects/finador && git add -A && git commit -m "market: currency assurance and twin retry move down into pofo"
```

---

### Task 10: finador - perf facade diet and the 7d switch (consumes pofo Lot B)

**Files:**
- Modify: `~/projects/finador/internal/perf/report.go` (facade over metrics.Report)
- Modify: `~/projects/finador/internal/perf/perf.go` (MaxDrawdown over metrics.MaxDrawdown)
- Modify: `~/projects/finador/internal/perf/periods.go` (Names: 5d becomes 7d)
- Modify: `~/projects/finador/internal/perf/report_test.go`, `periods_test.go`, `perf_test.go` (5d expectations)
- Modify: `~/projects/finador/README.md` (perf recipe shows 7d)

**Interfaces:**
- Consumes: `metrics.Report`, `metrics.ReportOptions`, `metrics.Window`, `metrics.MaxDrawdown`, `metrics.Episode`.
- Produces: unchanged finador API (`perf.Report`, `perf.Row`, `perf.Metrics`, `perf.MaxDrawdown`, `perf.Names`) - callers in cli/web compile untouched.

- [ ] **Step 1: Switch Names() and update period tests**

`periods.go`:

```go
// Names lists the period table shown by `finador perf`, in display order.
// 7d, not 5d: five calendar days span only 3-4 trading sessions, while a
// calendar week holds the five sessions a human means by "a week".
func Names() []string {
	return []string{"1d", "7d", "1m", "3m", "ytd", "1y", "prev-yr"}
}
```

`PeriodRange` already parses "7d" (and keeps parsing "5d" for explicit CLI use). Update `periods_test.go` and any `report_test.go` fixture expecting "5d" in the table.

- [ ] **Step 2: Rewrite perf.MaxDrawdown as a facade**

`perf.go`:

```go
// MaxDrawdown returns the deepest drawdown episode of the series.
func MaxDrawdown(points []Point) Drawdown {
	dates, values := toSeries(points)
	ep := metrics.MaxDrawdown(dates, values)
	if ep.PeakDate.IsZero() {
		return Drawdown{}
	}
	dd := Drawdown{Depth: ep.Depth, Peak: domain.DateOf(ep.PeakDate), Trough: domain.DateOf(ep.TroughDate)}
	if !ep.Ongoing && !ep.RecoverDate.IsZero() {
		rec := domain.DateOf(ep.RecoverDate)
		dd.Recovered = &rec
	}
	return dd
}
```

- [ ] **Step 3: Rewrite perf.Report as a facade**

`report.go` keeps `RiskFreeFromConfig`, `Row`, `Metrics`, `MinDaysForRisk`, `MinDaysForCAGR` (now passed down) and becomes:

```go
// Report builds the standard period table + metrics for a daily series:
// the window assembly and gating are pofo's metrics.Report; this facade
// owns the domain types, the display windows (Names + the inception row,
// which for finador means "since the scope holds it") and the flat-window
// dedup, plus the house 0-instead-of-NaN convention.
func Report(points []Point, flows []Flow, evalTo domain.Date, rf float64) ([]Row, Metrics) {
	if len(points) == 0 {
		return nil, Metrics{}
	}
	origin := points[0].Date

	windows := make([]metrics.Window, 0, len(Names())+1)
	for _, name := range Names() {
		pf, pt, err := PeriodRange(name, evalTo)
		if err != nil {
			continue
		}
		windows = append(windows, metrics.Window{Name: name, From: pf.Time(), To: pt.Time()})
	}
	windows = append(windows, metrics.Window{Name: "inception", From: origin.Time(), To: evalTo.Time()})

	dates, values := toSeries(points)
	mrows, sum := metrics.Report(dates, values, toFlows(flows), metrics.ReportOptions{
		Windows:     windows,
		RiskFree:    rf,
		MinRiskDays: MinDaysForRisk,
		MinCAGRDays: MinDaysForCAGR,
	})

	// Flat-window dedup: a longer today-anchored window that measures
	// exactly what a shorter one already showed (bit-equal TWR and gain:
	// the signature of a series flat before the shorter window) adds no
	// information and reads as "a year measured" when only a month really
	// moved. Presentation, not measurement - it lives here, not in pofo.
	var rows []Row
	var last *Row
	for _, mr := range mrows {
		row := Row{Name: mr.Name, TWR: mr.TWR, HasTWR: mr.OK, Gain: mr.Gain, HasGain: mr.OK}
		anchored := mr.Name != "prev-yr" && mr.Name != "inception"
		if anchored && last != nil && row.HasTWR && last.HasTWR &&
			row.TWR == last.TWR && row.Gain == last.Gain {
			continue
		}
		rows = append(rows, row)
		if anchored {
			last = &rows[len(rows)-1]
		}
	}

	m := Metrics{
		InceptionTWR: sum.TWR,
		Since:        origin,
		Days:         sum.Days,
		RiskFree:     rf,
		HasCAGR:      sum.HasCAGR,
		HasRisk:      sum.HasRisk,
		Drawdown:     MaxDrawdown(points),
	}
	if sum.HasCAGR {
		m.CAGR = sum.CAGR
	}
	if sum.HasRisk {
		m.Vol, m.Sharpe, m.Sortino = orZero(sum.Vol), orZero(sum.Sharpe), orZero(sum.Sortino)
	}
	return rows, m
}
```

Delete the now-dead `periodRow` and `windowSlice` from `report.go`; `TWR`, `DailyReturns`, `CAGR`, `Vol`, `Sharpe`, `Sortino` stay in `perf.go` (still used by cli/web directly).

- [ ] **Step 4: Run the perf tests, fix expectations**

Run: `cd ~/projects/finador && go test ./internal/perf -count=1 -v`
Expected: PASS after updating fixtures that named "5d" (the numeric golden values must NOT change except rows whose window changed from 5d to 7d - recompute those by hand from the fixtures, do not just paste the new output).

- [ ] **Step 5: README + full gate**

Update the `finador perf` recipe output in README.md (5d line becomes 7d). Then:

Run: `cd ~/projects/finador && make check`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd ~/projects/finador && git add -A && git commit -m "perf: period table assembly moves down into pofo metrics; a week is 7d"
```

---

### Task 11: Release - tag pofo, drop finador's replace

**Files:**
- Modify: `~/projects/finador/go.mod`, `go.sum`
- Modify: `~/projects/finador/CLAUDE.md` (the "pofo is a sibling checkout" bullet)

**Interfaces:**
- Consumes: everything above, merged and green in both repos.

- [ ] **Step 1: Push pofo and tag the release**

```bash
cd ~/projects/pofo && gofmt -l pkg && go vet ./... && go test ./... -count=1
git push
git tag v0.1.0 && git push origin v0.1.0
```

(v0.1.0 is pofo's first tag; if `git tag` shows existing tags by then, take the next minor.)

- [ ] **Step 2: Point finador at the tag and drop the replace**

```bash
cd ~/projects/finador
go get github.com/bpineau/pofo@v0.1.0
go mod edit -dropreplace github.com/bpineau/pofo
go mod tidy
```

- [ ] **Step 3: Update CLAUDE.md**

Replace the "pofo is a sibling checkout" bullet with:

```markdown
- **pofo is a tagged dependency** (`github.com/bpineau/pofo`). For joint
  development add a temporary `replace github.com/bpineau/pofo => ../pofo`,
  but NEVER commit it: every session that changes pofo ends by tagging a
  pofo release and pointing go.mod at it. Market data fetching, performance
  math and chart rendering live in pofo; finador's `market`, `perf` and
  `chart` packages are thin facades that own finador's conventions (domain
  types, 0-instead-of-NaN, the house chart style). Fix generic math/fetching
  bugs in pofo, finador-flavor bugs in the facade.
```

- [ ] **Step 4: Full gate against the tagged pofo**

Run: `cd ~/projects/finador && make check`
Expected: PASS - proving the tag really contains everything finador consumes.

- [ ] **Step 5: Commit and push both repos**

```bash
cd ~/projects/finador && git add -A && git commit -m "build: pofo v0.1.0, replace directive dropped" && git push
```
