package web

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

// spotSrc scripts a live quote on top of fakeSource's daily data, and counts
// the daily fetches of the asset under test.
type spotSrc struct {
	fakeSource
	dailyCalls atomic.Int32
	quote      market.Quote
}

func (s *spotSrc) Daily(ctx context.Context, ref market.Ref, from domain.Date) (market.DailyData, error) {
	if ref.Symbol == "CW8.PA" {
		s.dailyCalls.Add(1)
	}
	return s.fakeSource.Daily(ctx, ref, from)
}

func (s *spotSrc) Latest(_ context.Context, ref market.Ref) (market.Quote, error) {
	if ref.Symbol == "CW8.PA" && s.quote.Price > 0 {
		return s.quote, nil
	}
	return market.Quote{}, market.ErrNotCovered
}

// An online refresh tick force-refreshes the market cache (Yahoo's daily candle
// carries today's live price), stamping the series as fetched today.
func TestRefreshOnceUpdatesCache(t *testing.T) {
	srv, f := testServer(t)
	srv.offline = false // testServer is offline by default; this tick must fetch
	if !f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("precondition: FetchedAt should start zero")
	}

	srv.refreshOnce(context.Background())

	if f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("online refreshOnce did not refresh the cache")
	}
}

// A refresh tick runs the daily fetch only when due (once a day), then keeps
// today's price following the market through light spot updates, and records
// the quote freshness for the UI.
func TestRefreshOnceSpotKeepsTodayLive(t *testing.T) {
	srv, f := testServer(t)
	srv.offline = false
	src := &spotSrc{quote: market.Quote{Price: 561.5, Time: time.Now(), Currency: domain.EUR, Live: true}}
	srv.source = src

	srv.refreshOnce(context.Background())
	if src.dailyCalls.Load() != 1 {
		t.Fatalf("daily calls = %d, want 1", src.dailyCalls.Load())
	}
	if close, _, ok := f.Book.Market.Price("cw8").At(domain.Today()); !ok || close != 561.5 {
		t.Errorf("today's close = %v, want the live spot 561.5", close)
	}

	// Same day, next tick: no daily re-fetch, but the spot moves the price.
	src.quote.Price = 562.25
	srv.refreshOnce(context.Background())
	if src.dailyCalls.Load() != 1 {
		t.Errorf("daily calls = %d, the second tick must skip the daily fetch", src.dailyCalls.Load())
	}
	if close, _, ok := f.Book.Market.Price("cw8").At(domain.Today()); !ok || close != 562.25 {
		t.Errorf("today's close = %v, want the refreshed spot 562.25", close)
	}

	asset, err := f.Book.Asset("cw8")
	if err != nil {
		t.Fatal(err)
	}
	note := srv.quoteNote(asset)
	if !strings.Contains(note, "562.25 EUR") || !strings.Contains(note, "live at") {
		t.Errorf("quote note = %q, want the live spot and its time", note)
	}
}

// Offline mode never touches the network.
func TestRefreshOnceNoopOffline(t *testing.T) {
	srv, f := testServer(t) // offline = true
	srv.refreshOnce(context.Background())
	if !f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("offline refreshOnce must not fetch")
	}
}

// A nowcast quote (a fund priced once a day, carried forward by a proxy) is
// captioned as an estimate, never as a live print.
func TestQuoteNoteSaysEstimated(t *testing.T) {
	srv, f := testServer(t)
	srv.offline = false
	srv.source = &spotSrc{quote: market.Quote{Price: 71.15, Time: time.Now(), Currency: domain.EUR, Live: true, Estimated: true}}
	srv.refreshOnce(context.Background())
	asset, err := f.Book.Asset("cw8")
	if err != nil {
		t.Fatal(err)
	}
	note := srv.quoteNote(asset)
	if !strings.Contains(note, "71.15 EUR") || !strings.Contains(note, "estimated at") || strings.Contains(note, "live at") {
		t.Errorf("quote note = %q, want the estimate flagged as such", note)
	}

	// It never reaches the stored series, so it cannot be served as a close.
	last, ok := f.Book.Market.Price("cw8").Last()
	if !ok || last.Close == 71.15 || last.Date == domain.Today() {
		t.Errorf("last stored point = %+v, want the last published close", last)
	}
	// A restart forgets what the session observed: what is left must be the
	// last published close, dated as one, and nothing may claim a close today.
	srv.spot = nil
	restarted := srv.quoteNote(asset)
	if strings.Contains(restarted, domain.Today().String()) || !strings.Contains(restarted, "close of") {
		t.Errorf("caption after a restart = %q, want the last published close", restarted)
	}
}

// The overview values an estimated line at its estimate - the freshest thing
// there is - through a throwaway override, and says so in the page's notes.
func TestOverviewValuesEstimateAsOverride(t *testing.T) {
	srv, f := testServer(t)
	srv.offline = false
	srv.source = &spotSrc{quote: market.Quote{Price: 600, Time: time.Now(),
		Currency: domain.EUR, Live: true, Estimated: true}}
	srv.refreshOnce(context.Background())

	scope, err := portfolio.ParseScope(f.Book, "cw8")
	if err != nil {
		t.Fatal(err)
	}
	val, err := portfolio.Value(f.Book, scope, domain.Today(), domain.EUR,
		market.Converter{FX: f.Book.Market.FX}, srv.estimatePrices()...)
	if err != nil {
		t.Fatal(err)
	}
	if val.Gross != 6000 {
		t.Errorf("gross = %v, want the estimate (10 x 600) to price today", val.Gross)
	}
	if len(val.Stale) == 0 || !strings.Contains(strings.Join(val.Stale, " "), "estimate") {
		t.Errorf("notes = %v, want the estimate named", val.Stale)
	}
}
