package market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"finador/internal/domain"
)

// FT search response shape (mirrors portfodor's structs): data.security[] with
// name/symbol/xid. The first hit is quoted in GBX (pence) and must be skipped
// in favour of the EUR listing.
const ftSearchEUR = `{"data":{"security":[
  {"name":"Convex Europe Small GBX","symbol":"LU1111111111:GBX","xid":"999999","isPrimary":false},
  {"name":"Convex Europe Small","symbol":"LU1111111111:EUR","xid":"118135654","isPrimary":true}
]}}`

// FT search with no quotable listing (no xid).
const ftSearchNoXid = `{"data":{"security":[
  {"name":"Some thing","symbol":"XX:EUR","xid":"","isPrimary":false}
]}}`

// FT chart response: Dates paired with a Close ComponentSeries, plus a non-Close
// series to make sure we pick the right one. One null close is skipped.
const ftChart = `{"Dates":["2026-06-09T00:00:00","2026-06-10T00:00:00","2026-06-11T00:00:00"],
"Elements":[{"Currency":"EUR","ComponentSeries":[
  {"Type":"Open","Values":[240.0,241.0,241.5]},
  {"Type":"Close","Values":[240.5,null,241.79]}
]}]}`

// ftServer routes the search and chart endpoints to fixtures and records the
// chart POST body so the test can assert the resolved xid was used.
func ftServer(t *testing.T, search, chart string, gotBody *map[string]any) *FT {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data/searchapi/searchsecurities":
			if r.URL.Query().Get("query") == "" {
				t.Errorf("search missing query")
			}
			w.Write([]byte(search))
		case "/data/chartapi/series":
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("chart content-type = %q", ct)
			}
			if gotBody != nil {
				_ = json.NewDecoder(r.Body).Decode(gotBody)
			}
			w.Write([]byte(chart))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	f := NewFT()
	f.BaseURL = srv.URL
	return f
}

func TestFTDaily(t *testing.T) {
	var body map[string]any
	f := ftServer(t, ftSearchEUR, ftChart, &body)
	got, err := f.Daily(context.Background(), Ref{ISIN: "LU1111111111"}, mustDate("2026-06-08"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != domain.EUR {
		t.Errorf("ccy = %s, want EUR", got.Currency)
	}
	// null close is skipped → 2 points, the EUR (non-GBX) listing's xid is used.
	if len(got.Closes) != 2 {
		t.Fatalf("closes = %+v", got.Closes)
	}
	if got.Closes[0].Date != mustDate("2026-06-09") || got.Closes[0].Close != 240.5 {
		t.Errorf("close[0] = %+v", got.Closes[0])
	}
	if got.Closes[1].Date != mustDate("2026-06-11") || got.Closes[1].Close != 241.79 {
		t.Errorf("close[1] = %+v", got.Closes[1])
	}
	// the chart POST must reference the non-GBX listing's xid (118135654)
	elems, _ := body["elements"].([]any)
	if len(elems) != 1 {
		t.Fatalf("elements = %v", body["elements"])
	}
	if sym := elems[0].(map[string]any)["Symbol"]; sym != "118135654" {
		t.Errorf("chart used xid %v, want 118135654 (the EUR listing)", sym)
	}
}

func TestFTDailyNoRefs(t *testing.T) {
	f := NewFT()
	if _, err := f.Daily(context.Background(), Ref{}, mustDate("2026-06-01")); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (no isin, no symbol)", err)
	}
}

// FT search resolving a US mutual fund by its ticker (no colon in the symbol).
const ftSearchUSD = `{"data":{"security":[
  {"name":"Invesco S&P 500 Index Fund Class C","symbol":"SPICX","xid":"300376","isPrimary":true}
]}}`

const ftChartUSD = `{"Dates":["2026-06-10T00:00:00","2026-06-11T00:00:00"],
"Elements":[{"Currency":"USD","ComponentSeries":[{"Type":"Close","Values":[73.66,74.02]}]}]}`

// FT resolves by ticker too (a US mutual fund like SPICX, or any symbol Yahoo throttles).
func TestFTDailyBySymbol(t *testing.T) {
	f := ftServer(t, ftSearchUSD, ftChartUSD, nil)
	got, err := f.Daily(context.Background(), Ref{Symbol: "SPICX"}, mustDate("2026-06-09"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != domain.USD || len(got.Closes) != 2 || got.Closes[1].Close != 74.02 {
		t.Fatalf("got %+v ccy=%s", got.Closes, got.Currency)
	}
}

// A stale/unknown ISIN with a good ticker (e.g. after converting an FCPE): FT
// tries the ISIN, gets nothing, then resolves by the ticker.
func TestFTDailyISINFallsBackToSymbol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data/searchapi/searchsecurities":
			if r.URL.Query().Get("query") == "SPICX" {
				w.Write([]byte(ftSearchUSD))
			} else {
				w.Write([]byte(ftSearchNoXid)) // the ISIN resolves to nothing
			}
		case "/data/chartapi/series":
			w.Write([]byte(ftChartUSD))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	f := NewFT()
	f.BaseURL = srv.URL
	got, err := f.Daily(context.Background(), Ref{ISIN: "990000000000", Symbol: "SPICX"}, mustDate("2026-06-09"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Closes) != 2 || got.Closes[1].Close != 74.02 {
		t.Fatalf("got %+v", got.Closes)
	}
}

func TestFTDailyNotListed(t *testing.T) {
	f := ftServer(t, ftSearchNoXid, ftChart, nil)
	if _, err := f.Daily(context.Background(), Ref{ISIN: "LU0000000000"}, mustDate("2026-06-01")); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (no xid in search)", err)
	}
}

func TestFTName(t *testing.T) {
	if NewFT().Name() != "ft" {
		t.Errorf("Name = %q", NewFT().Name())
	}
}
