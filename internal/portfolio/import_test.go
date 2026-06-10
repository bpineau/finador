package portfolio

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

const sampleCSV = `date,kind,account,asset,quantity,price,amount,currency,group,note
2026-01-15,buy,PEA BforBank,CW8.PA,10,550,,EUR,actions/monde,premier achat
2026-01-20,deposit,PEA BforBank,,,,5000,EUR,,
2026-02-01,statement,Livret A,,,,12000,EUR,,
`

func TestImportCSV(t *testing.T) {
	b := domain.NewBook()
	added, skipped, err := ImportCSV(b, strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d", added, skipped)
	}
	// comptes et actif créés à la volée
	if _, err := b.Account("pea-bforbank"); err != nil {
		t.Error(err)
	}
	if _, err := b.Account("Livret A"); err != nil {
		t.Error(err)
	}
	asset, err := b.Asset("CW8.PA")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Group != "actions/monde" {
		t.Errorf("group = %q", asset.Group)
	}
	// price unitaire × quantité → montant total
	if got := b.Transactions[0].Amount.Amount.String(); got != "5500" {
		t.Errorf("amount = %s", got)
	}
}

func TestImportCSVIdempotent(t *testing.T) {
	b := domain.NewBook()
	if _, _, err := ImportCSV(b, strings.NewReader(sampleCSV)); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := ImportCSV(b, strings.NewReader(sampleCSV))
	if err != nil || added != 0 || skipped != 3 {
		t.Fatalf("ré-import: added=%d skipped=%d err=%v", added, skipped, err)
	}
}

func TestImportCSVBadLine(t *testing.T) {
	b := domain.NewBook()
	bad := "date,kind,account,amount,currency\n2026-13-45,buy,X,100,EUR\n"
	if _, _, err := ImportCSV(b, strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "ligne 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportPropagatesAmbiguity(t *testing.T) {
	b := domain.NewBook()
	// Injection directe des deux actifs pour simuler un livre legacy/corrompu
	// avec des alias en collision — AddAsset les refuserait désormais.
	b.Assets = append(b.Assets,
		&domain.Asset{ID: "a1", Kind: domain.Security, Name: "Un", Aliases: []string{"dup"}, Currency: domain.EUR},
		&domain.Asset{ID: "a2", Kind: domain.Security, Name: "Deux", Aliases: []string{"dup"}, Currency: domain.EUR},
	)
	csv := "date,kind,account,asset,quantity,price,currency\n2026-01-15,buy,PEA,dup,1,10,EUR\n"
	if _, _, err := ImportCSV(b, strings.NewReader(csv)); err == nil || !strings.Contains(err.Error(), "ambiguë") {
		t.Fatalf("err = %v, attendu ambiguïté propagée", err)
	}
}
