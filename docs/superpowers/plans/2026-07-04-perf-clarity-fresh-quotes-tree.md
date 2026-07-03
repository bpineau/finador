# Perf Clarity, Fresh Quotes and Tree Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drop XIRR and redundant period rows from `perf`, serve quotes at most 1 hour old in the CLI (batched Yahoo spot via a new pofo `LatestBatch`), add `perf --tree` / `value --tree`, and align the read commands' option surfaces.

**Architecture:** pofo gains a cookie+crumb Yahoo auth and a batched `/v7/finance/quote` path behind `Client.LatestBatch` (per-id `Latest` fallback). finador's `market.SpotRefresh` batches through an optional `BatchSource` capability; a `MarketData.SpotAt` timestamp in the encrypted sidecar drives a 1-hour CLI freshness rule. `perf.Report` loses XIRR and deduplicates flat windows. `portfolio` exports its tree assembly (`AssetTree`) and gains scope helpers (`PairScope`, `EnvelopeScope`, `FilterScope`) so `perf --tree` computes one flow-neutralized TWR series per displayed line.

**Tech Stack:** Go stdlib only; existing deps (cobra, shopspring/decimal, pofo). No new dependencies.

## Global Constraints

- Pure Go, no CGo, no JavaScript; web is server-rendered html/template.
- No new dependencies (spec + CLAUDE.md dependency budget).
- pofo is a sibling checkout at `../pofo` (go.mod `replace`); generic fetching logic goes in pofo, finador-flavored behavior in `internal/market`.
- Everything user-visible is English; errors are one `finador: …` line; plain hyphens only, never em-dashes.
- CLI and web must stay behaviourally identical (same perf table rules).
- The ledger format does not change; `SpotAt` lives only in the sidecar cache (not FORMAT.md scope).
- Tests: table-driven stdlib, no network, fake `market.Source`; `FINADOR_CACHE_DIR` for the sidecar; `make check` must pass; `make race` after touching `web`/`store`.
- Run `make check` in finador and `make test` in `../pofo` before each commit touching that repo.
- DECISIONS.md entries are written in French, numbered D<next> (read the file for the next free number).

---

### Task 1: pofo - Yahoo cookie+crumb auth

**Files:**
- Create: `../pofo/pkg/marketdata/yahoo_auth.go`
- Create: `../pofo/pkg/marketdata/yahoo_auth_test.go`
- Modify: `../pofo/pkg/marketdata/client.go` (Client fields + NewClient default)

**Interfaces:**
- Consumes: `Client.do(ctx, method, rawURL, contentType, payload, headers)` (existing; sets User-Agent, returns `fmt.Errorf("HTTP %d", code)` on non-retryable non-200), `c.ChartBase`, `c.UserAgent`, `c.HTTP`.
- Produces: `(c *Client) yahooAuthPair(ctx) (yahooAuth, error)`, `(c *Client) invalidateYahooAuth()`, type `yahooAuth struct { cookie, crumb string }`, Client field `CookieBase string` (default `"https://fc.yahoo.com"`).

- [ ] **Step 1: Add Client fields**

In `client.go`, add to the `Client` struct (near `ChartBase`):

```go
	CookieBase      string // Yahoo cookie bootstrap host (fc.yahoo.com)
```

and to the private fields:

```go
	authMu     sync.Mutex
	auth       *yahooAuth // cached Yahoo cookie+crumb, nil until first use
```

In `NewClient`, add `CookieBase: "https://fc.yahoo.com",`.

- [ ] **Step 2: Write the failing test**

`yahoo_auth_test.go` - an `httptest.Server` playing both hosts (set `CookieBase` and `ChartBase` to the server URL):

```go
package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newAuthServer stubs the two auth endpoints: any hit on / sets the cookie
// (302, like fc.yahoo.com), /v1/test/getcrumb answers the crumb only when
// the cookie is presented.
func newAuthServer(t *testing.T, crumb string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A3", Value: "test-cookie"})
		http.Redirect(w, r, "https://consent.example/", http.StatusFound)
	})
	mux.HandleFunc("/v1/test/getcrumb", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("A3"); err != nil || c.Value != "test-cookie" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(crumb))
	})
	return httptest.NewServer(mux)
}

func TestYahooAuthPair(t *testing.T) {
	srv := newAuthServer(t, "abc/123")
	defer srv.Close()
	c := NewClient(t.TempDir())
	c.CookieBase, c.ChartBase = srv.URL, srv.URL

	a, err := c.yahooAuthPair(context.Background())
	if err != nil {
		t.Fatalf("yahooAuthPair: %v", err)
	}
	if a.crumb != "abc/123" || a.cookie == "" {
		t.Fatalf("got crumb %q cookie %q", a.crumb, a.cookie)
	}

	// Cached: a second call must not refetch (server closed to prove it).
	srv.Close()
	if _, err := c.yahooAuthPair(context.Background()); err != nil {
		t.Fatalf("cached pair refetched: %v", err)
	}

	// Invalidate drops the cache: the next call fails against the dead server.
	c.invalidateYahooAuth()
	if _, err := c.yahooAuthPair(context.Background()); err == nil {
		t.Fatal("expected an error after invalidation with the server gone")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd ../pofo && go test ./pkg/marketdata -run TestYahooAuthPair -count=1`
Expected: FAIL (undefined: yahooAuthPair).

- [ ] **Step 4: Implement `yahoo_auth.go`**

```go
package marketdata

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// yahooAuth is the cookie+crumb pair Yahoo's quote API requires: Yahoo hands
// the cookie on any page hit and derives the crumb from it. Both expire
// together, so they are cached and renewed as one unit.
type yahooAuth struct {
	cookie string // Cookie header value, e.g. "A3=d=…"
	crumb  string
}

// yahooAuthPair returns the cached cookie+crumb, performing the bootstrap
// dance on first use. Safe for concurrent callers.
func (c *Client) yahooAuthPair(ctx context.Context) (yahooAuth, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.auth != nil {
		return *c.auth, nil
	}
	a, err := c.fetchYahooAuth(ctx)
	if err != nil {
		return yahooAuth{}, err
	}
	c.auth = &a
	return a, nil
}

// invalidateYahooAuth drops the cached pair after an HTTP 401/403: the next
// yahooAuthPair call fetches a fresh one.
func (c *Client) invalidateYahooAuth() {
	c.authMu.Lock()
	c.auth = nil
	c.authMu.Unlock()
}

// fetchYahooAuth hits CookieBase to collect the consent cookie - on the raw
// redirect response, which must not be followed or the cookie is lost - then
// trades it for the crumb at /v1/test/getcrumb.
func (c *Client) fetchYahooAuth(ctx context.Context) (yahooAuth, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.CookieBase, nil)
	if err != nil {
		return yahooAuth{}, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	hc := *c.HTTP // shallow copy: same transport, no redirect following
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return yahooAuth{}, err
	}
	defer resp.Body.Close()
	parts := make([]string, 0, 2)
	for _, ck := range resp.Cookies() {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	if len(parts) == 0 {
		return yahooAuth{}, fmt.Errorf("yahoo auth: no cookie from %s", c.CookieBase)
	}
	cookie := strings.Join(parts, "; ")
	crumb, err := c.do(ctx, http.MethodGet, c.ChartBase+"/v1/test/getcrumb", "", nil,
		map[string]string{"Cookie": cookie})
	if err != nil {
		return yahooAuth{}, fmt.Errorf("yahoo auth: crumb: %w", err)
	}
	if len(crumb) == 0 || len(crumb) > 64 {
		return yahooAuth{}, fmt.Errorf("yahoo auth: implausible crumb %q", crumb)
	}
	return yahooAuth{cookie: cookie, crumb: string(crumb)}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd ../pofo && go test ./pkg/marketdata -run TestYahooAuthPair -count=1 -v`
Expected: PASS

- [ ] **Step 6: Commit (pofo repo)**

```bash
cd ../pofo && git add pkg/marketdata/yahoo_auth.go pkg/marketdata/yahoo_auth_test.go pkg/marketdata/client.go && git commit -m "marketdata: Yahoo cookie+crumb auth for the quote API"
```

---

### Task 2: pofo - batched Latest (`LatestBatch`)

**Files:**
- Create: `../pofo/pkg/marketdata/latest_batch.go`
- Create: `../pofo/pkg/marketdata/latest_batch_test.go`

**Interfaces:**
- Consumes: `yahooAuthPair`/`invalidateYahooAuth` (Task 1), `c.yahooSymbol(ctx, id) (string, bool)` (intraday.go), `c.Latest(ctx, id) (*Quote, error)`, `SplitSim`, `c.do`, `c.Logf`.
- Produces: `(c *Client) LatestBatch(ctx context.Context, ids []string) map[string]Quote`. Ids that no source can serve are simply absent from the map; the call never fails as a whole.

- [ ] **Step 1: Write the failing test**

`latest_batch_test.go`. The stub server (reuse `newAuthServer`'s mux style) serves `/v1/test/getcrumb`, `/v7/finance/quote` and, for the fallback id, the chart spot endpoint `/v8/finance/chart/{symbol}`:

```go
package marketdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestLatestBatch: two Yahoo symbols served by one v7 call; a symbol the
// batch does not return falls back to the per-id spot path.
func TestLatestBatch(t *testing.T) {
	var batchCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A3", Value: "ck"})
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/test/getcrumb", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("crumb1"))
	})
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		batchCalls.Add(1)
		if r.URL.Query().Get("crumb") != "crumb1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("symbols"); !strings.Contains(got, "AAPL") || !strings.Contains(got, "MC.PA") {
			t.Errorf("symbols=%q", got)
		}
		fmt.Fprint(w, `{"quoteResponse":{"result":[
		 {"symbol":"AAPL","currency":"USD","exchangeTimezoneName":"America/New_York","regularMarketPrice":308.63,"regularMarketTime":1782999000},
		 {"symbol":"MC.PA","currency":"EUR","exchangeTimezoneName":"Europe/Paris","regularMarketPrice":495.7,"regularMarketTime":1783092989}]}}`)
	})
	mux.HandleFunc("/v8/finance/chart/ORPHAN.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"chart":{"result":[{"meta":{"currency":"EUR","exchangeTimezoneName":"Europe/Paris","regularMarketPrice":10.5,"regularMarketTime":1783092989}}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(t.TempDir())
	c.CookieBase, c.ChartBase, c.SearchBase = srv.URL, srv.URL, srv.URL

	got := c.LatestBatch(context.Background(), []string{"AAPL", "MC.PA", "ORPHAN.PA"})
	if len(got) != 3 {
		t.Fatalf("got %d quotes, want 3: %#v", len(got), got)
	}
	if q := got["AAPL"]; q.Price != 308.63 || q.Currency != "USD" || !q.Live || q.Source != "yahoo" {
		t.Fatalf("AAPL quote: %+v", q)
	}
	if q := got["ORPHAN.PA"]; q.Price != 10.5 {
		t.Fatalf("fallback quote: %+v", q)
	}
	if n := batchCalls.Load(); n != 1 {
		t.Fatalf("batch endpoint hit %d times, want 1", n)
	}
}

// TestLatestBatchCrumbRenewal: a stale crumb gets 401, the client renews the
// auth pair once and retries.
func TestLatestBatchCrumbRenewal(t *testing.T) {
	var crumbServes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A3", Value: "ck"})
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/test/getcrumb", func(w http.ResponseWriter, r *http.Request) {
		if crumbServes.Add(1) == 1 {
			_, _ = w.Write([]byte("stale"))
			return
		}
		_, _ = w.Write([]byte("fresh"))
	})
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("crumb") != "fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"quoteResponse":{"result":[
		 {"symbol":"AAPL","currency":"USD","exchangeTimezoneName":"America/New_York","regularMarketPrice":300,"regularMarketTime":1782999000}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(t.TempDir())
	c.CookieBase, c.ChartBase = srv.URL, srv.URL

	got := c.LatestBatch(context.Background(), []string{"AAPL"})
	if q, ok := got["AAPL"]; !ok || q.Price != 300 {
		t.Fatalf("after renewal: %#v", got)
	}
	if n := crumbServes.Load(); n != 2 {
		t.Fatalf("crumb served %d times, want 2 (stale then fresh)", n)
	}
}
```

Note: `ORPHAN.PA` is a plain ticker, so `yahooSymbol` maps it to itself and it IS sent in the batch; the v7 stub simply does not return it, which exercises the "batch missed it" fallback through `Latest` → chart spot.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ../pofo && go test ./pkg/marketdata -run 'TestLatestBatch' -count=1`
Expected: FAIL (undefined: LatestBatch).

- [ ] **Step 3: Implement `latest_batch.go`**

```go
package marketdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// yahooQuoteBatchMax bounds one /v7/finance/quote call. Yahoo accepts far
// more; chunking just keeps URLs short.
const yahooQuoteBatchMax = 50

// LatestBatch returns the freshest available price of each id: one batched
// Yahoo quote call for the Yahoo-quoted ids, then the per-id Latest fallback
// (FT/Morningstar NAV, Stooq, cache…) for the rest and for anything the batch
// did not return. Ids no source can serve are absent from the result; the
// call never fails as a whole. Like Latest, a closed market still yields a
// Live quote: the regular session's last price, timed at the close.
func (c *Client) LatestBatch(ctx context.Context, ids []string) map[string]Quote {
	out := make(map[string]Quote, len(ids))
	symbols := make([]string, 0, len(ids))
	bySymbol := make(map[string][]string, len(ids)) // yahoo symbol → original ids
	rest := make([]string, 0, len(ids))
	for _, id := range ids {
		base, _ := SplitSim(id)
		symbol, ok := c.yahooSymbol(ctx, base)
		if !ok {
			rest = append(rest, id)
			continue
		}
		if len(bySymbol[symbol]) == 0 {
			symbols = append(symbols, symbol)
		}
		bySymbol[symbol] = append(bySymbol[symbol], id)
	}
	quotes := c.fetchYahooQuoteBatch(ctx, symbols)
	for symbol, ids := range bySymbol {
		q, ok := quotes[symbol]
		if !ok {
			rest = append(rest, ids...)
			continue
		}
		for _, id := range ids {
			out[id] = q
		}
	}
	for _, id := range rest {
		if q, err := c.Latest(ctx, id); err == nil {
			out[id] = *q
		} else {
			c.Logf("latest batch: %s: %v", id, err)
		}
	}
	return out
}

// fetchYahooQuoteBatch reads live regular-market prices for many symbols in
// yahooQuoteBatchMax-sized chunks of the v7 quote API (cookie+crumb needed).
func (c *Client) fetchYahooQuoteBatch(ctx context.Context, symbols []string) map[string]Quote {
	out := make(map[string]Quote, len(symbols))
	for start := 0; start < len(symbols); start += yahooQuoteBatchMax {
		c.quoteBatchChunk(ctx, symbols[start:min(start+yahooQuoteBatchMax, len(symbols))], out)
	}
	return out
}

func (c *Client) quoteBatchChunk(ctx context.Context, symbols []string, out map[string]Quote) {
	if len(symbols) == 0 {
		return
	}
	auth, err := c.yahooAuthPair(ctx)
	if err != nil {
		c.Logf("yahoo quote batch: %v", err)
		return
	}
	body, err := c.quoteBatchGet(ctx, symbols, auth)
	if isYahooAuthErr(err) { // stale crumb: renew once and retry
		c.invalidateYahooAuth()
		if auth, err = c.yahooAuthPair(ctx); err == nil {
			body, err = c.quoteBatchGet(ctx, symbols, auth)
		}
	}
	if err != nil {
		c.Logf("yahoo quote batch: %v", err)
		return
	}
	var resp struct {
		QuoteResponse struct {
			Result []struct {
				Symbol               string   `json:"symbol"`
				Currency             string   `json:"currency"`
				ExchangeTimezoneName string   `json:"exchangeTimezoneName"`
				RegularMarketPrice   *float64 `json:"regularMarketPrice"`
				RegularMarketTime    int64    `json:"regularMarketTime"`
			} `json:"result"`
		} `json:"quoteResponse"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		c.Logf("yahoo quote batch: unreadable response: %v", err)
		return
	}
	for _, r := range resp.QuoteResponse.Result {
		if r.RegularMarketPrice == nil || *r.RegularMarketPrice <= 0 {
			continue
		}
		loc, err := time.LoadLocation(r.ExchangeTimezoneName)
		if err != nil {
			loc = time.UTC
		}
		out[r.Symbol] = Quote{
			Price:    *r.RegularMarketPrice,
			Time:     time.Unix(r.RegularMarketTime, 0).In(loc),
			Currency: r.Currency,
			Source:   "yahoo",
			Live:     true,
		}
	}
}

func (c *Client) quoteBatchGet(ctx context.Context, symbols []string, auth yahooAuth) ([]byte, error) {
	path := "/v7/finance/quote?symbols=" + url.QueryEscape(strings.Join(symbols, ",")) +
		"&crumb=" + url.QueryEscape(auth.crumb)
	return c.do(ctx, http.MethodGet, c.ChartBase+path, "", nil, map[string]string{"Cookie": auth.cookie})
}

// isYahooAuthErr matches the "HTTP 401"/"HTTP 403" errors do() produces when
// the crumb or cookie has expired.
func isYahooAuthErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ../pofo && go test ./pkg/marketdata -run 'TestLatestBatch|TestYahooAuth' -count=1 -v`
Expected: PASS (both batch tests + Task 1's).

- [ ] **Step 5: Full pofo gate**

Run: `cd ../pofo && make test`
Expected: PASS. No committed live test: the endpoint and the cookie+crumb dance were validated by hand with curl during design (v7 answers with currency, marketState and regularMarketTime once the crumb is presented; without it, HTTP 401 "Unauthorized"). The final task's smoke test on the real portfolio covers the live path end to end.

- [ ] **Step 6: Commit (pofo repo)**

```bash
cd ../pofo && git add pkg/marketdata/latest_batch.go pkg/marketdata/latest_batch_test.go && git commit -m "marketdata: LatestBatch, one v7 quote call for many instruments"
```

---

### Task 3: finador - BatchSource capability + batched SpotRefresh

**Files:**
- Modify: `internal/market/source.go` (add BatchSource)
- Modify: `internal/market/pofo.go` (Pofo.LatestBatch)
- Modify: `internal/market/refresh.go` (SpotRefresh batches; ErrNotCovered is silent)
- Modify: `internal/market/refresh_test.go` (new tests)

**Interfaces:**
- Consumes: pofo `(*Client).LatestBatch(ctx, ids []string) map[string]marketdata.Quote` (Task 2).
- Produces: `market.BatchSource` interface `{ LatestBatch(ctx context.Context, refs []Ref) map[Ref]Quote }`; `SpotRefresh` unchanged signature, batch-first behavior; not-covered instruments no longer produce warnings.

- [ ] **Step 1: Write the failing tests**

Append to `refresh_test.go` (extend the existing `fakeSource` fixture file conventions; the existing `fakeSource` there gains nothing - build a wrapper):

```go
// batchSource wraps fakeSource with a scripted batch answer and counters.
type batchSource struct {
	*fakeSource
	batch       map[Ref]Quote
	batchCalls  int
	latestCalls int
}

func (b *batchSource) LatestBatch(_ context.Context, refs []Ref) map[Ref]Quote {
	b.batchCalls++
	out := map[Ref]Quote{}
	for _, r := range refs {
		if q, ok := b.batch[r]; ok {
			out[r] = q
		}
	}
	return out
}

func (b *batchSource) Latest(ctx context.Context, ref Ref) (Quote, error) {
	b.latestCalls++
	return b.fakeSource.Latest(ctx, ref)
}

// TestSpotRefreshBatch: one batch call serves the covered instruments; only
// the miss falls back to Latest; ErrNotCovered stays silent.
func TestSpotRefreshBatch(t *testing.T) { … }
```

Write `TestSpotRefreshBatch` with a book holding two tickered securities and one EUR account (so `EURUSD=X` is not needed - use a USD account to force one FX ref if simpler): assert `batchCalls == 1`, `latestCalls` equals exactly the number of refs absent from the scripted batch, quotes merged into `b.Market.Price(id)` (today's PricePoint present), and `sum.Warnings` empty when the fallback returns `ErrNotCovered`. Follow the fixture style of the existing tests in this file (they build `domain.Book` values inline).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/market -run TestSpotRefreshBatch -count=1`
Expected: FAIL (undefined: BatchSource / behavior mismatch).

- [ ] **Step 3: Implement**

`source.go` - add after the Source interface:

```go
// BatchSource is an optional Source capability: the freshest price of many
// instruments in one call. SpotRefresh uses it when the source implements it
// and falls back to per-instrument Latest for anything the batch missed.
type BatchSource interface {
	LatestBatch(ctx context.Context, refs []Ref) map[Ref]Quote
}
```

`pofo.go` - add:

```go
// LatestBatch fetches the freshest price of many instruments in one pofo
// call (one Yahoo quote request for the Yahoo-quoted ones). Refs are keyed
// like Latest resolves them: ISIN preferred, symbol otherwise.
func (p *Pofo) LatestBatch(ctx context.Context, refs []Ref) map[Ref]Quote {
	ids := make([]string, 0, len(refs))
	byID := make(map[string][]Ref, len(refs))
	for _, ref := range refs {
		id := ref.ISIN
		if id == "" {
			id = ref.Symbol
		}
		if id == "" {
			continue
		}
		if len(byID[id]) == 0 {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], ref)
	}
	quotes := p.Client.LatestBatch(ctx, ids)
	out := make(map[Ref]Quote, len(quotes))
	for id, q := range quotes {
		for _, ref := range byID[id] {
			out[ref] = Quote{Price: q.Price, Time: q.Time, Currency: domain.Currency(q.Currency), Live: q.Live}
		}
	}
	return out
}
```

`refresh.go` - rewrite SpotRefresh (imports gain `"errors"`):

```go
// SpotRefresh updates today's price of every quoted security and FX rate
// from the source's latest quotes: one batched call when the source supports
// it, individual fallbacks otherwise, no history depth, no dividends. It
// complements Refresh (which must still run once a day) and keeps valuations
// live between two daily refreshes. It never fails hard: a failed quote
// degrades to a warning, and an instrument the source does not cover at all
// is silently skipped (its last daily close already stands).
func SpotRefresh(ctx context.Context, b *domain.Book, src Source) SpotSummary {
	sum := SpotSummary{Quotes: map[domain.AssetID]Quote{}}

	type target struct {
		ref   Ref
		apply func(Quote)
	}
	var targets []target
	for _, asset := range b.Assets {
		if asset.Kind != domain.Security || asset.Ticker == "" {
			continue
		}
		id := asset.ID
		targets = append(targets, target{
			ref: Ref{Symbol: asset.Ticker, ISIN: asset.ISIN},
			apply: func(q Quote) {
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
	if bs, ok := src.(BatchSource); ok && len(targets) > 0 {
		refs := make([]Ref, len(targets))
		for i, t := range targets {
			refs[i] = t.ref
		}
		batched = bs.LatestBatch(ctx, refs)
	}
	for _, t := range targets {
		q, ok := batched[t.ref]
		if !ok {
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
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/market -count=1`
Expected: PASS (existing SpotRefresh tests may need the warning-on-not-covered expectation updated to silent-skip - update them, that behavior change is deliberate).

- [ ] **Step 5: Commit**

```bash
git add internal/market && git commit -m "market: batch spot quotes through pofo LatestBatch"
```

---

### Task 4: finador - SpotAt and the 1-hour CLI freshness rule

**Files:**
- Modify: `internal/domain/marketdata.go` (MarketData.SpotAt)
- Modify: `internal/market/refresh.go` (SpotRefresh stamps SpotAt)
- Modify: `internal/cli/cli.go:162-175` (ensureFresh spot pass)
- Modify: `internal/cli/refresh.go` (explicit refresh also spots)
- Modify: `internal/cli/cli_test.go` (freshness test)

**Interfaces:**
- Consumes: `market.SpotRefresh` (Task 3), `store.File.SaveCache`.
- Produces: `domain.MarketData.SpotAt time.Time` (JSON `spotAt,omitzero`, sidecar-only); ensureFresh contract: after any non-offline read command, quotes are at most 1h old.

- [ ] **Step 1: Add the field**

`domain/marketdata.go` (imports gain `"time"`):

```go
type MarketData struct {
	Prices    map[AssetID]*PriceSeries    `json:"prices,omitempty"`
	FX        map[Currency]*PriceSeries   `json:"fx,omitempty"` // value of 1 unit in USD
	Dividends map[AssetID][]DividendEvent `json:"dividends,omitempty"`
	// SpotAt is when the last spot pass ran (see market.SpotRefresh): the
	// CLI re-spots when it is older than an hour. Sidecar-cache state, never
	// part of the synced ledger.
	SpotAt time.Time `json:"spotAt,omitzero"`
}
```

In `market.SpotRefresh` (Task 3's version), first line of the function body:

```go
	b.Market.SpotAt = time.Now()
```

(imports gain `"time"`; stamped even when quotes fail, so an outage does not turn every command into a hammering retry - the next hour retries.)

- [ ] **Step 2: Write the failing CLI test**

In `cli_test.go`, a counting source + two runs (mimic `tryRunNet` conventions; `FINADOR_CACHE_DIR` is already pointed at a temp dir by the existing helpers - verify, else `t.Setenv` it):

```go
// countingSource counts spot lookups on top of fakeSource.
type countingSource struct {
	fakeSource
	latest atomic.Int32
}

func (c *countingSource) Latest(ctx context.Context, ref market.Ref) (market.Quote, error) {
	c.latest.Add(1)
	return market.Quote{}, market.ErrNotCovered
}

// TestSpotFreshnessHourly: the first online command runs a spot pass, the
// second within the hour does not (SpotAt persisted in the sidecar).
func TestSpotFreshnessHourly(t *testing.T) { … }
```

Body: init a db with one tickered asset (reuse the existing init/add helper calls other tests use), run `value` twice through a `cli.New(cli.WithSource(src))` command, assert `src.latest.Load() > 0` after the first run, record the count, run again, assert the count did not grow.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cli -run TestSpotFreshnessHourly -count=1`
Expected: FAIL (spot pass runs both times - no SpotAt rule yet). If it fails with "spot never runs", ensureFresh isn't wired yet either; both are the red state.

- [ ] **Step 4: Implement ensureFresh**

`cli/cli.go` (imports gain `"time"`):

```go
// ensureFresh keeps the market cache serving current numbers: the daily
// fetch when a series has not been fetched today (history depth, dividends),
// then a light spot pass when the last one is older than an hour, so every
// figure a command prints reflects the market of the last hour, not this
// morning's cache. A no-op in offline mode.
func (a *app) ensureFresh(cmd *cobra.Command, f *store.File) {
	if a.offline {
		return
	}
	sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), false)
	for _, w := range sum.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
	}
	spotted := false
	if time.Since(f.Book.Market.SpotAt) > time.Hour {
		spot := market.SpotRefresh(cmd.Context(), f.Book, a.marketSource())
		for _, w := range spot.Warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
		}
		spotted = true
	}
	if len(sum.Fetched) > 0 || spotted {
		if err := f.SaveCache(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: cache not saved:", err)
		}
	}
}
```

In `cli/refresh.go`, after the forced `market.Refresh(..., true)`, add an unconditional spot pass (explicit refresh means "give me the market now"):

```go
			spot := market.SpotRefresh(cmd.Context(), f.Book, a.marketSource())
			for _, w := range spot.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
```

(before the SaveCache that command already performs - check and keep its save unconditional there).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/cli ./internal/market ./internal/domain -count=1`
Expected: PASS. Existing CLI tests must stay green: fakeSource.Latest returns ErrNotCovered, silent since Task 3.

- [ ] **Step 6: Race gate (store sidecar content changed shape)**

Run: `make race`
Expected: PASS.

- [ ] **Step 7: DECISIONS.md entry + commit**

Append to `docs/superpowers/DECISIONS.md` (French, next D number), the decision: CLI = données fraîches (<1h) plutôt qu'un affichage de fraîcheur; SpotAt horodaté dans le sidecar; hors ouverture des marchés le spot est le dernier cours de séance ("1d" = maintenant).

```bash
git add internal/domain internal/market internal/cli docs/superpowers/DECISIONS.md && git commit -m "cli: quotes at most an hour old (SpotAt + spot pass in ensureFresh)"
```

---

### Task 5: perf - remove XIRR everywhere

**Files:**
- Modify: `internal/perf/report.go` (Row, periodRow)
- Modify: `internal/perf/perf.go` (delete XIRR + now-unused helpers)
- Modify: `internal/perf/report_test.go`, `internal/perf/perf_test.go` (drop XIRR expectations/tests)
- Modify: `internal/cli/perf.go` (column, xirrCell)
- Modify: `internal/web/templates/scope.html:44-48`, `internal/web/templates/dashboard.html:56-61`
- Modify: web tests asserting the XIRR header, `README.md` (lines 26, 54, 450, 691 area)

**Interfaces:**
- Produces: `perf.Row{Name string; TWR float64; HasTWR bool; Gain float64; HasGain bool}` (XIRR fields gone). Consumed as-is by cli/perf.go, web handlers and Task 10.

- [ ] **Step 1: Adjust the perf package tests first**

In `report_test.go` and `perf_test.go`: delete the XIRR-specific tests and every `HasXIRR`/`XIRR` field expectation. Where a test checked "XIRR dash under 30 days", delete that case (the concept disappears).

- [ ] **Step 2: Run to verify they fail to compile**

Run: `go test ./internal/perf -count=1`
Expected: FAIL (fields still exist, deleted tests referenced removed behavior - compile errors are the red state here).

- [ ] **Step 3: Implement the removal**

`report.go`: Row keeps only Name/TWR/HasTWR/Gain/HasGain; delete the whole `// XIRR: windows < 30 days …` block in `periodRow`. `perf.go`: delete `XIRR` and any helper only it used (bisection/NPV - check with `go build ./...` after removal). `cli/perf.go`: header becomes

```go
	fmt.Fprintf(out, "%-9s %14s %16s\n", "PERIOD", "TWR", "GAIN ("+string(display)+")")
```

`printRow` loses the xirr argument; the `--from` window row calls it without `xirrCell`; delete `xirrCell`. Templates: remove the `<th class="nombre">XIRR</th>` and the XIRR `<td>` line in both files. README: rewrite the four XIRR mentions - the pitch line 26 becomes TWR-only ("Real performance. TWR (strategy performance, flows neutralized), CAGR, vol…"), line 54's comment drops XIRR, the explainer section (line ~450) shrinks to TWR + gain, line 691 drops XIRR; line 327's "it feeds the tax basis and XIRR" becomes "it feeds the tax basis and the performance flows".

- [ ] **Step 4: Run the full gate**

Run: `make check`
Expected: PASS (web template tests updated in the same pass if they assert the column).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "perf: drop XIRR from the report (TWR and gain only)"
```

---

### Task 6: perf - skip uninformative period rows

**Files:**
- Modify: `internal/perf/report.go:62-79` (Report loop)
- Modify: `internal/perf/report_test.go` (new test)
- Modify: `docs/superpowers/DECISIONS.md`

**Interfaces:**
- Consumes/produces: `perf.Report` signature unchanged; fewer rows out.

- [ ] **Step 1: Write the failing test**

```go
// TestReportSkipsFlatDuplicates: a book flat for months then active one
// month must not print 3m/ytd/1y rows identical to the 1m row.
func TestReportSkipsFlatDuplicates(t *testing.T) {
	today := domain.Date{Year: 2026, Month: 7, Day: 3}
	var points []Point
	// 400 flat days at 1000, then a +5% month.
	for d := today.AddDays(-400); !today.Before(d); d = d.AddDays(1) {
		v := 1000.0
		if today.AddDays(-30).Before(d) {
			v = 1050.0
		}
		points = append(points, Point{Date: d, Value: v})
	}
	rows, _ := Report(points, nil, today, 0)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	got := strings.Join(names, ",")
	if strings.Contains(got, "3m") || strings.Contains(got, "ytd") || strings.Contains(got, "1y") {
		t.Fatalf("flat duplicates kept: %s", got)
	}
	if !strings.Contains(got, "1m") || !strings.Contains(got, "inception") {
		t.Fatalf("informative rows missing: %s", got)
	}
}
```

(The +5% step lands inside every window from 1m up; adjust the step day so the 1m window contains the whole move - the test data above jumps once ~30 days ago, so 1m/3m/ytd/1y all measure exactly +5% with equal Gain, which is the dedup trigger.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/perf -run TestReportSkipsFlatDuplicates -count=1`
Expected: FAIL (3m/ytd/1y present).

- [ ] **Step 3: Implement in Report**

Replace the period loop:

```go
	var rows []Row
	var last *Row // last kept today-anchored row, the dedup base
	for _, name := range Names() {
		pf, pt, err := PeriodRange(name, evalTo)
		if err != nil {
			continue
		}
		if pf.Before(origin) {
			continue // window predates the track record - the inception row covers it
		}
		row := periodRow(name, points, flows, pf, pt)
		// A longer window that measures exactly what a shorter one already
		// showed (bit-equal TWR and gain: the signature of a series flat
		// before the shorter window) adds no information - skip it. Only
		// today-anchored windows compare; "prev-yr" ends elsewhere.
		anchored := name != "prev-yr"
		if anchored && last != nil && row.HasTWR && last.HasTWR &&
			row.TWR == last.TWR && row.Gain == last.Gain {
			continue
		}
		rows = append(rows, row)
		if anchored {
			last = &rows[len(rows)-1]
		}
	}
	rows = append(rows, periodRow("inception", points, flows, origin, evalTo))
```

- [ ] **Step 4: Run the package + web tests**

Run: `go test ./internal/perf ./internal/web ./internal/cli -count=1`
Expected: PASS (fix any web/cli test that asserted the duplicated rows).

- [ ] **Step 5: DECISIONS.md + commit**

Append the D entry (French): une fenêtre plus longue bit-identique (TWR et gain) à la fenêtre plus courte déjà affichée est masquée; faux positif quasi impossible et bénin (ligne redondante), faux négatif impossible (toute vraie différence de marché change les flottants).

```bash
git add internal/perf internal/web internal/cli docs/superpowers/DECISIONS.md && git commit -m "perf: skip period rows that repeat a shorter flat window"
```

---

### Task 7: portfolio - export the tree assembly

**Files:**
- Modify: `internal/portfolio/export.go:117-238` (treeItem/treeEnvelope/assetTree → exported)
- Modify: `internal/portfolio/export_test.go` (adjust names)

**Interfaces:**
- Produces:

```go
type TreeItem struct {
	Asset      *domain.Asset // nil: the envelope's cash line
	Gross, Net float64
}
type TreeEnvelope struct {
	Account    *domain.Account
	Gross, Net float64
	Items      []TreeItem
}
func AssetTree(lines []PositionLine) []TreeEnvelope
```

Ordering guarantees unchanged: envelopes and items sorted by gross descending then name; Σ items == envelope.

- [ ] **Step 1: Mechanical refactor**

Replace `treeItem`/`treeEnvelope`/`assetTree` with the exported forms above. The item keeps the `*domain.Asset` instead of copied name/isin strings (identity is what `perf --tree` needs); `label()` becomes:

```go
// Label renders a line-item as "Name (ISIN)", or "cash" for a cash line.
func (it TreeItem) Label() string {
	if it.Asset == nil {
		return "cash"
	}
	if it.Asset.ISIN == "" {
		return it.Asset.Name
	}
	return fmt.Sprintf("%s (%s)", it.Asset.Name, it.Asset.ISIN)
}
```

`WriteAssetTree` adapts (envelope name = `env.Account.Name`; the single-item collapse reads `it.Asset.ISIN` when `it.Asset != nil`). Output must stay byte-identical - the existing export tests are the guard.

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/portfolio -count=1`
Expected: PASS with no golden-output change.

- [ ] **Step 3: Commit**

```bash
git add internal/portfolio && git commit -m "portfolio: export the envelope tree assembly (AssetTree)"
```

---

### Task 8: portfolio - scope helpers (PairScope, EnvelopeScope, FilterScope)

**Files:**
- Modify: `internal/portfolio/scope.go`
- Modify: `internal/portfolio/scope_test.go`

**Interfaces:**
- Produces:

```go
func PairScope(acc *domain.Account, asset *domain.Asset) Scope   // one position, no cash
func EnvelopeScope(s Scope, acc *domain.Account) Scope           // s restricted to one account
func FilterScope(lines []PositionLine, s Scope) []PositionLine   // breakdown lines kept by s
```

- [ ] **Step 1: Write the failing tests**

In `scope_test.go`, table-driven over a small book (two accounts, two assets, one label): `PairScope` keeps exactly its pair and never cash; `EnvelopeScope` of an `All` scope keeps the account's assets AND its cash, of a `ByGroup` scope keeps only that group's assets in that account and no cash, carries `Excluded` through; `FilterScope` drops excluded assets, other accounts' cash, keeps what `HasAsset`/`HasCash` keep. Assert via `HasAsset`/`HasCash` on the returned scopes and via line counts for `FilterScope`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/portfolio -run 'TestPairScope|TestEnvelopeScope|TestFilterScope' -count=1`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement in scope.go**

```go
// PairScope is the scope of a single (account, asset) position: what one
// line of a tree view measures. Cash is excluded (the envelope row owns it).
func PairScope(acc *domain.Account, asset *domain.Asset) Scope {
	return Scope{
		Kind:  ByLabel,
		Label: acc.Name + " › " + asset.Name,
		Pairs: map[pairKey]bool{{acc: acc.ID, asset: asset.ID}: true},
	}
}

// EnvelopeScope restricts s to one account: what the account's row of a tree
// view measures. Cash is kept exactly when s itself keeps it.
func EnvelopeScope(s Scope, acc *domain.Account) Scope {
	out := Scope{Kind: ByAccount, Account: acc, Label: acc.Name, Excluded: s.Excluded}
	switch s.Kind {
	case All, ByAccount:
		return out
	case ByGroup:
		out.Kind, out.Group = ByAccountGroup, s.Group
		return out
	case ByAsset:
		return Scope{Kind: ByLabel, Label: acc.Name, Excluded: s.Excluded,
			Pairs: map[pairKey]bool{{acc: acc.ID, asset: s.Asset.ID}: true}}
	case ByLabel:
		pairs := map[pairKey]bool{}
		for k := range s.Pairs {
			if k.acc == acc.ID {
				pairs[k] = true
			}
		}
		return Scope{Kind: ByLabel, Label: acc.Name, Excluded: s.Excluded, Pairs: pairs}
	}
	return out
}

// FilterScope keeps the breakdown lines that belong to s: positions s
// accepts and cash of accounts whose cash s accepts.
func FilterScope(lines []PositionLine, s Scope) []PositionLine {
	out := make([]PositionLine, 0, len(lines))
	for _, l := range lines {
		if l.Asset == nil {
			if s.hasCash(l.Account) {
				out = append(out, l)
			}
			continue
		}
		if s.hasAsset(l.Account, l.Asset) {
			out = append(out, l)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/portfolio -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/portfolio && git commit -m "portfolio: PairScope, EnvelopeScope and FilterScope helpers"
```

---

### Task 9: cli - shared resolveScope helper

**Files:**
- Modify: `internal/cli/helpers.go` (add resolveScope)
- Modify: `internal/cli/perf.go`, `internal/cli/value.go`, `internal/cli/chart.go` (use it; chart gains `--label`)

**Interfaces:**
- Produces: `resolveScope(b *domain.Book, ref, label string, exclude []string) (portfolio.Scope, error)`.

- [ ] **Step 1: Implement the helper**

In `helpers.go`:

```go
// resolveScope resolves the [scope]/--label/--exclude triple every read
// command shares: empty ref is the whole portfolio, --label restricts to
// labelled positions, --exclude prunes assets and tags the scope label.
func resolveScope(b *domain.Book, ref, label string, exclude []string) (portfolio.Scope, error) {
	if ref != "" && label != "" {
		return portfolio.Scope{}, fmt.Errorf("use either a [scope] argument or --label, not both")
	}
	var scope portfolio.Scope
	var err error
	if label != "" {
		scope, err = portfolio.LabelScope(b, label)
	} else {
		scope, err = portfolio.ParseScope(b, ref)
	}
	if err != nil {
		return portfolio.Scope{}, err
	}
	excluded, err := parseExclusions(b, exclude)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(excluded) > 0 {
		scope.Excluded = excluded
		scope.Label += " (excluding " + strings.Join(exclude, ",") + ")"
	}
	return scope, nil
}
```

Replace the equivalent inline blocks in `perf.go` (lines 36-64) and `value.go` (lines 37-65). In `chart.go`, add `var label string` + `cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")` and route through resolveScope too.

- [ ] **Step 2: Run the CLI tests, add the chart --label case**

Run: `go test ./internal/cli -count=1` - PASS, no behavior change for perf/value.
Add to the existing chart test file a case running `chart --label <name>` on the standard fixture and asserting it renders (exit 0, non-empty plot) and that `chart scope --label x` errors with the shared message.

- [ ] **Step 3: Commit**

```bash
git add internal/cli && git commit -m "cli: shared scope resolution; chart learns --label"
```

---

### Task 10: cli - `perf --tree`

**Files:**
- Modify: `internal/cli/perf.go`
- Modify: `internal/cli/cli_test.go` (or the file holding perf tests)
- Modify: `README.md` (perf recipes), `docs/superpowers/DECISIONS.md`

**Interfaces:**
- Consumes: `portfolio.Breakdown`, `portfolio.FilterScope`, `portfolio.AssetTree` (Task 7), `portfolio.PairScope`/`EnvelopeScope` (Task 8), `perf.PeriodRange`, `perf.TWR`, existing `window(res, from, to)` and `pctSigned` in perf.go, `tint` (couleur.go).
- Produces: `finador perf --tree` output: header line, column header `NET  1d  5d  1m  3m`, envelope-grouped rows, separator, TOTAL row.

- [ ] **Step 1: Write the failing test**

Fixture via the CLI like neighboring tests (temp db, `fakeSource`): one account "CTO Meridia" with a CW8.PA buy dated 2026-06-02 and a cash deposit (tracked cash), one account "Livret" with only cash. fakeSource already serves CW8.PA closes for 2026-06-01/05 (values will forward-fill). Then:

```go
// TestPerfTree: the tree shows an envelope row per account, an asset line
// with period returns, dashes for cash-only envelopes and windows the
// history does not cover, and a TOTAL row.
func TestPerfTree(t *testing.T) {
	db := … // build fixture with runNet helpers
	out := runNet(t, db, "perf", "--tree", "--to", "2026-06-05")
	for _, want := range []string{"CTO Meridia", "Amundi MSCI World", "Livret", "TOTAL", "NET"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// CW8 bought 2026-06-02, evaluated 2026-06-05: 1m/3m predate the series → dashes on that line.
	line := lineContaining(t, out, "Amundi")
	if strings.Count(line, "-") < 2 {
		t.Fatalf("expected dashed long windows on the asset line: %q", line)
	}
	// Livret is cash-only: all four period cells dashed.
	if l := lineContaining(t, out, "Livret"); strings.Count(l, "-") < 4 {
		t.Fatalf("cash-only envelope must be dashed: %q", l)
	}
	// --from is incompatible.
	if _, err := tryRunNet(t, db, "perf", "--tree", "--from", "2026-06-01"); err == nil {
		t.Fatal("perf --tree --from must error")
	}
}
```

(`lineContaining` is a three-line test helper: split on \n, return the first line containing the needle, t.Fatal otherwise.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestPerfTree -count=1`
Expected: FAIL (unknown flag: --tree).

- [ ] **Step 3: Implement**

In `perf.go`: add `var tree bool`, flag `cmd.Flags().BoolVar(&tree, "tree", false, "envelope-grouped tree: net value and 1d/5d/1m/3m returns per line")`, and in RunE after scope/display/evalTo resolution:

```go
			if tree {
				if from != "" {
					return errors.New("--tree shows fixed windows (1d, 5d, 1m, 3m): --from does not apply")
				}
				return perfTree(cmd, a, f.Book, scope, display, evalTo)
			}
```

New function (same file or `perftree.go` if perf.go grows past ~300 lines - prefer `internal/cli/perftree.go`):

```go
// perfTreePeriods are the tree's return columns, shortest first.
var perfTreePeriods = [4]string{"1d", "5d", "1m", "3m"}

// perfTree renders the scope as an envelope-grouped tree: net value per
// line, then flow-neutralized TWR over each period column. Every number
// agrees with the flat `perf` table run on the same sub-scope.
func perfTree(cmd *cobra.Command, a *app, b *domain.Book, scope portfolio.Scope, display domain.Currency, evalTo domain.Date) error {
	fx := market.Converter{FX: b.Market.FX}
	lines, err := portfolio.Breakdown(b, evalTo, display, fx)
	if err != nil {
		return err
	}
	envs := portfolio.AssetTree(portfolio.FilterScope(lines, scope))
	if len(envs) == 0 {
		return errors.New("nothing to show in this scope")
	}

	// cells computes the four period returns of one sub-scope.
	cells := func(sc portfolio.Scope) (texts [4]string, signs [4]float64) {
		for i := range texts {
			texts[i] = "-"
		}
		res, err := portfolio.Series(b, sc, domain.Date{}, evalTo, display, fx)
		if err != nil || len(res.Points) < 2 {
			return texts, signs
		}
		for i, name := range perfTreePeriods {
			from, _, perr := perf.PeriodRange(name, evalTo)
			if perr != nil || from.Before(res.Points[0].Date) {
				continue // window predates this line's track record
			}
			pts, fls := window(res, from, evalTo)
			if len(pts) < 2 {
				continue
			}
			r := perf.TWR(pts, fls)
			texts[i], signs[i] = pctSigned(r), r
		}
		return texts, signs
	}

	type row struct {
		label  string
		net    string
		cells  [4]string
		signs  [4]float64
		indent bool
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }
	dashes := func() (t [4]string, s [4]float64) { t = [4]string{"-", "-", "-", "-"}; return }

	var rows []row
	var totNet float64
	for _, env := range envs {
		totNet += env.Net
		hasSecurities := false
		for _, it := range env.Items {
			if it.Asset != nil {
				hasSecurities = true
			}
		}
		envTexts, envSigns := dashes()
		if hasSecurities { // a cash-only envelope has no market performance to show
			envTexts, envSigns = cells(portfolio.EnvelopeScope(scope, env.Account))
		}
		if len(env.Items) == 1 {
			it := env.Items[0]
			label := env.Account.Name
			if it.Asset != nil && it.Asset.ISIN != "" {
				label += " (" + it.Asset.ISIN + ")"
			}
			rows = append(rows, row{label: label, net: num(env.Net), cells: envTexts, signs: envSigns})
			continue
		}
		rows = append(rows, row{label: env.Account.Name, net: num(env.Net), cells: envTexts, signs: envSigns})
		for _, it := range env.Items {
			t, s := dashes()
			if it.Asset != nil {
				t, s = cells(portfolio.PairScope(env.Account, it.Asset))
			}
			rows = append(rows, row{label: "  " + it.Label(), net: num(it.Net), cells: t, signs: s, indent: true})
		}
	}
	totTexts, totSigns := cells(scope)

	out := cmd.OutOrStdout()
	colored := a.colorsEnabled(cmd)
	labelW, netW, cellW := len("TOTAL"), len("NET"), 7 // "+12.34%"
	for _, r := range rows {
		labelW = max(labelW, len([]rune(r.label)))
		netW = max(netW, len(r.net))
		for _, c := range r.cells {
			cellW = max(cellW, len(c))
		}
	}
	netW = max(netW, len(num(totNet)))

	pad := func(s string, w int) string {
		for len([]rune(s)) < w {
			s = " " + s
		}
		return s
	}
	printRow := func(label, net string, texts [4]string, signs [4]float64) {
		fmt.Fprintf(out, "%-*s  %s", labelW, label, pad(net, netW))
		for i, c := range texts {
			fmt.Fprintf(out, "  %s", tint(pad(c, cellW), signs[i], colored))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%s - performance (%s), as of %s\n\n", scope.Label, display, evalTo)
	printRow("", "NET", perfTreePeriods, [4]float64{})
	for _, r := range rows {
		printRow(r.label, r.net, r.cells, r.signs)
	}
	fmt.Fprintln(out, strings.Repeat("-", labelW+2+netW+4*(cellW+2)))
	printRow("TOTAL", num(totNet), totTexts, totSigns)
	return nil
}
```

(Keep godoc on every function; `perfTreePeriods` doubles as the header row.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli -count=1`
Expected: PASS.

- [ ] **Step 5: README + DECISIONS + commit**

README perf section gains, CLI-example style:

```sh
finador perf --tree        # per-envelope tree: net value, 1d/5d/1m/3m returns
finador perf pea --tree    # same, scoped
```

DECISIONS entry (French): chaque ligne de l'arbre = TWR de sa propre série (flux neutralisés, D8 respecté), cash à `-`, enveloppe cash-only à `-`; cohérence garantie avec `finador perf` sur le même sous-scope.

```bash
git add internal/cli README.md docs/superpowers/DECISIONS.md && git commit -m "cli: perf --tree, per-line net value and period returns"
```

---

### Task 11: cli - scoped export (+ ScopedRows)

**Files:**
- Modify: `internal/portfolio/export.go` (ScopedRows; AllRows delegates)
- Modify: `internal/portfolio/export_test.go`
- Modify: `internal/cli/export.go` ([scope], --label, --exclude)
- Modify: `internal/cli/cli_test.go` (scoped export test), `README.md`

**Interfaces:**
- Consumes: `Breakdown`, `FilterScope` (Task 8), `resolveScope` (Task 9).
- Produces: `portfolio.ScopedRows(b *domain.Book, s Scope, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error)`; `AllRows` becomes `ScopedRows` with `Scope{Kind: All}`.

- [ ] **Step 1: Write the failing test**

In `export_test.go`: on the standard fixture book, `ScopedRows` with a `ByAccount` scope returns only that account's assets and cash; with `All` it matches `AllRows` exactly (same rows, same order, same values).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/portfolio -run TestScopedRows -count=1` - FAIL (undefined).

- [ ] **Step 3: Implement ScopedRows**

```go
// ScopedRows values what the scope holds - securities and properties
// aggregated per asset, plus one cash row per account - sorted by gross
// descending. With an All scope it is exactly the full export.
func ScopedRows(b *domain.Book, s Scope, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error) {
	lines, err := Breakdown(b, at, ccy, fx)
	if err != nil {
		return nil, err
	}
	var rows []AssetRow
	assetRows := map[domain.AssetID]int{} // asset → index in rows, for aggregation
	for _, l := range FilterScope(lines, s) {
		if l.Asset == nil {
			rows = append(rows, AssetRow{Kind: "cash", Name: l.Account.Name, Gross: l.Gross, Net: l.Net, Currency: ccy})
			continue
		}
		if i, ok := assetRows[l.Asset.ID]; ok {
			rows[i].Gross += l.Gross
			rows[i].Net += l.Net
			continue
		}
		assetRows[l.Asset.ID] = len(rows)
		rows = append(rows, AssetRow{
			Kind:   l.Asset.Kind.String(),
			Ticker: l.Asset.Ticker, Name: l.Asset.Name, ISIN: l.Asset.ISIN,
			Gross: l.Gross, Net: l.Net, Currency: ccy,
		})
	}
	sortRows(rows)
	return rows, nil
}
```

Then `AllRows` body becomes `return ScopedRows(b, Scope{Kind: All}, at, ccy, fx)`; delete `AssetRows`/`CashRows` if nothing else uses them (grep first - the web may; keep them if used).

If the All-scope equality test shows cent-level float ordering differences versus the old AllRows, keep the old AllRows implementation and have the CLI use ScopedRows only when a scope is given - note it in the commit message. (Both paths use the same valuer math, so equality is expected.)

- [ ] **Step 4: Wire the CLI**

`export.go`: `Args: cobra.MaximumNArgs(1)`, add `--label`/`--exclude` flags, resolve via `resolveScope`, then `ScopedRows(b, scope, …)` for CSV and `FilterScope(lines, scope)` before `WriteAssetTree` for `--tree`. Example lines gain `finador export pea --tree`.

- [ ] **Step 5: CLI test + gate**

Add a CLI test: `export <account>` prints only that account's rows; `export --tree <account>` renders one envelope. Run `make check`. Expected: PASS.

- [ ] **Step 6: README + commit**

README export recipes gain the scoped forms.

```bash
git add internal/portfolio internal/cli README.md && git commit -m "cli: export accepts a scope, --label and --exclude"
```

---

### Task 12: cli - `value --tree`

**Files:**
- Modify: `internal/cli/value.go`
- Modify: `internal/cli/cli_test.go`, `README.md`

**Interfaces:**
- Consumes: `Breakdown` + `FilterScope` + `WriteAssetTree` (the export --tree path), `resolveScope`.

- [ ] **Step 1: Write the failing test**

CLI test: `value --tree` on the standard fixture prints the envelope tree (contains "Holdings in", an account name, "TOTAL"); scoped `value <account> --tree` prints only that envelope; `value --tree --what-if cw8=600` and `value --tree --by account` and `value --tree --gross` error.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli -run TestValueTree -count=1` - FAIL (unknown flag).

- [ ] **Step 3: Implement**

In `value.go`: add `var tree bool` + flag `"tree"` (same wording as export's). In RunE, after scope/date/display/FX resolution and before the `--by` switch:

```go
			if tree {
				if gross || by != "group" || len(whatIf) > 0 {
					return errors.New("--tree is incompatible with --gross, --by and --what-if")
				}
				lines, err := portfolio.Breakdown(b, date, display, market.Converter{FX: b.Market.FX})
				if err != nil {
					return err
				}
				return portfolio.WriteAssetTree(cmd.OutOrStdout(),
					portfolio.FilterScope(lines, scope), display, date)
			}
```

- [ ] **Step 4: Run + README + commit**

Run: `go test ./internal/cli -count=1` - PASS. README value section gains `finador value --tree` and `finador value pea --tree` lines.

```bash
git add internal/cli README.md && git commit -m "cli: value --tree, the envelope tree at any date and scope"
```

---

### Task 13: final gate and push

- [ ] **Step 1: Full gates in both repos**

Run: `cd ../pofo && make test && cd - && make check && make race`
Expected: all PASS.

- [ ] **Step 2: README coherence pass**

Re-read the README sections touched (perf, export, value, chart, the performance explainer): no XIRR left anywhere, recipes runnable as written, CLI-example style (no prose walls), no em-dashes.

- [ ] **Step 3: Live smoke test (real portfolio, read-only)**

Ask the user to run `! ./bin/finador perf` and `! ./bin/finador perf --tree` after `make build`: expect no XIRR column, no duplicated flat windows, fresh 1d, a coherent tree.

- [ ] **Step 4: Push both repos**

```bash
cd ../pofo && git push && cd - && git push
```
