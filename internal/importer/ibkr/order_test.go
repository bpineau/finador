package ibkr

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

// The statement below holds one day's round trip on one security: 20 bought
// at 100, then 10 sold at 150, in that order. Replay order is (date, id)
// (FORMAT.md 4.2), so the two lines are separated by their ids alone - and
// they are minted microseconds apart, inside one millisecond tick.
//
// Read in the statement's own order the average-cost basis of what is left is
// 1000 (half of 2000), the 10 remaining shares are worth 1500, and the
// envelope's 31.4 % on the 500 of gain is 157.00. Read the other way round
// the sell precedes any position, so it moves nothing, and the basis stays at
// the full 2000 the buy paid: the position then reads as a 500 loss and the
// latent tax as 0.00.
//
// Before the generator was made monotonic the two orders came out of a coin
// toss - 97 runs at 0 EUR and 103 at 157 EUR over 200 identical imports - and
// whichever one a run drew was then frozen in the ledger. The statement's own
// order is the faithful one: it is the order the events happened in, and the
// only one the broker reports.
const samedayFixture = "testdata/sameday.csv"

const samedayLatentTax = 157.00

func samedayBook(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	tax, err := domain.ParseTaxRule("gains:31.4%")
	if err != nil {
		t.Fatal(err)
	}
	acc := &domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR, Tax: tax}
	if err := b.AddAccount(acc); err != nil {
		t.Fatal(err)
	}
	asset := &domain.Asset{
		ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", ISIN: "LU1681043599", Currency: domain.EUR,
	}
	if err := b.AddAsset(asset); err != nil {
		t.Fatal(err)
	}
	b.Market.Price(asset.ID).Points = []domain.PricePoint{{Date: mustDate(t, "2026-03-31"), Close: 150}}
	return b
}

// sameCurrency is the whole FX this fixture needs: it is EUR throughout.
type sameCurrency struct{}

func (sameCurrency) Convert(amount float64, from, to domain.Currency, _ domain.Date) (float64, error) {
	if from != to {
		return 0, fmt.Errorf("unexpected conversion %s -> %s", from, to)
	}
	return amount, nil
}

func mustDate(t *testing.T, s string) domain.Date {
	t.Helper()
	d, err := domain.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// importSameday runs the fixture into a fresh book and returns its latent tax.
func importSameday(t *testing.T) (float64, *domain.Book) {
	t.Helper()
	raw, err := os.ReadFile(samedayFixture)
	if err != nil {
		t.Fatal(err)
	}
	b := samedayBook(t)
	res, err := Import(b, strings.NewReader(string(raw)), Options{Account: "CTO Meridia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 {
		t.Fatalf("added=%d, want 2 (%s)", res.Added, res.Summary())
	}
	acc, err := portfolio.ResolveAccount(b, "CTO Meridia")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := b.Asset("CW8.PA")
	if err != nil {
		t.Fatal(err)
	}
	// The position's own rule: value beyond the average-cost basis. That is
	// the number the replay order moves; the envelope rule nets buys against
	// sells and is order-blind by construction.
	val, err := portfolio.Value(b, portfolio.PairScope(acc, asset), mustDate(t, "2026-03-31"), domain.EUR, sameCurrency{})
	if err != nil {
		t.Fatal(err)
	}
	return val.Tax, b
}

// TestImportOrderIsTheStatementOrder is the regression the monotonic id
// generator exists for: the same statement, imported over and over, must give
// the same latent tax - the one its own line order implies.
func TestImportOrderIsTheStatementOrder(t *testing.T) {
	for run := range 200 {
		tax, b := importSameday(t)
		if tax != samedayLatentTax {
			t.Fatalf("run %d: latent tax = %.2f EUR, want %.2f", run, tax, samedayLatentTax)
		}
		// The ids themselves must carry that order, since replay reads them.
		txs := portfolio.Sorted(b)
		if len(txs) != 2 {
			t.Fatalf("run %d: %d transactions, want 2", run, len(txs))
		}
		if txs[0].Kind != domain.Buy || txs[1].Kind != domain.Sell {
			t.Fatalf("run %d: replay order is %s then %s, want buy then sell", run, txs[0].Kind, txs[1].Kind)
		}
	}
}
