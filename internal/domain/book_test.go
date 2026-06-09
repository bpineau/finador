package domain

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func sampleBook(t *testing.T) *Book {
	t.Helper()
	b := NewBook()
	if err := b.AddAccount(&Account{ID: "pea-zephyr", Name: "PEA Zephyr", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&Asset{
		ID: "cw8", Kind: Security, Name: "Amundi MSCI World", Ticker: "CW8.PA",
		ISIN: "LU1681043599", Aliases: []string{"world"}, Currency: EUR, Group: "actions/monde",
	}); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResolveAccount(t *testing.T) {
	b := sampleBook(t)
	for _, ref := range []string{"pea-zephyr", "PEA Zephyr", "pea zephyr"} {
		if _, err := b.Account(ref); (ref == "pea zephyr") == (err == nil) {
			// "pea zephyr" ne matche ni l'ID ni le nom exact → ErrNotFound
			t.Errorf("Account(%q): err=%v", ref, err)
		}
	}
	if _, err := b.Account("inconnu"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(inconnu): %v, attendu ErrNotFound", err)
	}
}

func TestResolveAssetTiers(t *testing.T) {
	b := sampleBook(t)
	for _, ref := range []string{"cw8", "CW8.PA", "lu1681043599", "WORLD", "amundi msci world"} {
		if _, err := b.Asset(ref); err != nil {
			t.Errorf("Asset(%q): %v", ref, err)
		}
	}
	if _, err := b.Asset(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Asset(\"\"): %v, attendu ErrNotFound", err)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	b := sampleBook(t)
	if err := b.AddAsset(&Asset{ID: "cw8-cto", Kind: Security, Name: "CW8 bis",
		Aliases: []string{"world"}, Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Asset("world"); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("Asset(world): %v, attendu ErrAmbiguous", err)
	}
	// le tier ID gagne avant que le tier alias ne devienne ambigu
	if _, err := b.Asset("cw8"); err != nil {
		t.Errorf("Asset(cw8): %v", err)
	}
}

func TestDuplicates(t *testing.T) {
	b := sampleBook(t)
	if err := b.AddAccount(&Account{ID: "pea-zephyr", Name: "Autre"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("ID dupliqué: %v", err)
	}
	if err := b.AddAccount(&Account{ID: "autre", Name: "PEA Zephyr"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("nom dupliqué: %v", err)
	}
}

func TestRejectEmptyIDs(t *testing.T) {
	b := NewBook()
	if err := b.AddAccount(&Account{ID: "", Name: "///"}); err == nil {
		t.Error("AddAccount avec ID vide aurait dû échouer")
	}
	if err := b.AddAsset(&Asset{ID: "", Kind: Security, Name: "日本語"}); err == nil {
		t.Error("AddAsset avec ID vide aurait dû échouer")
	}
}

func TestLedger(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	tx := b.Add(Transaction{Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(10),
		Amount:   Money{Amount: decimal.NewFromInt(5500), Currency: EUR}})
	if tx.ID != 1 {
		t.Fatalf("premier ID = %d", tx.ID)
	}
	if tx2 := b.Add(Transaction{Date: d, Account: "pea-zephyr", Kind: Deposit,
		Amount: Money{Amount: decimal.NewFromInt(1000), Currency: EUR}}); tx2.ID != 2 {
		t.Fatalf("second ID = %d", tx2.ID)
	}
	if err := b.RemoveTx(1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tx(1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Tx(1) après suppression: %v", err)
	}
	if _, err := b.Tx(2); err != nil {
		t.Errorf("Tx(2): %v", err)
	}
}

func TestBookJSONRoundTrip(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	b.Add(Transaction{Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(10),
		Amount:   Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}})
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	back := NewBook()
	if err := json.Unmarshal(raw, back); err != nil {
		t.Fatal(err)
	}
	if len(back.Accounts) != 1 || len(back.Assets) != 1 || len(back.Transactions) != 1 {
		t.Fatalf("roundtrip incomplet: %+v", back)
	}
	got := back.Transactions[0]
	if !got.Amount.Amount.Equal(decimal.RequireFromString("5500.50")) || got.Kind != Buy {
		t.Fatalf("transaction altérée: %+v", got)
	}
	if back.LastTxID != 1 {
		t.Fatalf("LastTxID = %d", back.LastTxID)
	}
}
