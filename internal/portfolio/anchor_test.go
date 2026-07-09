package portfolio

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/perf"
)

// fxDrift is a date-aware FX stub: it models EUR/USD drifting between two days
// while a held USD security's price is frozen (the exchange being shut). USD→EUR
// on/after 2026-07-09 differs from earlier days; every other pair is identity.
type fxDrift struct{}

func (fxDrift) Convert(amount float64, from, to domain.Currency, d domain.Date) (float64, error) {
	if from == to {
		return amount, nil
	}
	if from == domain.USD && to == domain.EUR {
		rate := 1.0 / 1.14038 // value of 1 EUR in USD on 07-07 (07-08 forward-fills it)
		if !d.Before(mustDate("2026-07-09")) {
			rate = 1.0 / 1.14416 // a fresh 07-09 FX point
		}
		return amount * rate, nil
	}
	return 0, errors.New("unexpected pair: " + string(from) + "→" + string(to))
}

// ddogBook holds 10 DDOG (USD) in a EUR-reported book. DDOG closed 256.81 on
// 07-07 and 261.09 on 07-08 (a +1.67% session); the US market has NOT opened on
// 07-09, so there is no 07-09 close - forward-fill repeats 261.09.
func ddogBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO", Currency: domain.USD}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "dd", Kind: domain.Security, Name: "Datadog",
		Ticker: "DDOG", Currency: domain.USD, Group: "actions"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-06-01"), Account: "cto", Asset: "dd",
		Kind: domain.Buy, Quantity: decimal.RequireFromString("10"),
		Amount: domain.Money{Amount: decimal.RequireFromString("2000"), Currency: domain.USD}})
	b.Market.Price("dd").Merge([]domain.PricePoint{
		{Date: mustDate("2026-07-07"), Close: 256.81},
		{Date: mustDate("2026-07-08"), Close: 261.09}, // last real close; no 07-09
	})
	return b
}

func TestCloseAnchor(t *testing.T) {
	b := ddogBook(t)
	// The last settled close is 07-08, even though "today" is 07-09.
	if got := perf.CloseAnchor(&b.Market, mustDate("2026-07-09")); got != mustDate("2026-07-08") {
		t.Errorf("CloseAnchor = %s, want 2026-07-08", got)
	}
	// No prices → the caller's date is preserved (property-only book behaviour).
	empty := domain.NewBook()
	if got := perf.CloseAnchor(&empty.Market, mustDate("2026-07-09")); got != mustDate("2026-07-09") {
		t.Errorf("CloseAnchor(no prices) = %s, want 2026-07-09 (unchanged)", got)
	}
}

// The "1d" row must be DDOG's last session (+1.67%), not the FX drift that
// anchoring on calendar today produces (a stale price at a fresh 07-09 rate
// against a stale 07-07 rate).
func TestOneDayAnchorReflectsLastSession(t *testing.T) {
	b := ddogBook(t)
	today := mustDate("2026-07-09")
	res, err := Series(b, scopeOf(t, b, ""), domain.Date{}, today, domain.EUR, fxDrift{})
	if err != nil {
		t.Fatal(err)
	}
	pts, fls := res.PerfPoints(false), res.PerfFlows()

	oneDay := func(evalTo domain.Date) (float64, bool) {
		rows, _ := perf.Report(pts, fls, evalTo, 0)
		for _, r := range rows {
			if r.Name == "1d" {
				return r.TWR, r.HasTWR
			}
		}
		return 0, false
	}

	// Bug reproduction: anchored on calendar today, "1d" is negative FX drift.
	buggy, ok := oneDay(today)
	if !ok || buggy >= 0 {
		t.Fatalf("expected a negative FX-drift 1d anchored on today, got %.5f (ok=%v)", buggy, ok)
	}

	// Fix: anchored on the last close, "1d" is DDOG's real +1.67% session.
	fixed, ok := oneDay(perf.CloseAnchor(&b.Market, today))
	if !ok {
		t.Fatal("no 1d row when anchored on the last close")
	}
	want := 261.09/256.81 - 1 // ~ +0.01667
	if d := fixed - want; d > 1e-4 || d < -1e-4 {
		t.Errorf("anchored 1d = %.5f, want DDOG's last session %.5f", fixed, want)
	}
}
