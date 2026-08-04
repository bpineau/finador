package portfolio

import (
	"testing"

	"finador/internal/domain"
	"finador/internal/perf"
)

// unquotedFundBook is the README onboarding recipe for a fund with no market
// data: buy it, then value it with `asset set` (the statement is the price
// fallback). declaresCash toggles whether the envelope also declares a cash
// balance - which the trades must leave strictly alone.
func unquotedFundBook(t *testing.T, declaresCash bool) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "fund", Kind: domain.Security, Name: "FCPE Fund", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if declaresCash {
		b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "cto", Kind: domain.Deposit, Amount: eur("10000")})
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-10"), Account: "cto", Asset: "fund",
		Kind: domain.Buy, Quantity: dec("10"), Amount: eur("4000")})
	b.Add(domain.Transaction{Date: mustDate("2026-02-10"), Account: "cto", Asset: "fund",
		Kind: domain.Statement, Amount: eur("4200")})
	return b
}

// A buy is never a gain NOR a loss: a bought security with no quote is valued
// at cost until observed, so the buy day stays flow-neutral and the buy is an
// external flow - the first statement is a NAV observation (performance),
// never a second adoption. Declaring cash alongside changes nothing to the
// flows; it only adds an idle balance that dilutes the measured return.
func TestSeriesUnquotedBuyThenStatement(t *testing.T) {
	for _, tc := range []struct {
		name         string
		declaresCash bool
		wantFlows    int
		wantTWR      float64
	}{
		{"no declared cash: the buy is the only flow", false, 1, 4200.0/4000 - 1},
		{"declared cash: same flow, return diluted by the idle balance", true, 1, 14200.0/14000 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := unquotedFundBook(t, tc.declaresCash)
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
			// The buy adds the position at cost and its flow matches it
			// exactly, so the day is neutral for performance.
			approx(t, "buy day value = previous + cost", after, before+4000)
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
