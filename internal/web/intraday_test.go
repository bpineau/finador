package web

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finador/internal/domain"
	"finador/internal/market"
)

// intradaySrc is a controllable Source for intraday tests.
type intradaySrc struct {
	fakeSource
	data  market.IntradayData
	err   error
	calls atomic.Int32
}

func (s *intradaySrc) Intraday(_ context.Context, _ market.Ref) (market.IntradayData, error) {
	s.calls.Add(1)
	return s.data, s.err
}

func twoPoints() []market.IntradayPoint {
	base := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	return []market.IntradayPoint{
		{Time: base, Close: 550},
		{Time: base.Add(5 * time.Minute), Close: 551},
	}
}

func TestIntradayForCacheFresh(t *testing.T) {
	src := &intradaySrc{data: market.IntradayData{Currency: domain.EUR, Points: twoPoints()}}
	srv, _ := testServer(t)
	srv.source = src
	srv.offline = false

	asset, _ := srv.file.Book.Asset("cw8")

	// first call fetches
	pts, ok := srv.intradayFor(context.Background(), asset)
	if !ok || len(pts) != 2 {
		t.Fatalf("first call: ok=%v pts=%d", ok, len(pts))
	}
	if src.calls.Load() != 1 {
		t.Fatalf("expected 1 fetch, got %d", src.calls.Load())
	}

	// second call immediately after: cache is fresh, no fetch
	pts2, ok2 := srv.intradayFor(context.Background(), asset)
	if !ok2 || len(pts2) != 2 {
		t.Fatalf("second call: ok=%v pts=%d", ok2, len(pts2))
	}
	if src.calls.Load() != 1 {
		t.Errorf("cache not used: expected 1 fetch total, got %d", src.calls.Load())
	}
}

func TestIntradayForStale(t *testing.T) {
	src := &intradaySrc{data: market.IntradayData{Currency: domain.EUR, Points: twoPoints()}}
	srv, _ := testServer(t)
	srv.source = src
	srv.offline = false

	asset, _ := srv.file.Book.Asset("cw8")

	// prime the cache with an expired entry (yesterday)
	yesterday := domain.DateOf(time.Now().AddDate(0, 0, -1))
	srv.intradayMu.Lock()
	srv.intraday[asset.ID] = intradayEntry{day: yesterday, at: time.Now().Add(-10 * time.Minute), pts: twoPoints()}
	srv.intradayMu.Unlock()

	// stale (wrong day) → should fetch
	_, ok := srv.intradayFor(context.Background(), asset)
	if !ok {
		t.Fatal("expected ok=true after refetch")
	}
	if src.calls.Load() != 1 {
		t.Errorf("expected 1 fetch for stale cache, got %d", src.calls.Load())
	}
}

func TestIntradayForOffline(t *testing.T) {
	src := &intradaySrc{data: market.IntradayData{Currency: domain.EUR, Points: twoPoints()}}
	srv, _ := testServer(t)
	srv.source = src
	srv.offline = true

	asset, _ := srv.file.Book.Asset("cw8")

	// offline with no cache → false, no network call
	if _, ok := srv.intradayFor(context.Background(), asset); ok {
		t.Error("expected false when offline and no cache")
	}
	if src.calls.Load() != 0 {
		t.Errorf("network called in offline mode: %d calls", src.calls.Load())
	}

	// offline with today's cache → return it without network call
	today := domain.Today()
	srv.intradayMu.Lock()
	srv.intraday[asset.ID] = intradayEntry{day: today, at: time.Now().Add(-5 * time.Minute), pts: twoPoints()}
	srv.intradayMu.Unlock()

	if pts, ok := srv.intradayFor(context.Background(), asset); !ok || len(pts) != 2 {
		t.Errorf("offline with cache: ok=%v pts=%d", ok, len(pts))
	}
	if src.calls.Load() != 0 {
		t.Errorf("network called in offline mode: %d calls", src.calls.Load())
	}
}

func TestIntradayForSourceError(t *testing.T) {
	src := &intradaySrc{err: market.ErrNotCovered}
	srv, _ := testServer(t)
	srv.source = src
	srv.offline = false

	asset, _ := srv.file.Book.Asset("cw8")

	// source error → false, no panic
	if _, ok := srv.intradayFor(context.Background(), asset); ok {
		t.Error("expected false on source error")
	}
}

// TestAssetPageIntraday verifies the 1d view for a covered vs uncovered asset.
func TestAssetPageIntraday(t *testing.T) {
	src := &intradaySrc{data: market.IntradayData{Currency: domain.EUR, Points: twoPoints()}}
	srv, _ := testServer(t)
	srv.source = src
	srv.offline = false

	// covered asset: intraday available → page renders intraday SVG with HH:MM labels
	code, body := get(t, srv, "/asset/cw8")
	if code != 200 {
		t.Fatalf("asset page = %d", code)
	}
	if !strings.Contains(body, "09:00") {
		t.Errorf("HH:MM label '09:00' missing from intraday chart:\n%s", excerpt(body))
	}
	if !strings.Contains(body, "price EUR") {
		t.Errorf("price legend missing:\n%s", excerpt(body))
	}

	// uncovered: source returns error → fallback rendered, page still 200
	srv2 := &Server{
		file:     srv.file,
		source:   &intradaySrc{err: market.ErrNotCovered},
		offline:  false,
		intraday: make(map[domain.AssetID]intradayEntry),
	}
	code2, body2 := get(t, srv2, "/asset/cw8")
	if code2 != 200 {
		t.Fatalf("fallback page = %d", code2)
	}
	if !strings.Contains(body2, "intraday unavailable") && !strings.Contains(body2, "not enough price history") {
		t.Errorf("fallback message missing:\n%s", excerpt(body2))
	}
}
