package portfolio

import (
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

func mustDate(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eur(s string) domain.Money {
	return domain.Money{Amount: dec(s), Currency: domain.EUR}
}

// fixture: PEA with cw8 (2 buys, 1 sell); CTO with cw8 too (multi-account);
// Livret (pure cash); an "untracked" account with no pure-cash movement at all.
func sampleBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	for _, acc := range []*domain.Account{
		{ID: "pea", Name: "PEA", Currency: domain.EUR},
		{ID: "cto", Name: "CTO", Currency: domain.EUR},
		{ID: "livret", Name: "Livret", Currency: domain.EUR},
	} {
		if err := b.AddAccount(acc); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions/monde"}); err != nil {
		t.Fatal(err)
	}
	txs := []domain.Transaction{
		{Date: mustDate("2026-01-10"), Account: "pea", Kind: domain.Deposit, Amount: eur("10000")},
		{Date: mustDate("2026-01-15"), Account: "pea", Asset: "cw8", Kind: domain.Buy, Quantity: dec("10"), Amount: eur("5000")},
		{Date: mustDate("2026-02-15"), Account: "pea", Asset: "cw8", Kind: domain.Buy, Quantity: dec("5"), Amount: eur("2750")},
		{Date: mustDate("2026-03-15"), Account: "pea", Asset: "cw8", Kind: domain.Sell, Quantity: dec("3"), Amount: eur("1800")},
		{Date: mustDate("2026-01-20"), Account: "cto", Asset: "cw8", Kind: domain.Buy, Quantity: dec("2"), Amount: eur("1100")},
		{Date: mustDate("2026-01-05"), Account: "livret", Kind: domain.Statement, Amount: eur("12000")},
	}
	for _, tx := range txs {
		b.Add(tx)
	}
	return b
}

func TestHoldings(t *testing.T) {
	b := sampleBook(t)
	hs := Holdings(b, mustDate("2026-12-31"))
	if len(hs) != 2 {
		t.Fatalf("holdings = %d, attendu 2 (pea/cw8 et cto/cw8)", len(hs))
	}
	if !Quantity(b, "pea", "cw8", mustDate("2026-12-31")).Equal(dec("12")) {
		t.Errorf("qté pea = %s", Quantity(b, "pea", "cw8", mustDate("2026-12-31")))
	}
	// at an earlier date, the replay stops there
	if !Quantity(b, "pea", "cw8", mustDate("2026-02-01")).Equal(dec("10")) {
		t.Errorf("qté pea au 1er févr = %s", Quantity(b, "pea", "cw8", mustDate("2026-02-01")))
	}
	if !Quantity(b, "cto", "cw8", mustDate("2026-12-31")).Equal(dec("2")) {
		t.Errorf("qté cto = %s", Quantity(b, "cto", "cw8", mustDate("2026-12-31")))
	}
}

func TestHoldingsDropsZeroPositions(t *testing.T) {
	b := sampleBook(t)
	b.Add(domain.Transaction{Date: mustDate("2026-04-01"), Account: "cto", Asset: "cw8",
		Kind: domain.Sell, Quantity: dec("2"), Amount: eur("1200")})
	hs := Holdings(b, mustDate("2026-12-31"))
	if len(hs) != 1 || hs[0].Account.ID != "pea" {
		t.Fatalf("holdings = %+v", hs)
	}
}

func TestOverSellClampsToZero(t *testing.T) {
	b := sampleBook(t)
	b.Add(domain.Transaction{Date: mustDate("2026-05-01"), Account: "cto", Asset: "cw8",
		Kind: domain.Sell, Quantity: dec("99"), Amount: eur("999")})
	if q := Quantity(b, "cto", "cw8", mustDate("2026-12-31")); !q.IsZero() {
		t.Errorf("survente: qté = %s, attendu 0", q)
	}
	for _, h := range Holdings(b, mustDate("2026-12-31")) {
		if h.Account.ID == "cto" {
			t.Errorf("position survendue présente: %+v", h)
		}
	}
}

func TestCashTracked(t *testing.T) {
	b := sampleBook(t)
	for acc, want := range map[domain.AccountID]bool{
		"pea":    true,  // has a Deposit
		"livret": true,  // has a cash Statement
		"cto":    false, // only has trades
	} {
		if got := CashTracked(b, acc); got != want {
			t.Errorf("CashTracked(%s) = %v, attendu %v", acc, got, want)
		}
	}
	// a Statement ON AN ASSET (property estimate) does not make cash tracked
	if err := b.AddAsset(&domain.Asset{ID: "maison", Kind: domain.Property, Name: "Maison", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "cto", Asset: "maison",
		Kind: domain.Statement, Amount: eur("450000")})
	if CashTracked(b, "cto") {
		t.Error("un Statement d'actif ne doit pas activer le suivi du cash")
	}
}
