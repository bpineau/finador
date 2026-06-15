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

// addAccount is a test helper that pre-declares an account.
func addAccount(t *testing.T, b *domain.Book, name string, ccy domain.Currency) *domain.Account {
	t.Helper()
	acc := &domain.Account{ID: domain.AccountID(domain.Slugify(name)), Name: name, Currency: ccy}
	if err := b.AddAccount(acc); err != nil {
		t.Fatalf("addAccount %q: %v", name, err)
	}
	return acc
}

func TestImportCSV(t *testing.T) {
	b := domain.NewBook()
	// Accounts must now be declared before import.
	addAccount(t, b, "PEA BforBank", domain.EUR)
	addAccount(t, b, "Livret A", domain.EUR)

	added, skipped, err := ImportCSV(b, strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d", added, skipped)
	}
	// accounts and asset created on the fly
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
	// unit price × quantity → total amount
	if got := b.Transactions[0].Amount.Amount.String(); got != "5500" {
		t.Errorf("amount = %s", got)
	}
}

func TestImportCSVIdempotent(t *testing.T) {
	b := domain.NewBook()
	addAccount(t, b, "PEA BforBank", domain.EUR)
	addAccount(t, b, "Livret A", domain.EUR)

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
	addAccount(t, b, "X", domain.EUR)
	bad := "date,kind,account,amount,currency\n2026-13-45,buy,X,100,EUR\n"
	if _, _, err := ImportCSV(b, strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportCSVUnknownAccount(t *testing.T) {
	b := domain.NewBook()
	// No accounts pre-declared: import must fail with actionable error.
	csv := "date,kind,account,amount,currency\n2026-01-15,deposit,Mystery Bank,100,EUR\n"
	_, _, err := ImportCSV(b, strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for unknown account, got nil")
	}
	if !strings.Contains(err.Error(), "unknown account") {
		t.Fatalf("expected 'unknown account' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "finador account add") {
		t.Fatalf("expected hint 'finador account add' in error, got: %v", err)
	}
	// No account should have been created.
	if len(b.Accounts) != 0 {
		t.Errorf("account was created on the fly: %v", b.Accounts)
	}
}

func TestResolveAccount(t *testing.T) {
	b := domain.NewBook()
	// ResolveAccount errors on unknown account.
	_, err := ResolveAccount(b, "My Bank")
	if err == nil || !strings.Contains(err.Error(), "unknown account") {
		t.Fatalf("expected unknown account error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "finador account add") {
		t.Fatalf("expected hint in error, got: %v", err)
	}

	// Pre-declare it; now it resolves.
	acc := &domain.Account{ID: "mybank", Name: "My Bank", Currency: domain.EUR}
	if err := b.AddAccount(acc); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveAccount(b, "My Bank")
	if err != nil {
		t.Fatalf("ResolveAccount after add: %v", err)
	}
	if resolved.ID != acc.ID {
		t.Fatalf("resolved wrong account: %v", resolved)
	}

	// Second call also resolves.
	resolved2, err := ResolveAccount(b, "My Bank")
	if err != nil || resolved2.ID != acc.ID {
		t.Fatalf("second resolve = %v, %v", resolved2, err)
	}

	// Empty ref always errors.
	if _, err := ResolveAccount(b, ""); err == nil {
		t.Fatal("expected error on empty ref")
	}
}

func TestEnsureAsset(t *testing.T) {
	b := domain.NewBook()
	// EnsureAsset creates on ErrNotFound
	asset, err := EnsureAsset(b, "NVDA", domain.USD, "tech")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "NVDA" || asset.Kind != domain.Security || asset.Currency != domain.USD {
		t.Fatalf("asset = %+v", asset)
	}
	// second call resolves
	asset2, err := EnsureAsset(b, "NVDA", domain.EUR, "")
	if err != nil || asset2.ID != asset.ID {
		t.Fatalf("second asset resolve = %v, %v", asset2, err)
	}
}

func TestImportPropagatesAmbiguity(t *testing.T) {
	b := domain.NewBook()
	// Pre-declare the account so the asset ambiguity is what we're testing.
	addAccount(t, b, "PEA", domain.EUR)
	// Inject both assets directly to simulate a legacy/corrupted book
	// with colliding aliases — AddAsset would now reject them.
	b.Assets = append(b.Assets,
		&domain.Asset{ID: "a1", Kind: domain.Security, Name: "Un", Aliases: []string{"dup"}, Currency: domain.EUR},
		&domain.Asset{ID: "a2", Kind: domain.Security, Name: "Deux", Aliases: []string{"dup"}, Currency: domain.EUR},
	)
	csv := "date,kind,account,asset,quantity,price,currency\n2026-01-15,buy,PEA,dup,1,10,EUR\n"
	if _, _, err := ImportCSV(b, strings.NewReader(csv)); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, attendu ambiguïté propagée", err)
	}
}
