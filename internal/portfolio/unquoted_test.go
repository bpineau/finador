package portfolio

import (
	"testing"

	"finador/internal/domain"
	"finador/internal/perf"
)

// unquotedFundBook is the README onboarding recipe for a fund with no market
// data: buy it, then value it with `asset set` (the statement is the price
// fallback). tracked toggles whether the account carries cash.
func unquotedFundBook(t *testing.T, tracked bool) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "fund", Kind: domain.Security, Name: "FCPE Fund", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if tracked {
		b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "cto", Kind: domain.Deposit, Amount: eur("10000")})
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-10"), Account: "cto", Asset: "fund",
		Kind: domain.Buy, Quantity: dec("10"), Amount: eur("4000")})
	b.Add(domain.Transaction{Date: mustDate("2026-02-10"), Account: "cto", Asset: "fund",
		Kind: domain.Statement, Amount: eur("4200")})
	return b
}

// A buy is never a gain NOR a loss: a bought security with no quote is valued
// at cost until observed, so the buy day is value-neutral and the only
// external flow of an untracked envelope is the buy itself - the first
// statement is a NAV observation (performance), never a second adoption.
func TestSeriesUnquotedBuyThenStatement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tracked   bool
		wantFlows int
		wantTWR   float64
	}{
		{"untracked: buy is the only flow", false, 1, 4200.0/4000 - 1},
		{"tracked: buy is value-neutral, no flow", true, 0, 10200.0/10000 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := unquotedFundBook(t, tc.tracked)
			res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-03-01"), domain.EUR, fxStub{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Flows) != tc.wantFlows {
				t.Fatalf("flows = %+v, want %d", res.Flows, tc.wantFlows)
			}
			approx(t, "TWR", perf.TWR(res.PerfPoints(false), res.PerfFlows()), tc.wantTWR)

			// The buy day must not move the measured value (cost fallback).
			var before, after float64
			for _, p := range res.Points {
				switch p.Date {
				case mustDate("2026-01-09"):
					before = p.Gross
				case mustDate("2026-01-10"):
					after = p.Gross
				}
			}
			if tc.tracked {
				approx(t, "value across the buy day", after, before)
			} else {
				approx(t, "buy day value = cost", after, 4000)
			}
		})
	}
}

// Value() must agree with the end of Series() on the statement-per-share and
// cost fallbacks, like it does on quoted positions.
func TestValueMatchesSeriesOnUnquotedFallbacks(t *testing.T) {
	b := unquotedFundBook(t, true)
	// Sell half after the statement: the NAV observation scales per share.
	b.Add(domain.Transaction{Date: mustDate("2026-02-20"), Account: "cto", Asset: "fund",
		Kind: domain.Sell, Quantity: dec("5"), Amount: eur("2100")})

	for _, at := range []string{"2026-01-15", "2026-02-15", "2026-03-01"} {
		d := mustDate(at)
		want, err := Value(b, scopeOf(t, b, ""), d, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), d, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		approx(t, "gross at "+at, res.Points[len(res.Points)-1].Gross, want.Gross)
	}

	// After selling 5 of the 10 shares observed at 4200, the position is worth
	// 5 × 420 = 2100 - not the stale 4200 total.
	v, err := Value(b, scopeOf(t, b, "fund"), mustDate("2026-03-01"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "per-share statement after sell", v.Gross, 2100)
}
