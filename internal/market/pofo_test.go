package market

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"finador/internal/domain"

	"github.com/bpineau/pofo/pkg/marketdata"
)

// stubPofo points every pofo data source at the test server: no test can
// reach the real APIs (mirrors pofo's own stubAllBases pattern).
func stubPofo(p *Pofo, base string) {
	c := p.Client
	c.ChartBase, c.SearchBase, c.StooqBase = base, base, base
	c.FTBase, c.BoursoramaBase, c.MorningstarBase = base, base, base
	c.JustETFBase, c.EurostatBase, c.FredBase = base, base, base
	c.CookieBase = base // the quote batch needs Yahoo's cookie+crumb pair
}

// pofoChartJSON is a minimal Yahoo chart payload with raw and adjusted
// closes plus one dividend, as pofo's client parses it.
func pofoChartJSON(symbol string, day time.Time) string {
	ts := day.Add(14 * time.Hour).Unix()
	ts2 := day.Add(38 * time.Hour).Unix()
	div := fmt.Sprintf(`"%d":{"amount":0.5,"date":%d}`, ts2, ts2)
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":"EUR","symbol":%q,"longName":"Test %s"},"timestamp":[%d,%d],"events":{"dividends":{%s}},"indicators":{"quote":[{"close":[100,101]}],"adjclose":[{"adjclose":[95,96]}]}}],"error":null}}`,
		symbol, symbol, ts, ts2, div)
}

func TestPofoDailyRawClosesAndDividends(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/CW8.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, pofoChartJSON("CW8.PA", day))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	got, err := p.Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2026-05-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "EUR" {
		t.Errorf("currency: %q", got.Currency)
	}
	if len(got.Closes) != 2 || got.Closes[0].Close != 100 {
		t.Fatalf("finador needs RAW closes (dividends as cash), got %+v", got.Closes)
	}
	if len(got.Dividends) != 1 || got.Dividends[0].Amount != 0.5 {
		t.Fatalf("dividends: %+v", got.Dividends)
	}
	if got.Closes[0].Date != mustDate("2026-06-01") {
		t.Errorf("date mapping: %+v", got.Closes[0].Date)
	}
}

func TestPofoDailyPrefersISIN(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	searched := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		searched = r.URL.Query().Get("q")
		fmt.Fprint(w, `{"quotes":[{"symbol":"CW8.PA","longname":"Amundi MSCI World","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/CW8.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, pofoChartJSON("CW8.PA", day))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	// An uncatalogued ISIN goes through the search; the symbol is a fallback.
	_, err := p.Daily(context.Background(), Ref{Symbol: "CW8.PA", ISIN: "LU9990000015"}, mustDate("2026-05-01"))
	if err != nil {
		t.Fatal(err)
	}
	if searched != "LU9990000015" {
		t.Errorf("the ISIN should resolve first, searched %q", searched)
	}
}

func TestPofoResolve(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"CW8.PA","longname":"Amundi MSCI World","quoteType":"ETF"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	info, err := p.Resolve(context.Background(), "amundi msci world")
	if err != nil {
		t.Fatal(err)
	}
	if info.Symbol != "CW8.PA" || info.Name != "Amundi MSCI World" {
		t.Errorf("resolved: %+v", info)
	}
}

func TestPofoIntradayNotCovered(t *testing.T) {
	p := NewPofo()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"chart":{"result":[],"error":null}}`)
	}))
	defer srv.Close()
	stubPofo(p, srv.URL)

	if _, err := p.Intraday(context.Background(), Ref{}); !errors.Is(err, ErrNotCovered) {
		t.Errorf("no symbol should be ErrNotCovered, got %v", err)
	}
	if _, err := p.Intraday(context.Background(), Ref{Symbol: "NOPE"}); !errors.Is(err, ErrNotCovered) {
		t.Errorf("an unquotable symbol should be ErrNotCovered, got %v", err)
	}
}

func TestPofoLatestLiveSpot(t *testing.T) {
	at := time.Date(2026, 7, 2, 18, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/CW8.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"chart":{"result":[{"meta":{"currency":"EUR","exchangeTimezoneName":"Europe/Paris","regularMarketPrice":561.5,"regularMarketTime":%d}}],"error":null}}`, at.Unix())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	q, err := p.Latest(context.Background(), Ref{Symbol: "CW8.PA"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Live || q.Price != 561.5 || q.Currency != domain.EUR {
		t.Errorf("quote: %+v", q)
	}
	if !q.Time.Equal(at) {
		t.Errorf("time: %v, want %v", q.Time, at)
	}
}

func TestPofoLatestFallsBackToClose(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	// The daily fixture carries no regularMarketPrice: the spot step is not
	// covered and Latest degrades to the last daily close, Live false.
	mux.HandleFunc("/v8/finance/chart/CW8.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, pofoChartJSON("CW8.PA", day))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	q, err := p.Latest(context.Background(), Ref{Symbol: "CW8.PA"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Live || q.Currency != domain.EUR {
		t.Errorf("quote: %+v", q)
	}
	// pofo serves ADJUSTED closes on this path; at the last bar the adjusted
	// close equals the raw close, so the fixture's final adjclose is expected.
	if q.Price != 96 {
		t.Errorf("price = %v, want the last close 96", q.Price)
	}
}

func TestPofoLatestEmptyRef(t *testing.T) {
	p := NewPofo()
	if _, err := p.Latest(context.Background(), Ref{}); !errors.Is(err, ErrNotCovered) {
		t.Errorf("empty ref should be ErrNotCovered, got %v", err)
	}
}

func TestPofoDailyEmptyRef(t *testing.T) {
	p := NewPofo()
	if _, err := p.Daily(context.Background(), Ref{}, mustDate("2026-05-01")); !errors.Is(err, ErrNotCovered) {
		t.Errorf("empty ref: %v", err)
	}
}

// chartCcy is pofoChartJSON with an explicit currency and closes.
func chartCcy(symbol, ccy string, day time.Time, c1, c2 float64) string {
	ts := day.Add(14 * time.Hour).Unix()
	ts2 := day.Add(38 * time.Hour).Unix()
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":%q,"symbol":%q,"longName":"Test %s"},"timestamp":[%d,%d],"indicators":{"quote":[{"close":[%g,%g]}],"adjclose":[{"adjclose":[%g,%g]}]}}],"error":null}}`,
		ccy, symbol, symbol, ts, ts2, c1, c2, c1, c2)
}

// twinPofoMux: the ISIN search only finds the deep USD twin; the native
// EUR line stays quotable by its own ticker; FX crosses cover the spot
// conversion under both spellings.
func twinPofoMux(day time.Time) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[{"symbol":"TWIN.US","longname":"Twin Fund","quoteType":"ETF"}]}`)
	})
	mux.HandleFunc("/v8/finance/chart/TWIN.US", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartCcy("TWIN.US", "USD", day, 50, 51))
	})
	mux.HandleFunc("/v8/finance/chart/NATV.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartCcy("NATV.PA", "EUR", day, 40, 41))
	})
	mux.HandleFunc("/v8/finance/chart/USDEUR=X", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartCcy("USDEUR=X", "EUR", day, 0.5, 0.5))
	})
	mux.HandleFunc("/v8/finance/chart/EURUSD=X", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartCcy("EURUSD=X", "USD", day, 2, 2))
	})
	return mux
}

// Daily is strict about the declared currency (NoConvert): without a
// native line among the ids it fails rather than serving converted twin
// closes; with the declared ticker among the ids, the native line wins.
func TestPofoDailyStrictCurrency(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(twinPofoMux(day))
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	_, err := p.Daily(context.Background(),
		Ref{ISIN: "FR0000000008", Currency: domain.EUR}, mustDate("2026-05-01"))
	if !errors.Is(err, marketdata.ErrWrongCurrency) {
		t.Fatalf("want ErrWrongCurrency for the USD-only twin, got %v", err)
	}

	got, err := p.Daily(context.Background(),
		Ref{ISIN: "FR0000000008", Symbol: "NATV.PA", Currency: domain.EUR}, mustDate("2026-05-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != domain.EUR || len(got.Closes) != 2 || got.Closes[0].Close != 40 {
		t.Fatalf("native line expected, got %+v", got)
	}
}

// Latest tolerates a last-resort conversion: a spot point is overwritten
// by the next real close, so it never leaves a seam in the history.
func TestPofoLatestConvertsSpotAsLastResort(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(twinPofoMux(day))
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	q, err := p.Latest(context.Background(), Ref{ISIN: "FR0000000008", Currency: domain.EUR})
	if err != nil {
		t.Fatal(err)
	}
	// Last USD close is 51; at 0.5 EUR per USD: 25.5.
	if q.Currency != domain.EUR || q.Price != 25.5 {
		t.Fatalf("converted spot expected, got %+v", q)
	}
}

// TestQuoteOfSessions: the mapping from a pofo quote, on the three shapes
// that matter. A nowcast is an estimate and claims NO session - the whole
// point of the captioning: nobody has struck that number, in any session.
func TestQuoteOfSessions(t *testing.T) {
	cases := []struct {
		name      string
		in        marketdata.Quote
		estimated bool
		session   string
		extended  bool
	}{
		{"regular", marketdata.Quote{Source: "yahoo", Live: true, Session: "regular"}, false, "regular", false},
		{"pre", marketdata.Quote{Source: "yahoo", Live: true, Session: "pre"}, false, "pre", true},
		{"post", marketdata.Quote{Source: "yahoo", Live: true, Session: "post"}, false, "post", true},
		{"nowcast", marketdata.Quote{Source: "nowcast", Live: true}, true, "", false},
		{"fund NAV", marketdata.Quote{Source: "ft"}, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := quoteOf(&tc.in)
			if q.Estimated != tc.estimated || q.Session != tc.session || q.Extended() != tc.extended {
				t.Errorf("estimated=%v session=%q extended=%v, want %v/%q/%v",
					q.Estimated, q.Session, q.Extended(), tc.estimated, tc.session, tc.extended)
			}
		})
	}
}

// The standard source is cache-less on purpose: plaintext quote files on disk
// would reveal the holdings the encrypted book protects.
func TestDefaultSourceIsCacheless(t *testing.T) {
	p := Default()
	if p == nil || p.Client == nil {
		t.Fatal("Default() has no client")
	}
	if p.Client.CacheDir != "" {
		t.Errorf("cache dir = %q, want none: no plaintext quotes on disk", p.Client.CacheDir)
	}
	var _ Source = p
	var _ BatchSource = p
	var _ ExtendedSource = p
}

// yahooAuthMux registers the cookie+crumb bootstrap the v7 quote API needs.
func yahooAuthMux(mux *http.ServeMux) {
	mux.HandleFunc("/v1/test/getcrumb", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "crumb1")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "A1=cookie1; Path=/")
		w.WriteHeader(http.StatusOK)
	})
}

// quoteBatchJSON is a v7 quote payload: one regular print, plus an optional
// after-hours one an hour later.
func quoteBatchJSON(symbol string, at time.Time, post float64) string {
	extra := ""
	if post > 0 {
		extra = fmt.Sprintf(`,"postMarketPrice":%g,"postMarketTime":%d`, post, at.Add(time.Hour).Unix())
	}
	return fmt.Sprintf(`{"quoteResponse":{"result":[{"symbol":%q,"currency":"EUR","exchangeTimezoneName":"Europe/Paris","marketState":"CLOSED","regularMarketPrice":550,"regularMarketTime":%d%s}]}}`,
		symbol, at.Unix(), extra)
}

// TestPofoLatestBatch pins the batch contract finador's spot pass relies on:
// one quote call keyed on the declared tickers (deduplicated), the per-ref
// fallback for whatever the batch did not answer, and an error kept for every
// ref no source could serve - never a silent drop.
func TestPofoLatestBatch(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	at := day.Add(17 * time.Hour)
	var asked []string
	mux := http.NewServeMux()
	yahooAuthMux(mux)
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("symbols"))
		fmt.Fprint(w, quoteBatchJSON("CW8.PA", at, 0))
	})
	mux.HandleFunc("/v8/finance/chart/NAV.FUND", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chartCcy("NAV.FUND", "EUR", day, 40, 41))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	quoted := Ref{Symbol: "CW8.PA", Currency: domain.EUR}
	twin := Ref{Symbol: "CW8.PA", ISIN: "FR0010315770", Currency: domain.EUR}
	fund := Ref{Symbol: "NAV.FUND", Currency: domain.EUR}
	missing := Ref{Symbol: "NOSUCH.XX", Currency: domain.EUR}

	got := p.LatestBatch(context.Background(), []Ref{quoted, twin, fund, missing})

	if len(asked) != 1 || asked[0] != "CW8.PA,NAV.FUND,NOSUCH.XX" {
		t.Fatalf("quote calls = %q, want one call over the deduplicated tickers", asked)
	}
	for _, ref := range []Ref{quoted, twin} {
		q, ok := got.Quotes[ref]
		if !ok || q.Price != 550 || !q.Live || q.Currency != domain.EUR || q.Session != "regular" {
			t.Errorf("batched quote of %+v = %+v (ok=%v)", ref, q, ok)
		}
		if !q.Time.Equal(at) {
			t.Errorf("quote time = %v, want %v", q.Time, at)
		}
	}
	// The batch answers exact symbols only: a fund NAV comes from the
	// per-ref fallback, as the last daily close.
	if q, ok := got.Quotes[fund]; !ok || q.Price != 41 || q.Live {
		t.Errorf("fallback quote = %+v (ok=%v), want the last close 41", q, ok)
	}
	if got.Errs[missing] == nil {
		t.Error("a ref no source can serve must be reported, not dropped")
	}
	if _, ok := got.Quotes[missing]; ok {
		t.Error("a failed ref must not appear in Quotes")
	}
}

// TestPofoLatestBatchExtended: with the opt-in, an after-hours print newer
// than the regular session's wins and says which session it came from - the
// label the display depends on, and what keeps it out of the series.
func TestPofoLatestBatchExtended(t *testing.T) {
	at := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC)
	extended := false
	mux := http.NewServeMux()
	yahooAuthMux(mux)
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		post := 0.0
		if extended {
			post = 561
		}
		fmt.Fprint(w, quoteBatchJSON("CW8.PA", at, post))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	ref := Ref{Symbol: "CW8.PA", Currency: domain.EUR}
	extended = true
	q := p.LatestBatchExtended(context.Background(), []Ref{ref}).Quotes[ref]
	if q.Price != 561 || q.Session != SessionPost || !q.Extended() {
		t.Fatalf("extended quote = %+v, want the labelled 561 post print", q)
	}

	// The regular batch never sees an off-hours print, even when one exists.
	q = p.LatestBatch(context.Background(), []Ref{ref}).Quotes[ref]
	if q.Price != 550 || q.Extended() {
		t.Fatalf("regular quote = %+v, want the 550 regular print", q)
	}
}

// TestPofoIntradayPoints: the 5-minute path of the current day, mapped into
// finador's types with the venue's currency.
func TestPofoIntradayPoints(t *testing.T) {
	t1 := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/CW8.PA", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"chart":{"result":[{"meta":{"currency":"EUR","exchangeTimezoneName":"UTC","longName":"Amundi MSCI World"},"timestamp":[%d,%d],"indicators":{"quote":[{"close":[550.1,551.2]}]}}],"error":null}}`,
			t1.Unix(), t1.Add(5*time.Minute).Unix())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	got, err := p.Intraday(context.Background(), Ref{Symbol: "CW8.PA", Currency: domain.EUR})
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != domain.EUR || len(got.Points) != 2 {
		t.Fatalf("intraday = %+v", got)
	}
	if !got.Points[0].Time.Equal(t1) || got.Points[0].Close != 550.1 || got.Points[1].Close != 551.2 {
		t.Errorf("points = %+v", got.Points)
	}
}

// TestPofoIntradayPropagatesFailures: a source failure is not "not covered" -
// the caller must be able to tell a broken fetch from an instrument nobody
// quotes intraday.
func TestPofoIntradayPropagatesFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	_, err := p.Intraday(context.Background(), Ref{Symbol: "CW8.PA"})
	if err == nil || errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want a transport failure rather than ErrNotCovered", err)
	}
}

// TestPofoResolveFailure: a query nothing matches surfaces as an error naming
// it (pofo's search reports "no results" itself), never as an empty
// SymbolInfo the caller would store as a ticker.
func TestPofoResolveFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	info, err := p.Resolve(context.Background(), "no such instrument at all")
	if err == nil {
		t.Fatalf("want an error, got %+v", info)
	}
	if !strings.Contains(err.Error(), "no such instrument at all") {
		t.Errorf("err = %v, want it to name the query", err)
	}
	if info != (SymbolInfo{}) {
		t.Errorf("info = %+v, want the zero value on failure", info)
	}
}

// errNotFound is the mapping the CLI's not-found handling hangs on: a source
// error is passed through untouched, and a search that answered with nothing
// at all becomes domain.ErrNotFound.
func TestErrNotFound(t *testing.T) {
	boom := errors.New("HTTP 500")
	if got := errNotFound("q", boom); !errors.Is(got, boom) {
		t.Errorf("errNotFound with a cause = %v, want %v", got, boom)
	}
	if got := errNotFound("q", nil); !errors.Is(got, domain.ErrNotFound) {
		t.Errorf("errNotFound with no cause = %v, want domain.ErrNotFound", got)
	}
}

// A fund pinned to a NAV source has no quotable symbol: Resolve keeps the
// query, which pofo resolves again through the same pin at fetch time.
// Storing an empty ticker instead would leave the asset unquotable.
func TestPofoResolveKeepsQueryForPinnedFund(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/finance/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"quotes":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewPofo()
	stubPofo(p, srv.URL)

	const isin = "FR0011147594" // catalogued, pinned to a NAV source, no ticker
	info, err := p.Resolve(context.Background(), isin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Symbol != isin {
		t.Errorf("symbol = %q, want the query %q kept", info.Symbol, isin)
	}
	if info.Name == "" {
		t.Error("the pinned resolution should still carry a name")
	}
}
