package market

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
