package domain

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
	// ID, nom exact, et nom insensible à la casse matchent tous
	for _, ref := range []string{"pea-zephyr", "PEA Zephyr", "pea zephyr", "PEA-ZEPHYR"} {
		if _, err := b.Account(ref); err != nil {
			t.Errorf("Account(%q): %v", ref, err)
		}
	}
	if _, err := b.Account("zephyr"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(zephyr): %v, attendu ErrNotFound", err)
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
	if tx.ID == "" {
		t.Fatal("Add did not assign an ID")
	}
	tx2 := b.Add(Transaction{Date: d, Account: "pea-zephyr", Kind: Deposit,
		Amount: Money{Amount: decimal.NewFromInt(1000), Currency: EUR}})
	if tx2.ID == "" || tx2.ID == tx.ID {
		t.Fatalf("second ID empty or collides with first: %q vs %q", tx2.ID, tx.ID)
	}
	if err := b.RemoveTx(tx.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tx(tx.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Tx after delete: %v", err)
	}
	if _, err := b.Tx(tx2.ID); err != nil {
		t.Errorf("Tx(tx2): %v", err)
	}
}

func TestResolveTx(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	tx := b.Add(Transaction{Date: d, Account: "pea-zephyr", Kind: Deposit,
		Amount: Money{Amount: decimal.NewFromInt(1000), Currency: EUR}})

	got, err := b.ResolveTx(string(tx.ID))
	if err != nil || got.ID != tx.ID {
		t.Fatalf("exact match: got %v, %v", got, err)
	}
	got, err = b.ResolveTx(string(tx.ID)[:6]) // unique prefix
	if err != nil || got.ID != tx.ID {
		t.Fatalf("prefix match: got %v, %v", got, err)
	}
	if _, err := b.ResolveTx("zzzzzzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no match: %v", err)
	}
	if _, err := b.ResolveTx(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty ref: %v", err)
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
}

func TestHasImportHash(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	b.Add(Transaction{Date: d, Account: "pea-zephyr", Kind: Deposit,
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
	b.Add(Transaction{Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
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

func TestAccountAliases(t *testing.T) {
	b := sampleBook(t)
	pea, _ := b.Account("pea-zephyr")
	pea.Aliases = []string{"pea", "bfb"}
	if err := b.CheckAccountRefs(pea); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"PEA", "bfb", "BFB"} {
		if acc, err := b.Account(ref); err != nil || acc.ID != "pea-zephyr" {
			t.Errorf("Account(%q) = %v, %v", ref, acc, err)
		}
	}
	// collision d'alias avec un nom existant → ErrDuplicate
	if err := b.AddAccount(&Account{ID: "autre", Name: "Autre", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	autre, _ := b.Account("autre")
	autre.Aliases = []string{"PEA Zephyr"}
	if err := b.CheckAccountRefs(autre); !errors.Is(err, ErrDuplicate) {
		t.Errorf("collision = %v", err)
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
		{ID: "pea-zephyr", Name: "PEA Zephyr", Currency: EUR},
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
	if err := b.AddAsset(&Asset{ID: "vizr", Kind: Security, Name: "Vizor Inc.",
		Ticker: "VIZR", Currency: USD}); err != nil {
		t.Fatal(err)
	}

	// préfixe unique d'ID → résout
	if a, err := b.Asset("cw8"); err != nil || a.ID != "cw8-pa" {
		t.Errorf("Asset(cw8) = %v, %v", a, err)
	}
	// préfixe unique de nom → résout
	if a, err := b.Asset("datad"); err != nil || a.ID != "vizr" {
		t.Errorf("Asset(datad) = %v, %v", a, err)
	}
	// préfixe de compte
	if acc, err := b.Account("pea"); err != nil || acc.ID != "pea-zephyr" {
		t.Errorf("Account(pea) = %v, %v", acc, err)
	}
	// préfixe ambigu → erreur qui liste les candidats
	_, err := b.Account("pe")
	if !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "pea-zephyr") ||
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

func TestLabels(t *testing.T) {
	b := sampleBook(t)
	const acc = AccountID("pea-zephyr")
	const asset = AssetID("cw8")

	l1 := &Label{ID: LabelID(NewID()), Account: acc, Asset: asset, Name: "retraite"}
	if err := b.AddLabel(l1); err != nil {
		t.Fatal(err)
	}
	l2 := &Label{ID: LabelID(NewID()), Account: acc, Asset: asset, Name: "core"}
	if err := b.AddLabel(l2); err != nil {
		t.Fatal(err)
	}

	// Exact duplicate (case-insensitive on name, same pair) is rejected.
	dup := &Label{ID: LabelID(NewID()), Account: acc, Asset: asset, Name: "RETRAITE"}
	if err := b.AddLabel(dup); !errors.Is(err, ErrDuplicate) {
		t.Errorf("AddLabel(duplicate) = %v, want ErrDuplicate", err)
	}
	// Same name on a different pair is fine.
	if err := b.AddAccount(&Account{ID: "cto", Name: "CTO", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddLabel(&Label{ID: LabelID(NewID()), Account: "cto", Asset: asset, Name: "retraite"}); err != nil {
		t.Errorf("same name on a different pair should be allowed: %v", err)
	}

	// LabelsFor returns the names of a pair, sorted.
	got := b.LabelsFor(acc, asset)
	if want := []string{"core", "retraite"}; !slices.Equal(got, want) {
		t.Errorf("LabelsFor = %v, want %v", got, want)
	}

	// ResolveLabel: exact id and unique prefix.
	if l, err := b.ResolveLabel(string(l1.ID)); err != nil || l.ID != l1.ID {
		t.Errorf("ResolveLabel(exact) = %v, %v", l, err)
	}
	if l, err := b.ResolveLabel(string(l1.ID)[:len(l1.ID)-1]); err != nil || l.ID != l1.ID {
		t.Errorf("ResolveLabel(prefix) = %v, %v", l, err)
	}
	if _, err := b.ResolveLabel("zzzzzzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveLabel(absent) = %v, want ErrNotFound", err)
	}
	if _, err := b.ResolveLabel(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveLabel(empty) = %v, want ErrNotFound", err)
	}

	// RemoveLabel: removes by id, ErrNotFound when absent.
	if err := b.RemoveLabel(l1.ID); err != nil {
		t.Fatal(err)
	}
	if got := b.LabelsFor(acc, asset); !slices.Equal(got, []string{"core"}) {
		t.Errorf("after RemoveLabel, LabelsFor = %v, want [core]", got)
	}
	if err := b.RemoveLabel(l1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveLabel(absent) = %v, want ErrNotFound", err)
	}
}
