package cli_test

import (
	"context"
	"strings"
	"testing"

	"finador/internal/domain"
	"finador/internal/market"
)

// nowcastSource is fakeSource plus the NowcastSource capability: ERES_DATADOG
// (last published NAV 208 EUR on 2026-06-05) is carried by DDOG, which closes
// at 100 USD that day and 110 USD on 2026-06-08, the EUR staying at 1.10 USD.
// With onOpen, the fund's NAV is struck on DDOG's opening print, 95 % of the
// close that day.
type nowcastSource struct {
	fakeSource
	onOpen bool
	none   bool // the source names no proxy at all
}

func (s nowcastSource) NowcastProxy(ref market.Ref) (market.Proxy, bool) {
	if s.none || ref.Symbol != "ERES_DATADOG" {
		return market.Proxy{}, false
	}
	return market.Proxy{Symbol: "DDOG", Currency: domain.USD, OnOpen: s.onOpen}, true
}

func (nowcastSource) ProxyCloses(_ context.Context, p market.Proxy, _ domain.Date) ([]domain.PricePoint, error) {
	return []domain.PricePoint{{Date: day("2026-06-05"), Close: 100}, {Date: day("2026-06-08"), Close: 110}}, nil
}

func (nowcastSource) OpenFactors(_ context.Context, _ market.Proxy, _ domain.Date) ([]domain.PricePoint, error) {
	return []domain.PricePoint{{Date: day("2026-06-05"), Close: 0.95}, {Date: day("2026-06-08"), Close: 0.98}}, nil
}

func day(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// bookWithTenFundShares: ten shares of a lagged fund in an untaxed envelope.
func bookWithTenFundShares(t *testing.T) string {
	t.Helper()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEE Zephyr")
	run(t, db, "asset", "add", "ERES_DATADOG", "--ccy", "EUR", "--alias", "eres")
	run(t, db, "asset", "buy", "eres", "10", "@200", "2026-06-01")
	return db
}

// TestValueEstimatesALaggedFund: past its last NAV, a fund is worth that NAV
// carried by its proxy's move (208 x 110/100), anchored on the open when the
// NAV is struck there (208 x 110/95), and the figure says so.
func TestValueEstimatesALaggedFund(t *testing.T) {
	for _, tc := range []struct {
		name   string
		onOpen bool
		want   string
	}{
		{"close anchor", false, "2288.00 EUR"},
		{"open anchor", true, "2408.42 EUR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := bookWithTenFundShares(t)
			out := runSession(t, nowcastSource{onOpen: tc.onOpen}, db, "value", "--gross")
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %s:\n%s", tc.want, out)
			}
			if !strings.Contains(out, "estimate for 2026-06-08") || !strings.Contains(out, "carried by DDOG") {
				t.Errorf("the figure does not say it rests on an estimate:\n%s", out)
			}
		})
	}
}

// TestValueEstimateIsComputedFromTheCache is the bug this pins: the estimate
// only existed in the process that fetched it, so the next command (offline,
// or inside the spot pass's freshness window) fell back to the stale NAV.
// The cache holds the proxy's quotes, so every read computes the same figure.
func TestValueEstimateIsComputedFromTheCache(t *testing.T) {
	db := bookWithTenFundShares(t)
	if out := runSession(t, nowcastSource{}, db, "value", "--gross"); !strings.Contains(out, "2288.00 EUR") {
		t.Fatalf("estimated valuation:\n%s", out)
	}
	out := run(t, db, "value", "--gross") // offline: the cache only
	if !strings.Contains(out, "2288.00 EUR") || !strings.Contains(out, "estimate for 2026-06-08") {
		t.Errorf("a cache-only valuation lost the estimate:\n%s", out)
	}
	out = run(t, db, "value", "--tree")
	if !strings.Contains(out, "2288") || !strings.Contains(out, "estimate for 2026-06-08") {
		t.Errorf("the tree lost the estimate, or its label:\n%s", out)
	}
}

// TestValueEstimateNotPersisted: the cache stores the proxy's quotes, never
// the estimate. Once the source names no proxy any more, the fund is back at
// its last published NAV, which an estimate merged into the series would
// have overwritten.
func TestValueEstimateNotPersisted(t *testing.T) {
	db := bookWithTenFundShares(t)
	if out := runSession(t, nowcastSource{}, db, "value", "--gross"); !strings.Contains(out, "2288.00 EUR") {
		t.Fatalf("estimated valuation:\n%s", out)
	}
	runSession(t, nowcastSource{none: true}, db, "refresh") // always forced
	out := run(t, db, "value", "--gross")
	if !strings.Contains(out, "2080.00 EUR") || strings.Contains(out, "estimate") {
		t.Errorf("want the last published NAV (10 x 208), unlabelled:\n%s", out)
	}
}

// TestPerfAndChartWalkTheEstimate: the day's move and the history end on the
// estimate too, labelled, so a lagged fund does not read flat until its NAV
// lands.
func TestPerfAndChartWalkTheEstimate(t *testing.T) {
	db := bookWithTenFundShares(t)
	runSession(t, nowcastSource{}, db, "refresh")
	out := run(t, db, "chart", "--asset", "eres")
	if !strings.Contains(out, "last point: 2288.00 EUR") || !strings.Contains(out, "estimate for 2026-06-08") {
		t.Errorf("the history does not end on the labelled estimate:\n%s", out)
	}
	out = run(t, db, "perf")
	if !strings.Contains(out, "estimate for 2026-06-08") {
		t.Errorf("perf does not label the estimate it measured:\n%s", out)
	}
}
