package portfolio

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// treeBook: one envelope taxed on its overall gain, holding a winner (bought
// 50, worth 100) and a loser (bought 150, worth 100): no gain overall.
func treeBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	tax, _ := domain.ParseTaxRule("gains:20%")
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia", Currency: domain.EUR, Tax: tax}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		id   domain.AssetID
		ccy  domain.Currency
		cost string
	}{{"win", domain.EUR, "50"}, {"lose", domain.EUR, "150"}} {
		if err := b.AddAsset(&domain.Asset{ID: a.id, Kind: domain.Security, Name: string(a.id), Currency: a.ccy}); err != nil {
			t.Fatal(err)
		}
		b.Add(domain.Transaction{Date: mustDate("2026-01-02"), Account: "cto", Asset: a.id, Kind: domain.Buy,
			Quantity: decimal.NewFromInt(1), Amount: domain.Money{Amount: decimal.RequireFromString(a.cost), Currency: a.ccy}})
		b.Market.Price(a.id).Merge([]domain.PricePoint{{Date: mustDate("2026-01-02"), Close: 100}})
	}
	return b
}

// TestTreeTotalIsExact: the tree's lines are per-position estimates, but its
// total and its envelope follow the per-envelope rule, as `value` does.
func TestTreeTotalIsExact(t *testing.T) {
	b := treeBook(t)
	at := mustDate("2026-01-05")
	tree, err := ScopedTree(b, Scope{Kind: All}, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "tree gross", tree.Gross, 200)
	approx(t, "tree net", tree.Net, 200) // no overall gain, no tax
	approx(t, "envelope net", tree.Envelopes[0].Net, 200)
	if tree.TaxNote == "" {
		t.Error("lines (190) and total (200) differ, yet no note says so")
	}
}

// TestTreeIgnoresPositionsOutOfScope: a USD position with no exchange rate
// cannot fail the tree of an EUR asset that leaves it out.
func TestTreeIgnoresPositionsOutOfScope(t *testing.T) {
	b := treeBook(t)
	if err := b.AddAsset(&domain.Asset{ID: "usd", Kind: domain.Security, Name: "usd", Currency: domain.USD}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-02"), Account: "cto", Asset: "usd", Kind: domain.Buy,
		Quantity: decimal.NewFromInt(1), Amount: domain.Money{Amount: decimal.NewFromInt(100), Currency: domain.USD}})
	b.Market.Price("usd").Merge([]domain.PricePoint{{Date: mustDate("2026-01-02"), Close: 100}})
	win, _ := b.Asset("win")
	tree, err := ScopedTree(b, AssetScope(win), mustDate("2026-01-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatalf("an out-of-scope USD position failed the tree: %v", err)
	}
	approx(t, "tree gross", tree.Gross, 100)
}

// TestCSVCarriesEstimateNote: the downloaded file is read away from the page
// that labelled the estimate, so the label travels in its last column.
func TestCSVCarriesEstimateNote(t *testing.T) {
	var buf bytes.Buffer
	rows := []AssetRow{{Kind: "security", Name: "Fund", Gross: 1100, Net: 1100, Currency: domain.EUR,
		Note: "estimated price"}}
	if err := WriteAssetCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "1100.00,1100.00,EUR,estimated price\n") {
		t.Fatalf("estimate note missing from the CSV:\n%s", buf.String())
	}
}
