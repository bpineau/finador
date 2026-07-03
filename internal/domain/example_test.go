package domain_test

import (
	"fmt"

	"finador/internal/domain"
)

func ExampleSlugify() {
	fmt.Println(domain.Slugify("PEA Zephyr"))
	fmt.Println(domain.Slugify("Maison à Rénover"))
	// Output:
	// pea-zephyr
	// maison-a-renover
}

func ExampleParseTaxRule() {
	pea, _ := domain.ParseTaxRule("gains:18.6%")
	per, _ := domain.ParseTaxRule("value:20")
	none, _ := domain.ParseTaxRule("none")
	fmt.Println(pea, per, none)
	// Output:
	// gains:18.6% value:20% none
}

// Any reference resolves an asset: ticker, alias, name - or a unique prefix
// of any of them when every exact tier fails.
func ExampleBook_Asset() {
	b := domain.NewBook()
	_ = b.AddAsset(&domain.Asset{
		ID: "cw8-pa", Kind: domain.Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", Aliases: []string{"world"}, Currency: domain.EUR,
	})
	_ = b.AddAsset(&domain.Asset{
		ID: "aapl", Kind: domain.Security, Name: "Apple Inc.",
		Ticker: "AAPL", Currency: domain.USD,
	})

	for _, ref := range []string{"cw8.pa", "world", "Apple Inc.", "amu"} {
		a, _ := b.Asset(ref)
		fmt.Printf("%-10s -> %s\n", ref, a.ID)
	}
	// Output:
	// cw8.pa     -> cw8-pa
	// world      -> cw8-pa
	// Apple Inc. -> aapl
	// amu        -> cw8-pa
}

// At forward-fills: a week-end or holiday reads the last known close.
func ExamplePriceSeries_At() {
	day := func(s string) domain.Date { d, _ := domain.ParseDate(s); return d }
	var s domain.PriceSeries
	s.Merge([]domain.PricePoint{
		{Date: day("2026-06-04"), Close: 100}, // Thursday
		{Date: day("2026-06-05"), Close: 102}, // Friday
	})

	close, at, _ := s.At(day("2026-06-07")) // Sunday
	fmt.Printf("%.0f (from %s)\n", close, at)
	// Output:
	// 102 (from 2026-06-05)
}
