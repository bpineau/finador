package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"finador/internal/domain"
	"finador/internal/market"
)

// estimateSource answers every batched ref with a nowcast: a fund nobody has
// priced today, carried forward by a listed proxy. It claims no session (pofo
// leaves Session empty on an estimate) and is the only way to exercise the
// estimate path without an employee-savings fund.
type estimateSource struct {
	fakeSource
	price float64
}

func (s estimateSource) LatestBatch(_ context.Context, refs []market.Ref) market.BatchQuotes {
	at := time.Date(2026, 9, 10, 16, 14, 0, 0, time.Local)
	out := market.BatchQuotes{Quotes: map[market.Ref]market.Quote{}, Errs: map[market.Ref]error{}}
	for _, ref := range refs {
		if ref.Symbol != "CW8.PA" {
			out.Errs[ref] = market.ErrNotCovered
			continue
		}
		out.Quotes[ref] = market.Quote{
			Price: s.price, Time: at, Currency: domain.EUR, Live: true, Estimated: true,
		}
	}
	return out
}

// TestValueUsesEstimateLabelled: an estimate prices today's valuation - it is
// the freshest thing there is - but it says what it is, and it does not borrow
// the extended-hours vocabulary (no session was involved).
func TestValueUsesEstimateLabelled(t *testing.T) {
	db := bookWithTenShares(t)
	out := runSession(t, estimateSource{price: 600}, db, "value", "--gross")

	if !strings.Contains(out, "6000.00 EUR") {
		t.Errorf("the estimate did not price the valuation:\n%s", out)
	}
	if !strings.Contains(out, "estimate") || !strings.Contains(out, "CW8.PA") {
		t.Errorf("the figure does not say it rests on an estimate:\n%s", out)
	}
	if !strings.Contains(out, "no published price") {
		t.Errorf("the note does not say nobody has struck that number:\n%s", out)
	}
	if strings.Contains(out, "extended hours") {
		t.Errorf("an estimate is no off-hours print:\n%s", out)
	}
}

// TestValueEstimateNotPersisted is the bug this pins: an estimate reached the
// encrypted cache, stayed there for good and was served as a close of that
// date - including after a restart, where nothing remembers it was estimated.
func TestValueEstimateNotPersisted(t *testing.T) {
	db := bookWithTenShares(t)
	if out := runSession(t, estimateSource{price: 600}, db, "value", "--gross"); !strings.Contains(out, "6000.00 EUR") {
		t.Fatalf("estimated valuation:\n%s", out)
	}

	// A fresh process (a new command, the same cache sidecar) is back to the
	// last published close, and claims no estimate it cannot see any more.
	out := run(t, db, "value", "--gross")
	if !strings.Contains(out, "5600.00 EUR") {
		t.Errorf("offline valuation after an estimated pass:\n%s", out)
	}
	if strings.Contains(out, "estimate") {
		t.Errorf("a restarted valuation claims an estimate it no longer holds:\n%s", out)
	}

}

// TestPerfAndChartIgnoreEstimates: performance and history read published
// prices only. Both are computed from the stored series, so an estimated pass
// must leave their output exactly as it was.
func TestPerfAndChartIgnoreEstimates(t *testing.T) {
	db := bookWithTenShares(t)
	if out := runSession(t, estimateSource{price: 600}, db, "value", "--gross"); !strings.Contains(out, "6000.00 EUR") {
		t.Fatalf("estimated valuation:\n%s", out)
	}

	for _, args := range [][]string{{"perf"}, {"chart", "--asset", "cw8"}} {
		if out := run(t, db, args...); strings.Contains(out, "6000") {
			t.Errorf("%v walked the estimate:\n%s", args, out)
		}
	}
	if out := run(t, db, "chart", "--asset", "cw8"); !strings.Contains(out, "last point: 5600.00 EUR") {
		t.Errorf("the history does not end on the last published close:\n%s", out)
	}
}
