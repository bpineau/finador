package portfolio

import (
	"fmt"
	"testing"

	"finador/internal/domain"
)

// spreadEnvelopes builds a book whose per-envelope figures span ten orders of
// magnitude: one large balance and forty tiny ones, each below the ULP of the
// large one. Adding them large-first swallows the small ones, small-first
// keeps them - so a sum accumulated in Go's randomized map order lands on a
// different float from one run to the next.
func spreadEnvelopes(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	rule, err := domain.ParseTaxRule("value:20%")
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, amount string) {
		accID := domain.AccountID(id)
		if err := b.AddAccount(&domain.Account{ID: accID, Name: id,
			Currency: domain.EUR, Tax: rule}); err != nil {
			t.Fatal(err)
		}
		b.Add(domain.Transaction{Date: mustDate("2026-01-05"), Account: accID,
			Kind: domain.Statement, Amount: eur(amount)})
	}
	add("big", "10000000000.13")
	for i := 0; i < 80; i++ {
		add(fmt.Sprintf("small%02d", i), "0.0000005")
	}
	return b
}

// A float sum is not associative, so a total accumulated in Go's randomized
// map order differs in its last digits from one run to the next. The output
// must be byte-stable: goldens, a diff of two runs and the Android
// cross-check all rest on it (D46).
func TestValueTotalsAreRunStable(t *testing.T) {
	b := spreadEnvelopes(t)
	at := mustDate("2026-06-02")
	want, err := Value(b, Scope{Kind: All}, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		got, err := Value(b, Scope{Kind: All}, at, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Gross != want.Gross || got.Tax != want.Tax || got.Net != want.Net {
			t.Fatalf("run %d: gross/tax/net %.17g/%.17g/%.17g, first run %.17g/%.17g/%.17g",
				i, got.Gross, got.Tax, got.Net, want.Gross, want.Tax, want.Net)
		}
	}
}

// Same invariant on the curve: valueAt sums the cash balances and the
// per-account grosses, both held in maps.
func TestSeriesPointsAreRunStable(t *testing.T) {
	b := spreadEnvelopes(t)
	from, to := mustDate("2026-01-01"), mustDate("2026-02-10")
	want, err := Series(b, Scope{Kind: All}, from, to, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		got, err := Series(b, Scope{Kind: All}, from, to, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		for j := range want.Points {
			if got.Points[j] != want.Points[j] {
				t.Fatalf("run %d point %d: %+v, first run %+v", i, j, got.Points[j], want.Points[j])
			}
		}
	}
}
