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
	got, err := y.Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2026-06-01"))
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
	if _, err := y.Daily(context.Background(), Ref{Symbol: "NOPE"}, mustDate("2026-06-01")); !errors.Is(err, domain.ErrNotFound) {
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
	if _, err := y.Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2026-06-01")); err != nil {
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

func TestYahooDailyEmptyQuote(t *testing.T) {
	y := testYahoo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"EUR"},"timestamp":[1780297200],"indicators":{"quote":[]}}],"error":null}}`))
	})
	got, err := y.Daily(context.Background(), Ref{Symbol: "X"}, mustDate("2026-06-01"))
	if err != nil || len(got.Closes) != 0 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
