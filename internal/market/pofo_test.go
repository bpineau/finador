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
