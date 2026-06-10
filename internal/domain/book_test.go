package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func sampleBook(t *testing.T) *Book {
	t.Helper()
	b := NewBook()
	if err := b.AddAccount(&Account{ID: "pea-bforbank", Name: "PEA BforBank", Currency: EUR}); err != nil {
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
	// ID, nom exact, et nom insensible à la casse matchent tous
	for _, ref := range []string{"pea-bforbank", "PEA BforBank", "pea bforbank", "PEA-BFORBANK"} {
		if _, err := b.Account(ref); err != nil {
			t.Errorf("Account(%q): %v", ref, err)
		}
	}
	if _, err := b.Account("bforbank"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(bforbank): %v, attendu ErrNotFound", err)
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

func TestAddAssetRejectsAnyCollidingReference(t *testing.T) {
	b := sampleBook(t)
	for _, dup := range []*Asset{
		{ID: "autre1", Kind: Security, Name: "X", Ticker: "cw8.pa", Currency: EUR},           // ticker, casse différente
		{ID: "autre2", Kind: Security, Name: "amundi msci world", Currency: EUR},             // nom
		{ID: "autre3", Kind: Security, Name: "Y", Aliases: []string{"WORLD"}, Currency: EUR}, // alias
		{ID: "autre4", Kind: Security, Name: "Z", ISIN: "LU1681043599", Currency: EUR},       // isin
	} {
		if err := b.AddAsset(dup); !errors.Is(err, ErrDuplicate) {
			t.Errorf("AddAsset(%s) = %v, attendu ErrDuplicate", dup.ID, err)
		}
	}
}

func TestResolveAmbiguous(t *testing.T) {
	b := sampleBook(t)
	// Injection directe pour simuler un livre corrompu/legacy :
	// AddAsset rejette désormais toute collision d'alias.
	b.Assets = append(b.Assets, &Asset{ID: "cw8-cto", Kind: Security, Name: "CW8 bis",
		Aliases: []string{"world"}, Currency: EUR})
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
	if err := b.AddAccount(&Account{ID: "pea-bforbank", Name: "Autre"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("ID dupliqué: %v", err)
	}
	if err := b.AddAccount(&Account{ID: "autre", Name: "PEA BforBank"}); !errors.Is(err, ErrDuplicate) {
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
	tx := b.Add(Transaction{Date: d, Account: "pea-bforbank", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(10),
		Amount:   Money{Amount: decimal.NewFromInt(5500), Currency: EUR}})
	if tx.ID != 1 {
		t.Fatalf("premier ID = %d", tx.ID)
	}
	if tx2 := b.Add(Transaction{Date: d, Account: "pea-bforbank", Kind: Deposit,
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
	b.Add(Transaction{Date: d, Account: "pea-bforbank", Asset: "cw8", Kind: Buy,
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

func TestHasImportHash(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	b.Add(Transaction{Date: d, Account: "pea-bforbank", Kind: Deposit,
		Amount:     Money{Amount: decimal.NewFromInt(100), Currency: EUR},
		ImportHash: "abcd1234"})
	if !b.HasImportHash("abcd1234") {
		t.Error("hash présent non détecté")
	}
	if b.HasImportHash("ffff0000") || b.HasImportHash("") {
		t.Error("hash absent ou vide ne doit jamais matcher")
	}
}

func TestRemoveAsset(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	b.Add(Transaction{Date: d, Account: "pea-bforbank", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(1), Amount: Money{Amount: decimal.NewFromInt(550), Currency: EUR}})
	if err := b.RemoveAsset("cw8"); err == nil {
		t.Fatal("RemoveAsset d'un actif référencé aurait dû échouer")
	}
	if err := b.AddAsset(&Asset{ID: "libre", Kind: Security, Name: "Libre", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	b.Market.Price("libre").Merge([]PricePoint{{Date: d, Close: 1}})
	if err := b.RemoveAsset("libre"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Asset("libre"); !errors.Is(err, ErrNotFound) {
		t.Error("l'actif devrait avoir disparu")
	}
	if b.Market.Prices["libre"] != nil {
		t.Error("le cache de prix devrait être purgé")
	}
}

func TestCheckAssetRefsCollision(t *testing.T) {
	b := sampleBook(t)
	if err := b.AddAsset(&Asset{ID: "autre", Kind: Security, Name: "Autre", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	autre, _ := b.Asset("autre")
	autre.Aliases = []string{"CW8.PA"} // collision exacte avec le ticker de cw8
	if err := b.CheckAssetRefs(autre); !errors.Is(err, ErrDuplicate) {
		t.Errorf("collision non détectée: %v", err)
	}
	autre.Aliases = []string{"unique-2026"}
	if err := b.CheckAssetRefs(autre); err != nil {
		t.Errorf("faux positif: %v", err)
	}
}

func TestParsePercent(t *testing.T) {
	for in, want := range map[string]float64{"15%": 0.15, "0%": 0, "30": 0.30} {
		got, err := ParsePercent(in)
		if err != nil || got != want {
			t.Errorf("ParsePercent(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"abc", "-5%", "150%"} {
		if _, err := ParsePercent(bad); err == nil {
			t.Errorf("ParsePercent(%q) accepté", bad)
		}
	}
}

func TestResolveUniquePrefix(t *testing.T) {
	b := NewBook()
	for _, a := range []*Account{
		{ID: "pea-bforbank", Name: "PEA BforBank", Currency: EUR},
		{ID: "per-linxea", Name: "PER Linxea", Currency: EUR},
	} {
		if err := b.AddAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AddAsset(&Asset{ID: "cw8-pa", Kind: Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&Asset{ID: "ddog", Kind: Security, Name: "Datadog Inc.",
		Ticker: "DDOG", Currency: USD}); err != nil {
		t.Fatal(err)
	}

	// préfixe unique d'ID → résout
	if a, err := b.Asset("cw8"); err != nil || a.ID != "cw8-pa" {
		t.Errorf("Asset(cw8) = %v, %v", a, err)
	}
	// préfixe unique de nom → résout
	if a, err := b.Asset("datad"); err != nil || a.ID != "ddog" {
		t.Errorf("Asset(datad) = %v, %v", a, err)
	}
	// préfixe de compte
	if acc, err := b.Account("pea"); err != nil || acc.ID != "pea-bforbank" {
		t.Errorf("Account(pea) = %v, %v", acc, err)
	}
	// préfixe ambigu → erreur qui liste les candidats
	_, err := b.Account("pe")
	if !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "pea-bforbank") ||
		!strings.Contains(err.Error(), "per-linxea") {
		t.Errorf("Account(pe) = %v, attendu ambiguïté listant les candidats", err)
	}
	// l'exact gagne toujours sur le préfixe : un actif ID "dd" exact
	if err := b.AddAsset(&Asset{ID: "dd", Kind: Security, Name: "Doubledown", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if a, err := b.Asset("dd"); err != nil || a.ID != "dd" {
		t.Errorf("Asset(dd) = %v, %v — l'exact doit gagner", a, err)
	}
	// introuvable reste introuvable
	if _, err := b.Asset("zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Asset(zz) = %v", err)
	}
}
