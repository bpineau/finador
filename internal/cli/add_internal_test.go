package cli

import (
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// TestTradeTailRejectsNegativePrice: a ledger amount is always positive, and
// "@-25" is not the documented negative-quantity shortcut for a sell.
func TestTradeTailRejectsNegativePrice(t *testing.T) {
	if _, _, err := parseTradeTail([]string{"@-25", "2026-01-03"}, decimal.NewFromInt(1)); err == nil {
		t.Fatal("a negative unit price was accepted")
	}
	total, _, err := parseTradeTail([]string{"@25"}, decimal.NewFromInt(-2))
	if err != nil || !total.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("@25 on -2 = %v, %v; want 50", total, err)
	}
}

func TestWhatIfRejectsNonFinite(t *testing.T) {
	b := newBookWithAsset(t)
	for _, p := range []string{"cw8=NaN", "cw8=Inf", "cw8=-1"} {
		if _, err := parseWhatIf(b, []string{p}); err == nil {
			t.Errorf("--what-if %s accepted", p)
		}
	}
}

func newBookWithAsset(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	return b
}
