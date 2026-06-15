# Finador phase B - marché & valorisation : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** La valeur du patrimoine - brut, impôt latent, net - à toute date, toute portée (tout / groupe / enveloppe / actif), toute devise d'affichage, avec prix Yahoo Finance, FX croisés par l'USD et dividendes automatiques, le tout en cache chiffré dans le fichier `.fin`.

**Architecture:** `domain` gagne les types de cache marché (séries de clôtures, FX, dividendes) stockés dans le Book. `market` = interface `Source` + client Yahoo (fixtures httptest), conversion FX par l'USD, orchestration du refresh. `portfolio` = rejeu du ledger (quantités, cash suivi/non suivi) puis valorisation avec fiscalité par enveloppe. La CLI gagne `value`, `refresh`, `--offline`, et la résolution Yahoo dans `asset add`.

**Tech Stack:** Go 1.26 pur, stdlib net/http + httptest, `import _ "time/tzdata"` pour les fuseaux des places boursières.

**Référence:** spec `docs/superpowers/specs/2026-06-09-finador-design.md` §5, §6 ; conventions du plan A (TDD strict, gofmt/vet silencieux, messages d'erreur français, pas de binaire committé, commits exacts).

**Décisions actées phase B :**
- Le fichier `.fin` reste en **version 1** : les champs marché sont des caches refetchables, un binaire plus ancien qui les perdrait ne perd aucune donnée utilisateur (décision D7, DECISIONS.md).
- Clôtures et calculs de valorisation en **float64** (l'exactitude décimale vit dans le ledger ; la valorisation est de l'analytique). Arrondi à 2 décimales uniquement à l'affichage.
- FX stocké **par devise vs USD** (série "valeur de 1 EUR en USD") ; toute conversion croise par l'USD. Une seule série par devise étrangère.
- `Statement` sur un actif : la valorisation marché **prime** si une série de prix existe ; sinon le dernier relevé ≤ date fait foi (biens, fonds non cotés). Le relevé vaut pour la position entière du couple (compte, actif), pas par part.
- Dividendes automatiques : crédités au cash des comptes **suivis** uniquement ; désactivés pour tout actif ayant au moins un `Dividend` manuel (toutes enveloppes confondues) ; montants bruts.
- Le refresh automatique avant `value` est **gracieux** : en cas d'échec réseau on continue sur le cache avec marqueurs « stale », jamais d'échec dur.

---

### Task B1: domain - types de cache marché

**Files:**
- Create: `internal/domain/marketdata.go`
- Test: `internal/domain/marketdata_test.go`
- Modify: `internal/domain/book.go` (champ Market)

- [ ] **Step 1: tests qui échouent**

`internal/domain/marketdata_test.go`:

```go
package domain

import (
	"encoding/json"
	"testing"
)

func d(s string) Date {
	dd, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func TestPriceSeriesAt(t *testing.T) {
	s := &PriceSeries{}
	s.Merge([]PricePoint{
		{Date: d("2026-06-01"), Close: 100},
		{Date: d("2026-06-03"), Close: 103},
		{Date: d("2026-06-05"), Close: 105},
	})
	for _, tc := range []struct {
		at    string
		want  float64
		wDate string
		ok    bool
	}{
		{"2026-06-05", 105, "2026-06-05", true},
		{"2026-06-04", 103, "2026-06-03", true}, // report de la dernière clôture
		{"2026-06-01", 100, "2026-06-01", true},
		{"2026-05-31", 0, "", false}, // avant le début de la série
		{"2026-07-01", 105, "2026-06-05", true},
	} {
		got, gDate, ok := s.At(d(tc.at))
		if ok != tc.ok || (ok && (got != tc.want || gDate != d(tc.wDate))) {
			t.Errorf("At(%s) = %v %v %v", tc.at, got, gDate, ok)
		}
	}
}

func TestPriceSeriesMergeUpsert(t *testing.T) {
	s := &PriceSeries{}
	s.Merge([]PricePoint{{Date: d("2026-06-01"), Close: 100}, {Date: d("2026-06-02"), Close: 101}})
	// chevauchement : le 02 est corrigé, le 03 ajouté ; l'ordre reste trié
	s.Merge([]PricePoint{{Date: d("2026-06-02"), Close: 102}, {Date: d("2026-06-03"), Close: 103}})
	if len(s.Points) != 3 {
		t.Fatalf("points = %d, attendu 3", len(s.Points))
	}
	if v, _, _ := s.At(d("2026-06-02")); v != 102 {
		t.Errorf("upsert raté: %v", v)
	}
	last, ok := s.Last()
	if !ok || last.Close != 103 {
		t.Errorf("Last = %+v %v", last, ok)
	}
}

func TestMarketDataLazyAndJSON(t *testing.T) {
	b := NewBook()
	ps := b.Market.Price("cw8")
	ps.Merge([]PricePoint{{Date: d("2026-06-01"), Close: 550}})
	b.Market.FXSeries(EUR).Merge([]PricePoint{{Date: d("2026-06-01"), Close: 1.08}})
	b.Market.Dividends = map[AssetID][]DividendEvent{
		"cw8": {{ExDate: d("2026-03-10"), Amount: 1.5}},
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	back := NewBook()
	if err := json.Unmarshal(raw, back); err != nil {
		t.Fatal(err)
	}
	if v, _, ok := back.Market.Price("cw8").At(d("2026-06-02")); !ok || v != 550 {
		t.Fatalf("prix perdu au roundtrip: %v %v", v, ok)
	}
	if v, _, ok := back.Market.FXSeries(EUR).At(d("2026-06-01")); !ok || v != 1.08 {
		t.Fatalf("fx perdu: %v %v", v, ok)
	}
	if len(back.Market.Dividends["cw8"]) != 1 {
		t.Fatalf("dividendes perdus")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/domain/`
Expected: FAIL - undefined: PriceSeries, etc.

- [ ] **Step 3: implémenter**

`internal/domain/marketdata.go`:

```go
package domain

import "slices"

// PricePoint is one daily close. Closes are analytics data: float64 is fine -
// decimal exactness lives in the ledger, not in market quotes.
type PricePoint struct {
	Date  Date    `json:"d"`
	Close float64 `json:"c"`
}

// PriceSeries is a date-sorted daily close series with forward-fill lookup.
// FetchedAt records the last refresh day, even when no new point appeared
// (week-ends) - staleness is judged on it, not on the last point.
type PriceSeries struct {
	Points    []PricePoint `json:"points"`
	FetchedAt Date         `json:"fetchedAt"`
}

// At returns the last close at or before d (forward-fill), with its date.
func (s *PriceSeries) At(d Date) (float64, Date, bool) {
	if s == nil {
		return 0, Date{}, false
	}
	i, found := slices.BinarySearchFunc(s.Points, d, func(p PricePoint, t Date) int {
		return p.Date.Time().Compare(t.Time())
	})
	if found {
		return s.Points[i].Close, s.Points[i].Date, true
	}
	if i == 0 {
		return 0, Date{}, false
	}
	p := s.Points[i-1]
	return p.Close, p.Date, true
}

// Merge upserts points, keeping the series sorted and deduplicated by date.
func (s *PriceSeries) Merge(pts []PricePoint) {
	for _, p := range pts {
		i, found := slices.BinarySearchFunc(s.Points, p.Date, func(q PricePoint, t Date) int {
			return q.Date.Time().Compare(t.Time())
		})
		if found {
			s.Points[i] = p
		} else {
			s.Points = slices.Insert(s.Points, i, p)
		}
	}
}

func (s *PriceSeries) Last() (PricePoint, bool) {
	if s == nil || len(s.Points) == 0 {
		return PricePoint{}, false
	}
	return s.Points[len(s.Points)-1], true
}

// DividendEvent is one gross per-share distribution.
type DividendEvent struct {
	ExDate Date    `json:"exDate"`
	Amount float64 `json:"amount"`
}

// MarketData is the cached public market state. It lives inside the encrypted
// Book: the list of held tickers is sensitive metadata. Everything here is
// refetchable - losing it costs one refresh, never user data.
type MarketData struct {
	Prices    map[AssetID]*PriceSeries    `json:"prices,omitempty"`
	FX        map[Currency]*PriceSeries   `json:"fx,omitempty"` // valeur de 1 unité en USD
	Dividends map[AssetID][]DividendEvent `json:"dividends,omitempty"`
}

// Price returns the price series of an asset, creating it lazily.
func (m *MarketData) Price(id AssetID) *PriceSeries {
	if m.Prices == nil {
		m.Prices = map[AssetID]*PriceSeries{}
	}
	if m.Prices[id] == nil {
		m.Prices[id] = &PriceSeries{}
	}
	return m.Prices[id]
}

// FXSeries returns the USD-value series of a currency, creating it lazily.
func (m *MarketData) FXSeries(c Currency) *PriceSeries {
	if m.FX == nil {
		m.FX = map[Currency]*PriceSeries{}
	}
	if m.FX[c] == nil {
		m.FX[c] = &PriceSeries{}
	}
	return m.FX[c]
}
```

Dans `internal/domain/book.go`, ajouter le champ au struct Book (après Config) :

```go
	Market   MarketData        `json:"market"`
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/domain/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/domain
git commit -m "feat(domain): cache marché - séries de clôtures, FX vs USD, dividendes"
```

---

### Task B2: market - interface Source et client Yahoo

**Files:**
- Create: `internal/market/source.go`, `internal/market/yahoo.go`
- Test: `internal/market/yahoo_test.go`

- [ ] **Step 1: tests qui échouent**

`internal/market/yahoo_test.go`:

```go
package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"finador/internal/domain"
)

// Réponse chart réaliste : 3 jours dont un close null (jour férié), un
// dividende, fuseau Europe/Paris (timestamps à 09:00 locale → 07:00 UTC).
const chartCW8 = `{"chart":{"result":[{"meta":{"currency":"EUR","symbol":"CW8.PA","exchangeTimezoneName":"Europe/Paris"},"timestamp":[1780297200,1780383600,1780470000],"events":{"dividends":{"1780297200":{"amount":1.5,"date":1780297200}}},"indicators":{"quote":[{"close":[550.0,null,553.25]}]}}],"error":null}}`

const searchCW8 = `{"quotes":[{"symbol":"CW8.PA","longname":"Amundi MSCI World UCITS ETF","quoteType":"ETF"},{"symbol":"CW8.MI","longname":"Amundi MSCI World (Milan)","quoteType":"ETF"}]}`

const chartNotFound = `{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found, symbol may be delisted"}}}`

func testYahoo(t *testing.T, handler http.HandlerFunc) *Yahoo {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	y := NewYahoo()
	y.BaseURL = srv.URL
	return y
}

func TestYahooDaily(t *testing.T) {
	y := testYahoo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/finance/chart/CW8.PA" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("events") != "div" || r.URL.Query().Get("interval") != "1d" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(chartCW8))
	})
	got, err := y.Daily(context.Background(), "CW8.PA", mustDate("2026-06-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != domain.EUR {
		t.Errorf("ccy = %s", got.Currency)
	}
	// le close null du 2e jour est sauté
	if len(got.Closes) != 2 {
		t.Fatalf("closes = %+v", got.Closes)
	}
	// 1780297200 = 2026-06-01 09:00 Europe/Paris
	if got.Closes[0].Date != mustDate("2026-06-01") || got.Closes[0].Close != 550 {
		t.Errorf("close[0] = %+v", got.Closes[0])
	}
	if got.Closes[1].Date != mustDate("2026-06-03") || got.Closes[1].Close != 553.25 {
		t.Errorf("close[1] = %+v", got.Closes[1])
	}
	if len(got.Dividends) != 1 || got.Dividends[0].Amount != 1.5 || got.Dividends[0].ExDate != mustDate("2026-06-01") {
		t.Errorf("dividends = %+v", got.Dividends)
	}
}

func TestYahooDailyNotFound(t *testing.T) {
	y := testYahoo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(chartNotFound))
	})
	if _, err := y.Daily(context.Background(), "NOPE", mustDate("2026-06-01")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestYahooResolve(t *testing.T) {
	y := testYahoo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/finance/search" || r.URL.Query().Get("q") != "amundi msci world" {
			t.Errorf("req = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Write([]byte(searchCW8))
	})
	info, err := y.Resolve(context.Background(), "amundi msci world")
	if err != nil {
		t.Fatal(err)
	}
	if info.Symbol != "CW8.PA" || info.Name != "Amundi MSCI World UCITS ETF" {
		t.Errorf("info = %+v", info)
	}
}

func TestYahooResolveNotFound(t *testing.T) {
	y := testYahoo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"quotes":[]}`))
	})
	if _, err := y.Resolve(context.Background(), "zzz"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestYahooRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	y := testYahoo(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(chartCW8))
	})
	y.RetryWait = 0 // pas d'attente en test
	if _, err := y.Daily(context.Background(), "CW8.PA", mustDate("2026-06-01")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func mustDate(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/market/`
Expected: FAIL - undefined: Yahoo, NewYahoo.

- [ ] **Step 3: implémenter**

`internal/market/source.go`:

```go
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
```

`internal/market/yahoo.go`:

```go
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
	_ "time/tzdata" // fuseaux des places boursières, sans dépendre de l'OS

	"finador/internal/domain"
)

// Yahoo is the default Source: the unofficial but stable Yahoo Finance API.
// No key, no auth - just a browser-looking User-Agent and polite retries.
type Yahoo struct {
	BaseURL   string
	Client    *http.Client
	RetryWait time.Duration
}

func NewYahoo() *Yahoo {
	return &Yahoo{
		BaseURL:   "https://query1.finance.yahoo.com",
		Client:    &http.Client{Timeout: 15 * time.Second},
		RetryWait: 2 * time.Second,
	}
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// get fetches a JSON endpoint with one retry on 429/5xx.
func (y *Yahoo) get(ctx context.Context, path string, query url.Values, into any) error {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, y.BaseURL+path+"?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := y.Client.Do(req)
		if err != nil {
			return err
		}
		retriable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retriable && attempt < 2 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(y.RetryWait << attempt):
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("yahoo %s: HTTP %d", path, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(into)
	}
}

func (y *Yahoo) Resolve(ctx context.Context, query string) (SymbolInfo, error) {
	var resp struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			LongName  string `json:"longname"`
			ShortName string `json:"shortname"`
		} `json:"quotes"`
	}
	q := url.Values{"q": {query}, "quotesCount": {"5"}, "newsCount": {"0"}}
	if err := y.get(ctx, "/v1/finance/search", q, &resp); err != nil {
		return SymbolInfo{}, err
	}
	for _, quote := range resp.Quotes {
		if quote.Symbol == "" {
			continue
		}
		name := quote.LongName
		if name == "" {
			name = quote.ShortName
		}
		return SymbolInfo{Symbol: quote.Symbol, Name: name}, nil
	}
	return SymbolInfo{}, fmt.Errorf("symbole pour %q: %w", query, domain.ErrNotFound)
}

func (y *Yahoo) Daily(ctx context.Context, symbol string, from domain.Date) (DailyData, error) {
	var resp struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency             string `json:"currency"`
					ExchangeTimezoneName string `json:"exchangeTimezoneName"`
				} `json:"meta"`
				Timestamp []int64 `json:"timestamp"`
				Events    struct {
					Dividends map[string]struct {
						Amount float64 `json:"amount"`
						Date   int64   `json:"date"`
					} `json:"dividends"`
				} `json:"events"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error any `json:"error"`
		} `json:"chart"`
	}
	q := url.Values{
		"period1":  {strconv.FormatInt(from.Time().Unix(), 10)},
		"period2":  {strconv.FormatInt(time.Now().Unix()+86400, 10)},
		"interval": {"1d"},
		"events":   {"div"},
	}
	if err := y.get(ctx, "/v8/finance/chart/"+url.PathEscape(symbol), q, &resp); err != nil {
		return DailyData{}, err
	}
	if len(resp.Chart.Result) == 0 {
		return DailyData{}, fmt.Errorf("cours de %q: %w", symbol, domain.ErrNotFound)
	}
	r := resp.Chart.Result[0]

	loc, err := time.LoadLocation(r.Meta.ExchangeTimezoneName)
	if err != nil {
		loc = time.UTC
	}
	dateOf := func(ts int64) domain.Date { return domain.DateOf(time.Unix(ts, 0).In(loc)) }

	out := DailyData{Currency: domain.Currency(r.Meta.Currency)}
	var closes []*float64
	if len(r.Indicators.Quote) > 0 {
		closes = r.Indicators.Quote[0].Close
	}
	for i, ts := range r.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue // jour férié ou close manquant
		}
		out.Closes = append(out.Closes, domain.PricePoint{Date: dateOf(ts), Close: *closes[i]})
	}
	for _, div := range r.Events.Dividends {
		out.Dividends = append(out.Dividends, domain.DividendEvent{ExDate: dateOf(div.Date), Amount: div.Amount})
	}
	slices.SortFunc(out.Dividends, func(a, b domain.DividendEvent) int {
		return a.ExDate.Time().Compare(b.ExDate.Time())
	})
	return out, nil
}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/market/ && go test ./...` → PASS. gofmt/vet silencieux. Vérifier que le timestamp de la fixture est cohérent : `TZ=Europe/Paris date -r 1780297200` doit donner le 1er juin 2026 ; sinon ajuster la fixture (timestamps = 09:00 locale des 1/2/3 juin 2026) et les assertions en conséquence - signaler tout ajustement.

- [ ] **Step 5: commit**

```bash
git add internal/market
git commit -m "feat(market): interface Source et client Yahoo - clôtures, dividendes, recherche, retries"
```

---

### Task B3: market - conversion FX par l'USD

**Files:**
- Create: `internal/market/convert.go`
- Test: `internal/market/convert_test.go`

- [ ] **Step 1: tests qui échouent**

`internal/market/convert_test.go`:

```go
package market

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

func testFX() Converter {
	eur := &domain.PriceSeries{}
	eur.Merge([]domain.PricePoint{
		{Date: mustDate("2026-06-01"), Close: 1.10},
		{Date: mustDate("2026-06-03"), Close: 1.12},
	})
	gbp := &domain.PriceSeries{}
	gbp.Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 1.30}})
	return Converter{FX: map[domain.Currency]*domain.PriceSeries{
		domain.EUR: eur, "GBP": gbp,
	}}
}

func TestConvert(t *testing.T) {
	c := testFX()
	for _, tc := range []struct {
		amount   float64
		from, to domain.Currency
		at       string
		want     float64
	}{
		{100, domain.EUR, domain.EUR, "2026-06-01", 100},      // identité
		{100, domain.EUR, domain.USD, "2026-06-01", 110},      // direct
		{110, domain.USD, domain.EUR, "2026-06-01", 100},      // inverse
		{100, domain.EUR, domain.USD, "2026-06-02", 110},      // forward-fill
		{100, "GBP", domain.EUR, "2026-06-03", 130.0 / 1.12},  // croisé par USD
	} {
		got, err := c.Convert(tc.amount, tc.from, tc.to, mustDate(tc.at))
		if err != nil {
			t.Fatalf("Convert(%v %s→%s): %v", tc.amount, tc.from, tc.to, err)
		}
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("Convert(%v %s→%s @%s) = %v, attendu %v", tc.amount, tc.from, tc.to, tc.at, got, tc.want)
		}
	}
}

func TestConvertMissingRate(t *testing.T) {
	c := testFX()
	_, err := c.Convert(100, "JPY", domain.EUR, mustDate("2026-06-01"))
	if err == nil || !strings.Contains(err.Error(), "JPY") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/market/` → FAIL - undefined: Converter.

- [ ] **Step 3: implémenter**

`internal/market/convert.go`:

```go
package market

import (
	"fmt"

	"finador/internal/domain"
)

// Converter converts amounts between currencies by crossing through the USD,
// using the cached FX series (value of one unit in USD).
type Converter struct {
	FX map[domain.Currency]*domain.PriceSeries
}

// usdValue returns how many USD one unit of c is worth at d.
func (cv Converter) usdValue(c domain.Currency, d domain.Date) (float64, error) {
	if c == domain.USD {
		return 1, nil
	}
	rate, _, ok := cv.FX[c].At(d) // At est nil-safe
	if !ok {
		return 0, fmt.Errorf("cours de change %s manquant au %s - lancez « finador refresh »", c, d)
	}
	return rate, nil
}

// Rate returns the multiplier turning an amount in from into to, at date d.
func (cv Converter) Rate(from, to domain.Currency, d domain.Date) (float64, error) {
	if from == to {
		return 1, nil
	}
	f, err := cv.usdValue(from, d)
	if err != nil {
		return 0, err
	}
	t, err := cv.usdValue(to, d)
	if err != nil {
		return 0, err
	}
	return f / t, nil
}

func (cv Converter) Convert(amount float64, from, to domain.Currency, d domain.Date) (float64, error) {
	rate, err := cv.Rate(from, to, d)
	if err != nil {
		return 0, err
	}
	return amount * rate, nil
}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/market/ && go test ./...` → PASS.

- [ ] **Step 5: commit**

```bash
git add internal/market
git commit -m "feat(market): conversion de devises croisée par l'USD"
```

---

### Task B4: market - orchestration du refresh

**Files:**
- Create: `internal/market/refresh.go`
- Modify: `internal/domain/date.go` (méthode AddDays)
- Test: `internal/market/refresh_test.go`, `internal/domain/date_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

Ajouter à `internal/domain/date_test.go`:

```go
func TestDateAddDays(t *testing.T) {
	dd, _ := ParseDate("2026-06-01")
	if got := dd.AddDays(-7).String(); got != "2026-05-25" {
		t.Errorf("AddDays(-7) = %s", got)
	}
	if got := dd.AddDays(30).String(); got != "2026-07-01" {
		t.Errorf("AddDays(30) = %s", got)
	}
}
```

`internal/market/refresh_test.go`:

```go
package market

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// fakeSource scripte les réponses et enregistre les appels.
type fakeSource struct {
	calls []string // "DAILY sym from", "RESOLVE q"
	daily map[string]DailyData
	fail  map[string]bool
}

func (f *fakeSource) Resolve(_ context.Context, q string) (SymbolInfo, error) {
	f.calls = append(f.calls, "RESOLVE "+q)
	return SymbolInfo{Symbol: strings.ToUpper(q)}, nil
}

func (f *fakeSource) Daily(_ context.Context, sym string, from domain.Date) (DailyData, error) {
	f.calls = append(f.calls, "DAILY "+sym+" "+from.String())
	if f.fail[sym] {
		return DailyData{}, domain.ErrNotFound
	}
	return f.daily[sym], nil
}

func bookWithTrade(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-05-15"), Account: "pea", Asset: "cw8",
		Kind: domain.Buy, Quantity: decimal.NewFromInt(10),
		Amount: domain.Money{Amount: decimal.NewFromInt(5500), Currency: domain.EUR}})
	return b
}

func TestRefreshFetchesFromFirstTx(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA":   {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 550}}},
		"EURUSD=X": {Currency: domain.USD, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 1.1}}},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings: %v", sum.Warnings)
	}
	// prix demandés depuis la 1re transaction − 7 jours
	wantCall := "DAILY CW8.PA " + mustDate("2026-05-15").AddDays(-7).String()
	if !contains(src.calls, wantCall) {
		t.Errorf("appels = %v, attendu %q", src.calls, wantCall)
	}
	// série et FX en cache, FetchedAt posé à aujourd'hui
	if _, _, ok := b.Market.Price("cw8").At(mustDate("2026-05-15")); !ok {
		t.Error("série prix absente")
	}
	if _, _, ok := b.Market.FXSeries(domain.EUR).At(mustDate("2026-05-15")); !ok {
		t.Error("série FX absente")
	}
	if b.Market.Price("cw8").FetchedAt != domain.Today() {
		t.Error("FetchedAt non posé")
	}
}

func TestRefreshSkipsFreshSeries(t *testing.T) {
	b := bookWithTrade(t)
	b.Market.Price("cw8").FetchedAt = domain.Today()
	b.Market.FXSeries(domain.EUR).FetchedAt = domain.Today()
	src := &fakeSource{}
	Refresh(context.Background(), b, src, false)
	if len(src.calls) != 0 {
		t.Fatalf("séries fraîches refetchées: %v", src.calls)
	}
	// force passe outre
	src.daily = map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}
	Refresh(context.Background(), b, src, true)
	if len(src.calls) != 2 {
		t.Fatalf("force inopérant: %v", src.calls)
	}
}

func TestRefreshIncrementalFrom(t *testing.T) {
	b := bookWithTrade(t)
	b.Market.Price("cw8").Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}})
	src := &fakeSource{daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}}
	Refresh(context.Background(), b, src, false)
	// repart de la DERNIÈRE clôture connue (elle peut avoir bougé en séance)
	if !contains(src.calls, "DAILY CW8.PA 2026-06-01") {
		t.Errorf("appels = %v", src.calls)
	}
}

func TestRefreshWarnsAndContinues(t *testing.T) {
	b := bookWithTrade(t)
	if err := b.AddAsset(&domain.Asset{ID: "dead", Kind: domain.Security, Name: "Dead",
		Ticker: "DEAD.PA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{
		daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}},
		fail:  map[string]bool{"DEAD.PA": true},
	}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "DEAD.PA") {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
	if !contains(src.calls, "DAILY EURUSD=X "+mustDate("2026-05-15").AddDays(-7).String()) {
		t.Errorf("le FX aurait dû être rafraîchi malgré l'échec: %v", src.calls)
	}
}

func TestRefreshCurrencyMismatchWarning(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA":   {Currency: domain.USD}, // l'actif est déclaré EUR
		"EURUSD=X": {},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "USD") {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/market/ ./internal/domain/` → FAIL - undefined: Refresh, AddDays.

- [ ] **Step 3: implémenter**

Dans `internal/domain/date.go`, ajouter :

```go
// AddDays returns the date n days later (negative n: earlier).
func (d Date) AddDays(n int) Date { return DateOf(d.Time().AddDate(0, 0, n)) }
```

`internal/market/refresh.go`:

```go
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
		data, err := src.Daily(ctx, asset.Ticker, priceFetchFrom(b, asset.ID, series))
		if err != nil {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%s: %v", asset.Ticker, err))
			continue
		}
		series.Merge(data.Closes)
		series.FetchedAt = today
		mergeDividends(&b.Market, asset.ID, data.Dividends)
		if data.Currency != "" && data.Currency != asset.Currency {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf(
				"%s cote en %s mais l'actif est déclaré en %s", asset.Ticker, data.Currency, asset.Currency))
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
		data, err := src.Daily(ctx, symbol, from)
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
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/market/ ./internal/domain/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/market internal/domain
git commit -m "feat(market): refresh incrémental gracieux - prix, FX, dividendes"
```

---

### Task B5: portfolio - rejeu du ledger

**Files:**
- Create: `internal/portfolio/replay.go`
- Test: `internal/portfolio/replay_test.go`

- [ ] **Step 1: tests qui échouent**

`internal/portfolio/replay_test.go`:

```go
package portfolio

import (
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

func mustDate(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eur(s string) domain.Money {
	return domain.Money{Amount: dec(s), Currency: domain.EUR}
}

// fixture : PEA avec cw8 (2 achats, 1 vente) ; CTO avec cw8 aussi (multi-comptes) ;
// Livret (cash pur) ; compte « untracked » sans aucun mouvement de cash pur.
func sampleBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	for _, acc := range []*domain.Account{
		{ID: "pea", Name: "PEA", Currency: domain.EUR},
		{ID: "cto", Name: "CTO", Currency: domain.EUR},
		{ID: "livret", Name: "Livret", Currency: domain.EUR},
	} {
		if err := b.AddAccount(acc); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions/monde"}); err != nil {
		t.Fatal(err)
	}
	txs := []domain.Transaction{
		{Date: mustDate("2026-01-10"), Account: "pea", Kind: domain.Deposit, Amount: eur("10000")},
		{Date: mustDate("2026-01-15"), Account: "pea", Asset: "cw8", Kind: domain.Buy, Quantity: dec("10"), Amount: eur("5000")},
		{Date: mustDate("2026-02-15"), Account: "pea", Asset: "cw8", Kind: domain.Buy, Quantity: dec("5"), Amount: eur("2750")},
		{Date: mustDate("2026-03-15"), Account: "pea", Asset: "cw8", Kind: domain.Sell, Quantity: dec("3"), Amount: eur("1800")},
		{Date: mustDate("2026-01-20"), Account: "cto", Asset: "cw8", Kind: domain.Buy, Quantity: dec("2"), Amount: eur("1100")},
		{Date: mustDate("2026-01-05"), Account: "livret", Kind: domain.Statement, Amount: eur("12000")},
	}
	for _, tx := range txs {
		b.Add(tx)
	}
	return b
}

func TestHoldings(t *testing.T) {
	b := sampleBook(t)
	hs := Holdings(b, mustDate("2026-12-31"))
	if len(hs) != 2 {
		t.Fatalf("holdings = %d, attendu 2 (pea/cw8 et cto/cw8)", len(hs))
	}
	if !Quantity(b, "pea", "cw8", mustDate("2026-12-31")).Equal(dec("12")) {
		t.Errorf("qté pea = %s", Quantity(b, "pea", "cw8", mustDate("2026-12-31")))
	}
	// à une date antérieure, le rejeu s'arrête là
	if !Quantity(b, "pea", "cw8", mustDate("2026-02-01")).Equal(dec("10")) {
		t.Errorf("qté pea au 1er févr = %s", Quantity(b, "pea", "cw8", mustDate("2026-02-01")))
	}
	if !Quantity(b, "cto", "cw8", mustDate("2026-12-31")).Equal(dec("2")) {
		t.Errorf("qté cto = %s", Quantity(b, "cto", "cw8", mustDate("2026-12-31")))
	}
}

func TestHoldingsDropsZeroPositions(t *testing.T) {
	b := sampleBook(t)
	b.Add(domain.Transaction{Date: mustDate("2026-04-01"), Account: "cto", Asset: "cw8",
		Kind: domain.Sell, Quantity: dec("2"), Amount: eur("1200")})
	hs := Holdings(b, mustDate("2026-12-31"))
	if len(hs) != 1 || hs[0].Account.ID != "pea" {
		t.Fatalf("holdings = %+v", hs)
	}
}

func TestCashTracked(t *testing.T) {
	b := sampleBook(t)
	for acc, want := range map[domain.AccountID]bool{
		"pea":    true,  // a un Deposit
		"livret": true,  // a un Statement cash
		"cto":    false, // n'a que des trades
	} {
		if got := CashTracked(b, acc); got != want {
			t.Errorf("CashTracked(%s) = %v, attendu %v", acc, got, want)
		}
	}
	// un Statement SUR ACTIF (estimation de bien) ne rend pas le cash suivi
	if err := b.AddAsset(&domain.Asset{ID: "maison", Kind: domain.Property, Name: "Maison", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "cto", Asset: "maison",
		Kind: domain.Statement, Amount: eur("450000")})
	if CashTracked(b, "cto") {
		t.Error("un Statement d'actif ne doit pas activer le suivi du cash")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/portfolio/` → FAIL - undefined: Holdings.

- [ ] **Step 3: implémenter**

`internal/portfolio/replay.go`:

```go
// Package portfolio replays the ledger and values any scope of the patrimoine.
// It depends on domain only: prices come from the Book's market cache, currency
// conversion through the FX interface.
package portfolio

import (
	"slices"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// Holding is the replayed quantity of one (account, asset) pair.
type Holding struct {
	Account *domain.Account
	Asset   *domain.Asset
	Qty     decimal.Decimal
}

// Holdings replays Buy/Sell up to asOf and returns the non-zero positions,
// in first-seen ledger order.
func Holdings(b *domain.Book, asOf domain.Date) []Holding {
	type key struct {
		acc   domain.AccountID
		asset domain.AssetID
	}
	qty := map[key]decimal.Decimal{}
	var order []key
	for _, t := range Sorted(b) {
		if asOf.Before(t.Date) || t.Asset == "" {
			continue
		}
		k := key{t.Account, t.Asset}
		switch t.Kind {
		case domain.Buy:
			if _, seen := qty[k]; !seen {
				order = append(order, k)
			}
			qty[k] = qty[k].Add(t.Quantity)
		case domain.Sell:
			if _, seen := qty[k]; !seen {
				order = append(order, k)
			}
			qty[k] = qty[k].Sub(t.Quantity)
		}
	}
	var out []Holding
	for _, k := range order {
		if qty[k].IsZero() {
			continue
		}
		acc, errA := b.Account(string(k.acc))
		asset, errB := b.Asset(string(k.asset))
		if errA != nil || errB != nil {
			continue // référence orpheline : ignorée, le ledger reste la vérité
		}
		out = append(out, Holding{Account: acc, Asset: asset, Qty: qty[k]})
	}
	return out
}

// Quantity replays the quantity of one asset inside one account at asOf.
func Quantity(b *domain.Book, acc domain.AccountID, asset domain.AssetID, asOf domain.Date) decimal.Decimal {
	q := decimal.Zero
	for _, t := range Sorted(b) {
		if asOf.Before(t.Date) || t.Account != acc || t.Asset != asset {
			continue
		}
		switch t.Kind {
		case domain.Buy:
			q = q.Add(t.Quantity)
		case domain.Sell:
			q = q.Sub(t.Quantity)
		}
	}
	return q
}

// CashTracked reports whether the account's cash is tracked: any pure-cash
// Statement, Deposit or Withdraw makes it so. Otherwise trades are treated
// as external flows (spec §3) and the account carries no cash.
func CashTracked(b *domain.Book, acc domain.AccountID) bool {
	for _, t := range b.Transactions {
		if t.Account != acc || t.Asset != "" {
			continue
		}
		switch t.Kind {
		case domain.Statement, domain.Deposit, domain.Withdraw:
			return true
		}
	}
	return false
}

// Sorted returns the ledger in replay order: (date, id).
func Sorted(b *domain.Book) []*domain.Transaction {
	txs := slices.Clone(b.Transactions)
	slices.SortStableFunc(txs, func(x, y *domain.Transaction) int {
		if c := x.Date.Time().Compare(y.Date.Time()); c != 0 {
			return c
		}
		switch {
		case x.ID < y.ID:
			return -1
		case x.ID > y.ID:
			return 1
		}
		return 0
	})
	return txs
}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/portfolio/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/portfolio
git commit -m "feat(portfolio): rejeu du ledger - positions, quantités, cash suivi"
```

---

### Task B6: portfolio - portées et valorisation brut/impôt/net

**Files:**
- Create: `internal/portfolio/scope.go`, `internal/portfolio/value.go`
- Test: `internal/portfolio/value_test.go`

Règles (de la spec §3 et §6, décisions en tête de plan) :
- Titres : `quantité × clôture(at, forward-fill) × FX(at)` ; sans série de prix, dernier relevé du couple (compte, actif) ; sinon 0 + marqueur. Cours plus vieux que 5 jours → marqueur « stale ».
- Biens : dernier relevé ≤ at (escalier).
- Cash des comptes suivis : ancre = dernier relevé cash ≤ at, puis flux postérieurs (±Deposit/Withdraw/Buy/Sell/Dividend manuel/Fee) convertis vers la devise du compte à leur date, + dividendes automatiques (actifs sans Dividend manuel, qty à l'ex-date), solde converti à `at`.
- Impôt par ligne : position par position (TaxOnValue : valeur×taux ; TaxOnGains : max(0, valeur − coût moyen converti aux dates de flux)×taux ; bien : base = premier relevé). Impôt TOTAL des portées All/Account : règle d'enveloppe exacte (TaxOnGains : base = apports nets si cash suivi, sinon achats−ventes). Si l'écart ligne/total dépasse 1 centime → TaxNote l'explique.
- Portées : "" → tout ; préfixe de groupe (insensible à la casse, par segments) ; enveloppe ; actif - dans cet ordre, l'ambiguïté se propage.

- [ ] **Step 1: tests qui échouent**

`internal/portfolio/value_test.go`:

```go
package portfolio

import (
	"errors"
	"strings"
	"testing"

	"finador/internal/domain"
)

// identityFX convertit 1:1 entre devises identiques et échoue sinon,
// sauf si on fournit un taux fixe pour un couple.
type fxStub struct{ rates map[string]float64 } // "EUR→USD" → 1.10

func (f fxStub) Convert(amount float64, from, to domain.Currency, _ domain.Date) (float64, error) {
	if from == to {
		return amount, nil
	}
	if r, ok := f.rates[string(from)+"→"+string(to)]; ok {
		return amount * r, nil
	}
	return 0, errors.New("taux absent: " + string(from) + "→" + string(to))
}

func approx(t *testing.T, what string, got, want float64) {
	t.Helper()
	if d := got - want; d > 0.005 || d < -0.005 {
		t.Errorf("%s = %.4f, attendu %.4f", what, got, want)
	}
}

// fixture riche : PEA (gains:17.2%, cash suivi), CTO (gains:30%, cash non
// suivi), Immo (gains:30%) avec un bien à deux relevés.
func valuationBook(t *testing.T) *domain.Book {
	t.Helper()
	b := sampleBook(t) // de replay_test.go : pea/cto/livret + cw8 + trades
	pea, _ := b.Account("pea")
	pea.Tax, _ = domain.ParseTaxRule("gains:17.2%")
	cto, _ := b.Account("cto")
	cto.Tax, _ = domain.ParseTaxRule("gains:30%")
	if err := b.AddAccount(&domain.Account{ID: "immo", Name: "Immo", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	immo, _ := b.Account("immo")
	immo.Tax, _ = domain.ParseTaxRule("gains:30%")
	if err := b.AddAsset(&domain.Asset{ID: "maison", Kind: domain.Property,
		Name: "Maison à Rénover", Currency: domain.EUR, Group: "immo"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "immo", Asset: "maison",
		Kind: domain.Statement, Amount: eur("400000")})
	b.Add(domain.Transaction{Date: mustDate("2026-06-01"), Account: "immo", Asset: "maison",
		Kind: domain.Statement, Amount: eur("450000")})
	// série de prix cw8 : clôture 560 le 5 juin
	b.Market.Price("cw8").Merge([]domain.PricePoint{
		{Date: mustDate("2026-03-20"), Close: 540},
		{Date: mustDate("2026-06-05"), Close: 560},
	})
	return b
}

func scopeOf(t *testing.T, b *domain.Book, ref string) Scope {
	t.Helper()
	s, err := ParseScope(b, ref)
	if err != nil {
		t.Fatalf("ParseScope(%q): %v", ref, err)
	}
	return s
}

func TestValueAll(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA : 12 × 560 = 6720 ; cash suivi = 10000 − 5000 − 2750 + 1800 = 4050
	// CTO : 2 × 560 = 1120 ; cash non suivi
	// Livret : relevé 12000 ; Maison : relevé 450000
	approx(t, "gross", v.Gross, 6720+4050+1120+12000+450000)
	// impôt exact par enveloppe :
	// PEA gains:17.2% base 10000 (apports), valeur 10770 → 770 × 0.172 = 132.44
	// CTO gains:30% base 1100 (achats−ventes), valeur 1120 → 20 × 0.30 = 6
	// Livret none → 0 ; Immo gains:30% base 400000, valeur 450000 → 15000
	approx(t, "tax", v.Tax, 132.44+6+15000)
	approx(t, "net", v.Net, v.Gross-v.Tax)
	if v.TaxNote == "" {
		t.Error("TaxNote attendue (ventilation approximative ≠ enveloppe)")
	}
	// lignes par groupe de tête + liquidités
	labels := map[string]bool{}
	for _, l := range v.Lines {
		labels[l.Label] = true
	}
	for _, want := range []string{"actions", "immo", "liquidités"} {
		if !labels[want] {
			t.Errorf("ligne %q absente (%v)", want, v.Lines)
		}
	}
}

func TestValueGroupScope(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, "ACTIONS"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross", v.Gross, 6720+1120)
	// impôt position par position :
	// PEA : coût moyen = 7750 − 7750×3/15 = 6200 ; gain 520 × 0.172 = 89.44
	// CTO : coût moyen 1100 ; gain 20 × 0.30 = 6
	approx(t, "tax", v.Tax, 89.44+6)
}

func TestValueAccountAndAssetScopes(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross pea", v.Gross, 6720+4050)
	approx(t, "tax pea", v.Tax, 132.44) // enveloppe exacte

	v, err = Value(b, scopeOf(t, b, "cw8"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross cw8", v.Gross, 6720+1120)
	if len(v.Lines) != 2 { // une ligne par enveloppe
		t.Errorf("lines = %+v", v.Lines)
	}
}

func TestValueAtEarlierDate(t *testing.T) {
	b := valuationBook(t)
	// au 21 mars : clôture forward-fill 540 du 20 mars, maison au 1er relevé
	v, err := Value(b, scopeOf(t, b, ""), mustDate("2026-03-21"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA 12×540=6480, cash 4050 ; CTO 2×540=1080 ; livret 12000 ; maison 400000
	approx(t, "gross", v.Gross, 6480+4050+1080+12000+400000)
}

func TestValueOtherCurrency(t *testing.T) {
	b := valuationBook(t)
	fx := fxStub{rates: map[string]float64{"EUR→USD": 1.10}}
	v, err := Value(b, scopeOf(t, b, "PEA"), mustDate("2026-06-05"), domain.USD, fx)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross usd", v.Gross, (6720+4050)*1.10)
}

func TestValueStaleMarkers(t *testing.T) {
	b := valuationBook(t)
	// au 30 mars, dernière clôture du 20 mars → > 5 jours → stale
	v, err := Value(b, scopeOf(t, b, "cw8"), mustDate("2026-03-30"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Stale) == 0 || !strings.Contains(v.Stale[0], "2026-03-20") {
		t.Errorf("stale = %v", v.Stale)
	}
}

func TestValueAutoDividends(t *testing.T) {
	b := valuationBook(t)
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	at := mustDate("2026-06-05")
	// PEA détient 15 parts au 1er mars (achats 10+5, vente après) → +30 EUR de cash
	v, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross avec dividendes", v.Gross, 6720+4050+30)

	// un Dividend manuel sur cw8 désactive l'automatique
	b.Add(domain.Transaction{Date: mustDate("2026-03-02"), Account: "pea", Asset: "cw8",
		Kind: domain.Dividend, Amount: eur("25")})
	v, err = Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross avec dividende manuel", v.Gross, 6720+4050+25)
}

func TestParseScopeOrderAndErrors(t *testing.T) {
	b := valuationBook(t)
	for ref, kind := range map[string]ScopeKind{
		"": All, "actions": ByGroup, "actions/monde": ByGroup,
		"PEA": ByAccount, "cw8": ByAsset, "CW8.PA": ByAsset,
	} {
		s, err := ParseScope(b, ref)
		if err != nil || s.Kind != kind {
			t.Errorf("ParseScope(%q) = %v kind=%v err=%v", ref, s, s.Kind, err)
		}
	}
	if _, err := ParseScope(b, "inconnu"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ParseScope(inconnu): %v", err)
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/portfolio/` → FAIL - undefined: Scope, Value.

- [ ] **Step 3: implémenter**

`internal/portfolio/scope.go`:

```go
package portfolio

import (
	"errors"
	"fmt"
	"strings"

	"finador/internal/domain"
)

type ScopeKind uint8

const (
	All ScopeKind = iota
	ByGroup
	ByAccount
	ByAsset
)

// Scope is what a command evaluates: everything, a group subtree, one
// envelope, or one asset. Resolution order on a free reference: group
// prefix first, then account, then asset.
type Scope struct {
	Kind    ScopeKind
	Group   string // chemin en minuscules
	Account *domain.Account
	Asset   *domain.Asset
	Label   string
}

func ParseScope(b *domain.Book, ref string) (Scope, error) {
	if ref == "" {
		return Scope{Kind: All, Label: "patrimoine"}, nil
	}
	low := strings.ToLower(ref)
	for _, a := range b.Assets {
		if inGroup(a.Group, low) {
			return Scope{Kind: ByGroup, Group: low, Label: low}, nil
		}
	}
	if acc, err := b.Account(ref); err == nil {
		return Scope{Kind: ByAccount, Account: acc, Label: acc.Name}, nil
	} else if errors.Is(err, domain.ErrAmbiguous) {
		return Scope{}, err
	}
	if asset, err := b.Asset(ref); err == nil {
		return Scope{Kind: ByAsset, Asset: asset, Label: asset.Name}, nil
	} else if errors.Is(err, domain.ErrAmbiguous) {
		return Scope{}, err
	}
	return Scope{}, fmt.Errorf("portée %q (ni groupe, ni compte, ni actif): %w", ref, domain.ErrNotFound)
}

// inGroup reports whether an asset group path falls under scope (lowercase),
// matching whole path segments.
func inGroup(assetGroup, scope string) bool {
	g := strings.ToLower(assetGroup)
	return g == scope || strings.HasPrefix(g, scope+"/")
}

func (s Scope) hasAsset(acc *domain.Account, asset *domain.Asset) bool {
	switch s.Kind {
	case All:
		return true
	case ByGroup:
		return inGroup(asset.Group, s.Group)
	case ByAccount:
		return acc.ID == s.Account.ID
	case ByAsset:
		return asset.ID == s.Asset.ID
	}
	return false
}

func (s Scope) hasCash(acc *domain.Account) bool {
	switch s.Kind {
	case All:
		return true
	case ByAccount:
		return acc.ID == s.Account.ID
	}
	return false
}

// lineLabel groups the breakdown lines: top-level group for All, next path
// segment (or asset name) inside a group, asset name inside an account,
// account name for an asset scope. Cash lines are labelled "liquidités".
func (s Scope) lineLabel(acc *domain.Account, asset *domain.Asset) string {
	if asset == nil {
		return "liquidités"
	}
	switch s.Kind {
	case All:
		if asset.Group == "" {
			return "(sans groupe)"
		}
		head, _, _ := strings.Cut(strings.ToLower(asset.Group), "/")
		return head
	case ByGroup:
		g := strings.ToLower(asset.Group)
		if g == s.Group {
			return asset.Name
		}
		seg, _, _ := strings.Cut(strings.TrimPrefix(g, s.Group+"/"), "/")
		return s.Group + "/" + seg
	case ByAccount:
		return asset.Name
	case ByAsset:
		return acc.Name
	}
	return asset.Name
}
```

`internal/portfolio/value.go`:

```go
package portfolio

import (
	"fmt"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// FX converts an amount between currencies at a date.
type FX interface {
	Convert(amount float64, from, to domain.Currency, at domain.Date) (float64, error)
}

// Valuation is the value of a scope at a date, in one display currency.
// Line taxes are position-by-position (documented approximation); the total
// of All/Account scopes uses the exact envelope rule - TaxNote is set when
// the two visibly diverge.
type Valuation struct {
	Currency        domain.Currency
	Gross, Tax, Net float64
	Lines           []Line
	Stale           []string
	TaxNote         string
}

type Line struct {
	Label           string
	Gross, Tax, Net float64
}

const staleAfterDays = 5

func Value(b *domain.Book, scope Scope, at domain.Date, ccy domain.Currency, fx FX) (Valuation, error) {
	v := &valuer{b: b, fx: fx, at: at, ccy: ccy}
	out := Valuation{Currency: ccy}

	lines := map[string]*Line{}
	var order []string
	add := func(label string, gross, tax float64) {
		l, ok := lines[label]
		if !ok {
			l = &Line{Label: label}
			lines[label] = l
			order = append(order, label)
		}
		l.Gross += gross
		l.Tax += tax
	}
	perAccount := map[domain.AccountID]float64{}

	// 1. positions titres
	for _, h := range Holdings(b, at) {
		if !scope.hasAsset(h.Account, h.Asset) {
			continue
		}
		gross, err := v.positionValue(h)
		if err != nil {
			return out, err
		}
		tax, err := v.positionTax(h.Account, h.Asset, gross)
		if err != nil {
			return out, err
		}
		add(scope.lineLabel(h.Account, h.Asset), gross, tax)
		perAccount[h.Account.ID] += gross
	}

	// 2. biens, valorisés par relevés
	for _, p := range statementPairs(b, at) {
		if p.asset.Kind != domain.Property || !scope.hasAsset(p.account, p.asset) {
			continue
		}
		gross, err := v.statementValue(p.account.ID, p.asset)
		if err != nil {
			return out, err
		}
		tax, err := v.propertyTax(p.account, p.asset, gross)
		if err != nil {
			return out, err
		}
		add(scope.lineLabel(p.account, p.asset), gross, tax)
		perAccount[p.account.ID] += gross
	}

	// 3. liquidités des comptes suivis
	for _, acc := range b.Accounts {
		if !scope.hasCash(acc) || !CashTracked(b, acc.ID) {
			continue
		}
		gross, err := v.cashValue(acc)
		if err != nil {
			return out, err
		}
		if gross == 0 {
			continue
		}
		tax := 0.0
		if acc.Tax.Mode == domain.TaxOnValue {
			tax = gross * rate(acc.Tax)
		}
		add(scope.lineLabel(acc, nil), gross, tax)
		perAccount[acc.ID] += gross
	}

	for _, label := range order {
		l := lines[label]
		l.Net = l.Gross - l.Tax
		out.Lines = append(out.Lines, *l)
		out.Gross += l.Gross
		out.Tax += l.Tax
	}
	// All/Account : l'impôt total exact est celui de la règle d'enveloppe
	if scope.Kind == All || scope.Kind == ByAccount {
		exact := 0.0
		for accID, gross := range perAccount {
			acc, err := b.Account(string(accID))
			if err != nil {
				continue
			}
			t, err := v.accountTax(acc, gross)
			if err != nil {
				return out, err
			}
			exact += t
		}
		if d := exact - out.Tax; d > 0.01 || d < -0.01 {
			out.TaxNote = "impôt total calculé par enveloppe ; la ventilation par ligne est approximative"
		}
		out.Tax = exact
	}
	out.Net = out.Gross - out.Tax
	out.Stale = v.stale
	return out, nil
}

type valuer struct {
	b     *domain.Book
	fx    FX
	at    domain.Date
	ccy   domain.Currency
	stale []string
}

func rate(t domain.TaxRule) float64 { f, _ := t.Rate.Float64(); return f }
func toF(d decimal.Decimal) float64 { f, _ := d.Float64(); return f }

func (v *valuer) convertAt(m domain.Money, to domain.Currency, at domain.Date) (float64, error) {
	return v.fx.Convert(toF(m.Amount), m.Currency, to, at)
}

// positionValue: market close if a series exists, else last statement of the
// (account, asset) pair, else zero - each fallback flagged.
func (v *valuer) positionValue(h Holding) (float64, error) {
	if close, cdate, ok := v.b.Market.Prices[h.Asset.ID].At(v.at); ok {
		if cdate.AddDays(staleAfterDays).Before(v.at) {
			v.stale = append(v.stale, fmt.Sprintf("%s: dernier cours au %s", h.Asset.Name, cdate))
		}
		return v.fx.Convert(toF(h.Qty)*close, h.Asset.Currency, v.ccy, v.at)
	}
	if tx, ok := v.lastStatement(h.Account.ID, h.Asset.ID); ok {
		v.stale = append(v.stale, fmt.Sprintf("%s: valorisé par relevé du %s", h.Asset.Name, tx.Date))
		return v.convertAt(tx.Amount, v.ccy, v.at)
	}
	v.stale = append(v.stale, fmt.Sprintf("%s: aucun cours ni relevé - compté pour 0", h.Asset.Name))
	return 0, nil
}

func (v *valuer) statementValue(acc domain.AccountID, asset *domain.Asset) (float64, error) {
	tx, ok := v.lastStatement(acc, asset.ID)
	if !ok {
		return 0, nil
	}
	return v.convertAt(tx.Amount, v.ccy, v.at)
}

func (v *valuer) lastStatement(acc domain.AccountID, asset domain.AssetID) (*domain.Transaction, bool) {
	var last *domain.Transaction
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Kind != domain.Statement || t.Account != acc || t.Asset != asset {
			continue
		}
		last = t
	}
	return last, last != nil
}

func (v *valuer) firstStatement(acc domain.AccountID, asset domain.AssetID) (*domain.Transaction, bool) {
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) {
			break
		}
		if t.Kind == domain.Statement && t.Account == acc && t.Asset == asset {
			return t, true
		}
	}
	return nil, false
}

// positionTax: per-position rule - TaxOnValue: value × rate; TaxOnGains:
// max(0, value − average-cost basis, flows converted at their date) × rate.
func (v *valuer) positionTax(acc *domain.Account, asset *domain.Asset, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		basis, err := v.positionBasis(acc.ID, asset.ID)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

// positionBasis replays the pair's trades as average cost, in display currency.
func (v *valuer) positionBasis(acc domain.AccountID, asset domain.AssetID) (float64, error) {
	var qty, basis float64
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc || t.Asset != asset {
			continue
		}
		switch t.Kind {
		case domain.Buy:
			amt, err := v.convertAt(t.Amount, v.ccy, t.Date)
			if err != nil {
				return 0, err
			}
			basis += amt
			qty += toF(t.Quantity)
		case domain.Sell:
			if qty <= 0 {
				continue
			}
			sold := min(toF(t.Quantity), qty)
			basis -= basis * sold / qty
			qty -= sold
		}
	}
	return basis, nil
}

// propertyTax: gains are measured from the FIRST known estimate.
func (v *valuer) propertyTax(acc *domain.Account, asset *domain.Asset, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		first, ok := v.firstStatement(acc.ID, asset.ID)
		if !ok {
			return 0, nil
		}
		basis, err := v.convertAt(first.Amount, v.ccy, first.Date)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

// accountTax: the exact envelope rule. TaxOnGains basis: net external
// contributions when cash is tracked, buys − sells otherwise (spec §3).
func (v *valuer) accountTax(acc *domain.Account, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		basis, err := v.accountBasis(acc)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

func (v *valuer) accountBasis(acc *domain.Account) (float64, error) {
	tracked := CashTracked(v.b, acc.ID)
	basis := 0.0
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID {
			continue
		}
		sign := 0.0
		switch {
		case tracked && t.Kind == domain.Deposit:
			sign = 1
		case tracked && t.Kind == domain.Withdraw:
			sign = -1
		case !tracked && t.Kind == domain.Buy:
			sign = 1
		case !tracked && t.Kind == domain.Sell:
			sign = -1
		default:
			continue
		}
		amt, err := v.convertAt(t.Amount, v.ccy, t.Date)
		if err != nil {
			return 0, err
		}
		basis += sign * amt
	}
	// Les biens valorisés par relevés entrent dans la base par leur première
	// estimation connue - sinon une enveloppe immo serait taxée sur la valeur
	// totale et non la plus-value. Approximation documentée : si un apport
	// suivi a financé le bien ET que le bien a un relevé initial, la base
	// compte les deux (cas inhabituel, à corriger à la main si rencontré).
	for _, p := range statementPairs(v.b, v.at) {
		if p.account.ID != acc.ID || p.asset.Kind != domain.Property {
			continue
		}
		first, ok := v.firstStatement(acc.ID, p.asset.ID)
		if !ok {
			continue
		}
		amt, err := v.convertAt(first.Amount, v.ccy, first.Date)
		if err != nil {
			return 0, err
		}
		basis += amt
	}
	return basis, nil
}

// cashValue: anchor on the last cash statement ≤ at, then post-anchor flows
// converted to the account currency at their date, plus auto-dividends; the
// final balance converts to the display currency at `at`.
func (v *valuer) cashValue(acc *domain.Account) (float64, error) {
	var balance float64
	var anchor domain.Date
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID || t.Asset != "" || t.Kind != domain.Statement {
			continue
		}
		amt, err := v.convertAt(t.Amount, acc.Currency, t.Date)
		if err != nil {
			return 0, err
		}
		balance, anchor = amt, t.Date
	}
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID {
			continue
		}
		if !anchor.IsZero() && !anchor.Before(t.Date) {
			continue // déjà inclus dans le relevé d'ancrage
		}
		sign := 0.0
		switch t.Kind {
		case domain.Deposit, domain.Sell, domain.Dividend:
			sign = 1
		case domain.Withdraw, domain.Buy, domain.Fee:
			sign = -1
		default:
			continue
		}
		amt, err := v.convertAt(t.Amount, acc.Currency, t.Date)
		if err != nil {
			return 0, err
		}
		balance += sign * amt
	}
	div, err := v.autoDividends(acc, anchor)
	if err != nil {
		return 0, err
	}
	balance += div
	return v.fx.Convert(balance, acc.Currency, v.ccy, v.at)
}

// autoDividends credits Yahoo-known distributions for assets without any
// manual Dividend transaction: quantity held at ex-date × gross amount.
func (v *valuer) autoDividends(acc *domain.Account, after domain.Date) (float64, error) {
	manual := manualDividendAssets(v.b)
	total := 0.0
	for id, events := range v.b.Market.Dividends {
		if manual[id] {
			continue
		}
		asset, err := v.b.Asset(string(id))
		if err != nil {
			continue
		}
		for _, ev := range events {
			if v.at.Before(ev.ExDate) {
				continue
			}
			if !after.IsZero() && !after.Before(ev.ExDate) {
				continue
			}
			qty := Quantity(v.b, acc.ID, id, ev.ExDate)
			if qty.IsZero() {
				continue
			}
			amt, err := v.fx.Convert(toF(qty)*ev.Amount, asset.Currency, acc.Currency, ev.ExDate)
			if err != nil {
				return 0, err
			}
			total += amt
		}
	}
	return total, nil
}

func manualDividendAssets(b *domain.Book) map[domain.AssetID]bool {
	out := map[domain.AssetID]bool{}
	for _, t := range b.Transactions {
		if t.Kind == domain.Dividend && t.Asset != "" {
			out[t.Asset] = true
		}
	}
	return out
}

type pair struct {
	account *domain.Account
	asset   *domain.Asset
}

// statementPairs lists the distinct (account, asset) couples having at least
// one statement dated ≤ at, in first-seen order.
func statementPairs(b *domain.Book, at domain.Date) []pair {
	type key struct {
		acc   domain.AccountID
		asset domain.AssetID
	}
	seen := map[key]bool{}
	var out []pair
	for _, t := range Sorted(b) {
		if at.Before(t.Date) || t.Kind != domain.Statement || t.Asset == "" {
			continue
		}
		k := key{t.Account, t.Asset}
		if seen[k] {
			continue
		}
		seen[k] = true
		acc, errA := b.Account(string(t.Account))
		asset, errB := b.Asset(string(t.Asset))
		if errA != nil || errB != nil {
			continue
		}
		out = append(out, pair{acc, asset})
	}
	return out
}
```

NOTE pour l'implémenteur : `v.b.Market.Prices[id]` peut être nil - `At` est nil-safe par construction (Task B1). `max`/`min` sont les builtins Go 1.21+.

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/portfolio/ -v && go test ./...` → PASS. gofmt/vet silencieux. Si un nombre attendu d'un test diverge de plus d'un centime, NE PAS ajuster le test : investiguer le calcul (les valeurs ont été vérifiées à la main).

- [ ] **Step 5: commit**

```bash
git add internal/portfolio
git commit -m "feat(portfolio): valorisation brut/impôt latent/net à toute date et toute portée"
```

---

### Task B7: cli - value, refresh, --offline, résolution Yahoo d'asset add

**Files:**
- Create: `internal/cli/value.go`, `internal/cli/refresh.go`
- Modify: `internal/cli/cli.go` (options, flag --offline, ensureFresh, AddCommand), `internal/cli/asset.go` (enrichissement Yahoo)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

Ajouter à `internal/cli/cli_test.go` (le harnais existant garde `--offline` partout ; un second harnais branche une Source factice) :

```go
// fakeSource sert des données de marché déterministes aux tests CLI.
type fakeSource struct{}

func (fakeSource) Resolve(_ context.Context, q string) (market.SymbolInfo, error) {
	if strings.EqualFold(q, "CW8.PA") {
		return market.SymbolInfo{Symbol: "CW8.PA", Name: "Amundi MSCI World UCITS ETF"}, nil
	}
	return market.SymbolInfo{}, domain.ErrNotFound
}

func (fakeSource) Daily(_ context.Context, sym string, _ domain.Date) (market.DailyData, error) {
	day := func(s string) domain.Date {
		d, err := domain.ParseDate(s)
		if err != nil {
			panic(err)
		}
		return d
	}
	switch sym {
	case "CW8.PA":
		return market.DailyData{Currency: domain.EUR, Closes: []domain.PricePoint{
			{Date: day("2026-06-01"), Close: 550},
			{Date: day("2026-06-05"), Close: 560},
		}}, nil
	case "EURUSD=X":
		return market.DailyData{Currency: domain.USD, Closes: []domain.PricePoint{
			{Date: day("2026-06-01"), Close: 1.10},
			{Date: day("2026-06-05"), Close: 1.10},
		}}, nil
	}
	return market.DailyData{}, domain.ErrNotFound
}

// tryRunNet exécute finador SANS --offline, avec la Source factice.
func tryRunNet(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New(cli.WithSource(fakeSource{}))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func runNet(t *testing.T, db string, args ...string) string {
	t.Helper()
	out, err := tryRunNet(t, db, args...)
	if err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestValueEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "value", "--net", "--at", "2026-06-05")
	// 10 × 560 = 5600 ; cash suivi = 5000 − 5500 = −500 → brut 5100
	// base d'enveloppe 5000 → gain 100 → impôt 17.20 → net 5082.80
	for _, want := range []string{"5100.00 EUR", "17.20 EUR", "5082.80 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("value --net: %q manquant dans:\n%s", want, out)
		}
	}

	out = runNet(t, db, "value", "--ccy", "USD", "--at", "2026-06-05")
	if !strings.Contains(out, "5610.00 USD") { // 5100 × 1.10
		t.Errorf("value USD:\n%s", out)
	}

	// le cache permet ensuite le hors-ligne
	out = run(t, db, "value", "--at", "2026-06-05")
	if !strings.Contains(out, "5100.00 EUR") {
		t.Errorf("value --offline après cache:\n%s", out)
	}
}

func TestRefreshCommand(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	out := runNet(t, db, "refresh")
	if !strings.Contains(out, "rafraîchie") {
		t.Errorf("refresh: %q", out)
	}
	if _, err := tryRun(t, db, "refresh"); err == nil {
		t.Fatal("refresh en --offline aurait dû échouer")
	}
}

func TestAssetAddResolvesFromYahoo(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")
	out := runNet(t, db, "asset", "add", "cw8.pa", "--group", "actions/monde")
	if !strings.Contains(out, "Amundi MSCI World UCITS ETF") {
		t.Errorf("résolution Yahoo absente: %q", out)
	}
	list := run(t, db, "asset", "list")
	if !strings.Contains(list, "CW8.PA") { // ticker canonique résolu
		t.Errorf("asset list:\n%s", list)
	}
}
```

(ajouter aux imports du fichier de test : `"context"`, `"finador/internal/domain"`, `"finador/internal/market"`)

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/cli/` → FAIL - undefined: cli.WithSource, unknown command "value".

- [ ] **Step 3: implémenter**

Dans `internal/cli/cli.go` :

```go
// en tête de fichier, imports += "fmt", "finador/internal/market"

// Option configures the CLI - tests inject a fake market source.
type Option func(*app)

// WithSource replaces the default Yahoo market source.
func WithSource(s market.Source) Option { return func(a *app) { a.source = s } }
```

Le struct app gagne deux champs :

```go
type app struct {
	dbPath     string
	noKeychain bool
	offline    bool
	source     market.Source
}
```

New devient variadique et gagne le flag :

```go
func New(opts ...Option) *cobra.Command {
	a := &app{}
	for _, opt := range opts {
		opt(a)
	}
	...
	root.PersistentFlags().BoolVar(&a.offline, "offline", false, "n'accède jamais au réseau (cache uniquement)")
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a), txCmd(a), importCmd(a),
		configCmd(a), lockCmd(a), valueCmd(a), refreshCmd(a))
	return root
}

func (a *app) marketSource() market.Source {
	if a.source == nil {
		a.source = market.NewYahoo()
	}
	return a.source
}

// ensureFresh refreshes the market cache when needed. It never fails hard:
// offline or network trouble degrade to warnings, stale data stays usable.
func (a *app) ensureFresh(cmd *cobra.Command, f *store.File) {
	if a.offline {
		return
	}
	sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), false)
	for _, w := range sum.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "avertissement:", w)
	}
	if len(sum.Fetched) > 0 {
		if err := f.Save(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "avertissement: cache non sauvegardé:", err)
		}
	}
}
```

`internal/cli/value.go`:

```go
package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

func valueCmd(a *app) *cobra.Command {
	var ccy, at string
	var net bool
	cmd := &cobra.Command{
		Use:   "value [portée]",
		Short: "Valeur du patrimoine - tout, un groupe, une enveloppe ou un actif",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := portfolio.ParseScope(b, ref)
			if err != nil {
				return err
			}
			date, err := dateOrToday(at)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			val, err := portfolio.Value(b, scope, date, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			printValuation(cmd, scope, date, val, net)
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise d'affichage (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&at, "at", "", "date d'évaluation AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().BoolVar(&net, "net", false, "affiche brut, impôt latent estimé et net")
	return cmd
}

// displayCurrency: config "currency" si valide, sinon EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}

func money(x float64, c domain.Currency) string {
	return strconv.FormatFloat(x, 'f', 2, 64) + " " + string(c)
}

func printValuation(cmd *cobra.Command, scope portfolio.Scope, date domain.Date, v portfolio.Valuation, net bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s au %s\n", scope.Label, date)
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	if net {
		fmt.Fprintln(w, "LIGNE\tBRUT\tIMPÔT\tNET")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Label,
				money(l.Gross, v.Currency), money(l.Tax, v.Currency), money(l.Net, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\t%s\t%s\n",
			money(v.Gross, v.Currency), money(v.Tax, v.Currency), money(v.Net, v.Currency))
	} else {
		fmt.Fprintln(w, "LIGNE\tVALEUR")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\n", l.Label, money(l.Gross, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\n", money(v.Gross, v.Currency))
	}
	w.Flush()
	errw := cmd.ErrOrStderr()
	for _, s := range v.Stale {
		fmt.Fprintln(errw, "≈", s)
	}
	if net && v.TaxNote != "" {
		fmt.Fprintln(errw, "ℹ", v.TaxNote)
	}
}
```

`internal/cli/refresh.go`:

```go
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/market"
)

func refreshCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Rafraîchit cours, change et dividendes depuis Yahoo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.offline {
				return errors.New("refresh impossible en --offline")
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), true)
			for _, w := range sum.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "avertissement:", w)
			}
			if err := f.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d série(s) rafraîchie(s)\n", len(sum.Fetched))
			return nil
		},
	}
}
```

Dans `internal/cli/asset.go`, le RunE d'assetAdd devient (l'enrichissement précède le calcul de l'ID ; tout échec réseau est un avertissement, la saisie manuelle fait foi) :

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := domain.ParseAssetKind(kind)
			if err != nil {
				return err
			}
			ccyParsed, err := domain.ParseCurrency(ccy)
			if err != nil {
				return err
			}
			asset := &domain.Asset{
				Kind:     k,
				Name:     cmp.Or(name, args[0]),
				ISIN:     isin,
				Aliases:  aliases,
				Currency: ccyParsed,
				Group:    group,
			}
			if k == domain.Security {
				asset.Ticker = args[0]
				if !a.offline {
					enrichFromMarket(cmd, a, asset, args[0],
						cmd.Flags().Changed("name"), cmd.Flags().Changed("ccy"))
				}
			}
			asset.ID = domain.AssetID(cmp.Or(id, domain.Slugify(asset.Name)))
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAsset(asset); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Actif %s (%s) créé\n", asset.Name, asset.ID)
				return nil
			})
		},
```

et, dans le même fichier :

```go
// enrichFromMarket completes ticker/name/currency from Yahoo; explicit flags
// always win, and any network failure downgrades to a warning.
func enrichFromMarket(cmd *cobra.Command, a *app, asset *domain.Asset, query string, nameSet, ccySet bool) {
	src := a.marketSource()
	info, err := src.Resolve(cmd.Context(), query)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "avertissement: résolution %q: %v\n", query, err)
		return
	}
	asset.Ticker = info.Symbol
	if !nameSet && info.Name != "" {
		asset.Name = info.Name
	}
	if data, err := src.Daily(cmd.Context(), asset.Ticker, domain.Today().AddDays(-7)); err == nil {
		if !ccySet && data.Currency != "" {
			asset.Currency = data.Currency
		}
	}
}
```

(ajouter l'import `"cmp"` toujours présent, et rien d'autre - `a.offline` et `a.marketSource()` viennent de cli.go)

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/cli/ && go test ./...` → PASS. gofmt/vet silencieux. ATTENTION : vérifier que les tests N'APPELLENT JAMAIS le vrai Yahoo - `tryRun` garde `--offline`, seul `tryRunNet` (Source factice) s'en passe. `git grep -n "query1.finance" internal/cli` ne doit apparaître nulle part dans les tests.

- [ ] **Step 5: commit**

```bash
git add internal/cli
git commit -m "feat(cli): value brut/net multi-devises, refresh, --offline, résolution Yahoo"
```

---

### Task B8: finition de la phase B

**Files:**
- Modify: `docs/superpowers/DECISIONS.md` (décision D7)

- [ ] **Step 1: portillons complets**

Run: `gofmt -l . && go vet ./... && go test ./... -count=1`
Expected: silencieux + tous PASS.

- [ ] **Step 2: smoke test binaire (hors ligne, déterministe)**

```bash
go build -trimpath -o bin/finador ./cmd/finador
export FINADOR_PASSWORD=demo
B="./bin/finador --db /tmp/demo-b.fin --no-keychain --offline"
$B init
$B account add "PEA BforBank" --tax gains:17.2%
$B account add "Immo" --tax gains:30%
$B asset add "Maison à Rénover" --kind property --group immo
$B asset set maison-a-renover 450000 --at 2026-06-01 --account Immo
$B deposit "PEA BforBank" 5000 2026-01-10
$B cash set "PEA BforBank" 4800 --at 2026-06-01
$B value --net
rm -f /tmp/demo-b.fin*
```
Expected: la valeur affiche la maison (450000) + le cash (4800), impôt latent de l'immo selon la règle, AUCUN accès réseau (--offline), marqueurs ≈ pour le bien (valorisé par relevé). Le binaire n'est pas committé.

- [ ] **Step 3: décision D7 dans DECISIONS.md**

Ajouter à `docs/superpowers/DECISIONS.md` :

```markdown
## D7 - Le fichier .fin reste en version 1 avec le cache marché

**Contexte :** la phase B ajoute prices/fx/dividends dans le JSON du Book. Un binaire
phase A qui réécrirait ce fichier perdrait silencieusement ces champs. **Choix :** pas de
bump de version - ces champs sont des caches refetchables (un `finador refresh` les
reconstruit), et toutes les phases sont livrées ensemble. **Alternative si refusé :**
bump l'octet de version à 2 et refuser les versions inconnues.
```

- [ ] **Step 4: commit + tag**

```bash
git add docs/superpowers/DECISIONS.md
git commit -m "docs: décision D7 - cache marché sans bump de version de fichier"
git tag phase-b
```


