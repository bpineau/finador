package cli

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// TestWriteScriptEmitsReplayOrder: the dump is a rebuild recipe, and the
// rebuilt copy mints a fresh id per emitted command, in emission order. So
// the script must emit the ledger the way the engine READS it - (date, id) -
// and not the order the records happen to sit in the file, which a merge from
// another device reshuffles freely. On a same-day round trip the difference is
// the average-cost basis, hence the latent tax.
func TestWriteScriptEmitsReplayOrder(t *testing.T) {
	b := domain.NewBook()
	acc := &domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR}
	if err := b.AddAccount(acc); err != nil {
		t.Fatal(err)
	}
	asset := &domain.Asset{ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World", Ticker: "CW8.PA", Currency: domain.EUR}
	if err := b.AddAsset(asset); err != nil {
		t.Fatal(err)
	}
	day, err := domain.ParseDate("2026-03-02")
	if err != nil {
		t.Fatal(err)
	}
	money := func(v string) domain.Money {
		return domain.Money{Amount: decimal.RequireFromString(v), Currency: domain.EUR}
	}
	// Ids say buy-then-sell; the slice says the opposite, as a file merged
	// from another device can.
	buy := &domain.Transaction{
		ID: "00000000000000000000001", Date: day, Account: acc.ID, Asset: asset.ID,
		Kind: domain.Buy, Quantity: decimal.RequireFromString("20"), Amount: money("2000"),
	}
	sell := &domain.Transaction{
		ID: "00000000000000000000002", Date: day, Account: acc.ID, Asset: asset.ID,
		Kind: domain.Sell, Quantity: decimal.RequireFromString("10"), Amount: money("1500"),
	}
	b.Transactions = []*domain.Transaction{sell, buy}

	var out strings.Builder
	if err := writeScript(&out, b); err != nil {
		t.Fatal(err)
	}
	buyAt := strings.Index(out.String(), "finador asset buy")
	sellAt := strings.Index(out.String(), "finador asset sell")
	if buyAt < 0 || sellAt < 0 {
		t.Fatalf("script does not carry both trades:\n%s", out.String())
	}
	if buyAt > sellAt {
		t.Fatalf("script emits the sell before the buy, replaying in the wrong order:\n%s", out.String())
	}
}
