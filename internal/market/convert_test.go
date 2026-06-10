package market

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

func testFX() Converter {
	eur := &domain.PriceSeries{}
	eur.Merge([]domain.PricePoint{
		{Date: mustDate("2026-06-01"), Close: 1.10},
		{Date: mustDate("2026-06-03"), Close: 1.12},
	})
	gbp := &domain.PriceSeries{}
	gbp.Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 1.30}})
	return Converter{FX: map[domain.Currency]*domain.PriceSeries{
		domain.EUR: eur, "GBP": gbp,
	}}
}

func TestConvert(t *testing.T) {
	c := testFX()
	for _, tc := range []struct {
		amount   float64
		from, to domain.Currency
		at       string
		want     float64
	}{
		{100, domain.EUR, domain.EUR, "2026-06-01", 100},     // identité
		{100, domain.EUR, domain.USD, "2026-06-01", 110},     // direct
		{110, domain.USD, domain.EUR, "2026-06-01", 100},     // inverse
		{100, domain.EUR, domain.USD, "2026-06-02", 110},     // forward-fill
		{100, "GBP", domain.EUR, "2026-06-03", 130.0 / 1.12}, // croisé par USD
	} {
		got, err := c.Convert(tc.amount, tc.from, tc.to, mustDate(tc.at))
		if err != nil {
			t.Fatalf("Convert(%v %s→%s): %v", tc.amount, tc.from, tc.to, err)
		}
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("Convert(%v %s→%s @%s) = %v, attendu %v", tc.amount, tc.from, tc.to, tc.at, got, tc.want)
		}
	}
}

func TestConvertMissingRate(t *testing.T) {
	c := testFX()
	_, err := c.Convert(100, "JPY", domain.EUR, mustDate("2026-06-01"))
	if err == nil || !strings.Contains(err.Error(), "JPY") {
		t.Fatalf("err = %v", err)
	}
}
