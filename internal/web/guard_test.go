package web

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/store"
)

// TestCrossSitePostRefused: a form another site submits to the local server
// is refused before it mutates anything; the same form from the page itself,
// or from a client that sends no browser headers, still goes through.
func TestCrossSitePostRefused(t *testing.T) {
	srv, f := testServer(t)
	post := func(site, origin string) int {
		form := url.Values{"name": {"CTO Meridia"}, "currency": {"EUR"}}
		req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("cross-site", "https://untrusted.example"); code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", code)
	}
	if code := post("", "https://untrusted.example"); code != http.StatusForbidden {
		t.Fatalf("cross-origin POST without fetch metadata = %d, want 403", code)
	}
	if n := len(f.Book.Accounts); n != 1 {
		t.Fatalf("a refused POST still created an account: %d accounts", n)
	}
	if code := post("same-origin", ""); code != http.StatusSeeOther {
		t.Fatalf("same-origin POST = %d, want 303", code)
	}
}

// TestLoopbackOnly: a DNS-rebound name reaching the loopback server is
// refused; the loopback names are served.
func TestLoopbackOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	h := LoopbackOnly(ok)
	for host, want := range map[string]int{
		"127.0.0.1:8451":         http.StatusOK,
		"localhost:8451":         http.StatusOK,
		"[::1]:8451":             http.StatusOK,
		"untrusted.example:8451": http.StatusMisdirectedRequest,
		"127.0.0.1.example:8451": http.StatusMisdirectedRequest,
		"untrusted.example":      http.StatusMisdirectedRequest,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s = %d, want %d", host, rec.Code, want)
		}
	}
}

// TestColdAssetPagesReadConcurrently: asset pages are served under the read
// lock, so rendering one with no cached quote must not write the price map
// (run under -race).
func TestColdAssetPagesReadConcurrently(t *testing.T) {
	srv, f := testServer(t)
	var ids []string
	for i := range 8 {
		id := fmt.Sprintf("cold%d", i)
		if err := f.Book.AddAsset(&domain.Asset{ID: domain.AssetID(id), Kind: domain.Security,
			Name: "Cold " + id, Currency: domain.EUR}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	h := srv.Handler()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/asset/"+id, nil))
		})
	}
	wg.Wait()
}

// TestFailedImportLeavesNothingBehind: a CSV stopped by a bad line keeps none
// of its earlier lines, not even in memory, where the next unrelated save
// would have persisted them.
func TestFailedImportLeavesNothingBehind(t *testing.T) {
	srv, f := testServer(t)
	before := len(f.Book.Transactions)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "txs.csv")
	part.Write([]byte("date,kind,account,amount,currency\n" +
		"2026-02-01,deposit,PEA Zephyr,77,EUR\n" +
		"not-a-date,deposit,PEA Zephyr,1,EUR\n"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error") {
		t.Fatalf("a bad line did not fail the import: %q", loc)
	}
	if got := len(f.Book.Transactions); got != before {
		t.Fatalf("a failed import left %d transaction(s) in memory", got-before)
	}
}

// TestFailedSaveIsNotPersistedLater: a deposit whose save failed (HTTP 500)
// must not reach the disk with the next, unrelated, successful save.
func TestFailedSaveIsNotPersistedLater(t *testing.T) {
	srv, f := testServer(t)
	before := len(f.Book.Transactions)
	block := f.Path + ".tmp"
	if err := os.Mkdir(block, 0o700); err != nil { // the save cannot write its temp file
		t.Fatal(err)
	}
	code, _, _ := postForm(t, srv, "/tx", url.Values{"date": {"2026-03-01"}, "kind": {"deposit"},
		"account": {"PEA Zephyr"}, "amount": {"77"}})
	if code != http.StatusInternalServerError {
		t.Fatalf("POST /tx with a failing save = %d, want 500", code)
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := postForm(t, srv, "/accounts", url.Values{"name": {"CTO Meridia"}, "currency": {"EUR"}}); code != http.StatusSeeOther {
		t.Fatalf("POST /accounts = %d", code)
	}
	back, err := store.Open(f.Path, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(back.Book.Transactions); got != before {
		t.Fatalf("the failed deposit was persisted by a later save: %d transaction(s) added", got-before)
	}
}

// TestNewAssetTakesTheEnteredCurrency: buying an unknown asset in USD from an
// EUR account declares the asset in USD, as `finador asset buy` does.
func TestNewAssetTakesTheEnteredCurrency(t *testing.T) {
	srv, f := testServer(t)
	code, _, _ := postForm(t, srv, "/tx", url.Values{"date": {"2026-03-01"}, "kind": {"buy"},
		"account": {"PEA Zephyr"}, "asset": {"GTWR"}, "qty": {"1"}, "amount": {"100"}, "ccy": {"USD"}})
	if code != http.StatusSeeOther {
		t.Fatalf("POST /tx = %d", code)
	}
	a, err := f.Book.Asset("GTWR")
	if err != nil {
		t.Fatal(err)
	}
	if a.Currency != domain.USD {
		t.Fatalf("new asset declared in %s, want USD", a.Currency)
	}
}

// TestSparkSkipsDaysWithoutRate: a USD price with no USD/EUR rate is left out
// of an EUR sparkline, never drawn as if it were euros.
func TestSparkSkipsDaysWithoutRate(t *testing.T) {
	ps := &domain.PriceSeries{Points: []domain.PricePoint{
		{Date: day(t, "2026-01-02"), Close: 10}, {Date: day(t, "2026-01-05"), Close: 20},
	}}
	pts := assetPricePoints(ps, day(t, "2026-01-01"), day(t, "2026-01-06"), domain.USD, domain.EUR,
		market.Converter{FX: map[domain.Currency]*domain.PriceSeries{}})
	if len(pts) != 0 {
		t.Fatalf("USD closes drawn on an EUR sparkline with no rate: %+v", pts)
	}
}
