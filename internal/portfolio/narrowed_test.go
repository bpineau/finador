package portfolio

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

// narrowedBook: one envelope taxed on gains, two securities bought at 100 and
// both worth 200 - so the whole envelope owes tax on 2000 of gain, and each
// position owes tax on 1000.
func narrowedBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	rule, err := domain.ParseTaxRule("gains:31.4%")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia",
		Currency: domain.EUR, Tax: rule}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*domain.Asset{
		{ID: "cw8", Kind: domain.Security, Name: "CW8", Ticker: "CW8.PA", Currency: domain.EUR, Group: "equities"},
		{ID: "gtwr", Kind: domain.Security, Name: "GTWR", Ticker: "GTWR", Currency: domain.EUR, Group: "bonds"},
	} {
		if err := b.AddAsset(a); err != nil {
			t.Fatal(err)
		}
		b.Add(domain.Transaction{Date: mustDate("2026-01-05"), Account: "cto", Asset: a.ID,
			Kind: domain.Buy, Quantity: dec("10"), Amount: eur("1000")})
		b.Market.Price(a.ID).Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 200}})
	}
	return b
}

// An envelope's latent tax is a property of the WHOLE envelope: tax on
// max(0, value - contribution basis). Narrowing the scope with --exclude or
// --asset removes positions from the value and nothing from the basis, so the
// envelope rule reported 0 EUR of tax where the kept position alone carries
// 314. A narrowed scope is no longer a whole envelope, so it falls back on
// the per-position rule - the one a group or an asset scope already uses -
// and says so.
func TestNarrowedScopeUsesThePerPositionTax(t *testing.T) {
	b := narrowedBook(t)
	at := mustDate("2026-06-02")
	fx := fxStub{}

	whole, err := Value(b, Scope{Kind: All}, at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "whole gross", whole.Gross, 4000)
	approx(t, "whole tax", whole.Tax, 628)
	if whole.TaxNote != "" {
		t.Errorf("whole scope carries a note: %q", whole.TaxNote)
	}

	for _, tc := range []struct {
		name  string
		scope Scope
	}{
		{"--exclude", Scope{Kind: All, Excluded: map[domain.AssetID]bool{"gtwr": true}}},
		{"--asset", Scope{Kind: All, Only: map[domain.AssetID]bool{"cw8": true}}},
		{"--exclude on an envelope", func() Scope {
			acc, err := b.Account("cto")
			if err != nil {
				t.Fatal(err)
			}
			s := AccountScope(acc)
			s.Excluded = map[domain.AssetID]bool{"gtwr": true}
			return s
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Value(b, tc.scope, at, domain.EUR, fx)
			if err != nil {
				t.Fatal(err)
			}
			approx(t, "gross", got.Gross, 2000)
			approx(t, "tax", got.Tax, 314)
			if !strings.Contains(got.TaxNote, "per position") {
				t.Errorf("TaxNote = %q, expected it to say the tax is per position", got.TaxNote)
			}
		})
	}
}

// Series() must apply the same rule as Value(), pointwise: a narrowed scope
// reads the per-position tax on the curve too.
func TestNarrowedScopeSeriesAgreesWithValue(t *testing.T) {
	b := narrowedBook(t)
	at := mustDate("2026-06-02")
	fx := fxStub{}
	scopes := []Scope{
		{Kind: All, Excluded: map[domain.AssetID]bool{"gtwr": true}},
		{Kind: All, Only: map[domain.AssetID]bool{"cw8": true}},
	}
	for _, sc := range scopes {
		want, err := Value(b, sc, at, domain.EUR, fx)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Series(b, sc, mustDate("2026-01-01"), at, domain.EUR, fx)
		if err != nil {
			t.Fatal(err)
		}
		last := res.Points[len(res.Points)-1]
		approx(t, "series gross", last.Gross, want.Gross)
		approx(t, "series net", last.Net, want.Net)
		approx(t, "series net value", last.Net, 2000-314)
	}
}
