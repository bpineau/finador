package portfolio

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// TestFuzzValueSeriesEndpoint sweeps randomized ledgers against the one
// invariant the two valuation engines share: the last point of Series() is
// what Value() reports at that date, gross and net, for every scope.
//
// The named tests pin the conventions; this pins the AGREEMENT, which is what
// silently rots. Seeds deliberately pile several records on the same four
// days, mix currencies, envelopes, tax rules, properties and securities, and
// leave trades unpriced: every divergence found so far lived in exactly that
// corner (see D39, D41). It runs in about a second, so it stays in the gate.
func TestFuzzValueSeriesEndpoint(t *testing.T) {
	kinds := []domain.TxKind{domain.Buy, domain.Sell, domain.Dividend, domain.Fee,
		domain.Deposit, domain.Withdraw, domain.Statement}
	rules := []string{"none", "gains:31.4%", "value:20%"}
	fx := fxStub{rates: map[string]float64{"USD→EUR": 0.9, "EUR→USD": 1 / 0.9}}
	ccys := []domain.Currency{domain.EUR, domain.USD}
	fails := 0
	for seed := 0; seed < 20000 && fails < 6; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		b := domain.NewBook()
		var accIDs []domain.AccountID
		for i := 0; i < 1+rng.Intn(2); i++ {
			rule, _ := domain.ParseTaxRule(rules[rng.Intn(len(rules))])
			id := domain.AccountID(fmt.Sprintf("a%d", i))
			if err := b.AddAccount(&domain.Account{ID: id, Name: string(id),
				Currency: ccys[rng.Intn(2)], Tax: rule}); err != nil {
				t.Fatal(err)
			}
			accIDs = append(accIDs, id)
		}
		var assetIDs []domain.AssetID
		for i := 0; i < 1+rng.Intn(2); i++ {
			kind := domain.Security
			if rng.Intn(4) == 0 {
				kind = domain.Property
			}
			id := domain.AssetID(fmt.Sprintf("x%d", i))
			if err := b.AddAsset(&domain.Asset{ID: id, Kind: kind, Name: string(id),
				Currency: ccys[rng.Intn(2)], Group: "g"}); err != nil {
				t.Fatal(err)
			}
			assetIDs = append(assetIDs, id)
		}
		for i := 0; i < 1+rng.Intn(10); i++ {
			k := kinds[rng.Intn(len(kinds))]
			asset := domain.AssetID("")
			if k != domain.Deposit && k != domain.Withdraw && rng.Intn(5) > 0 {
				asset = assetIDs[rng.Intn(len(assetIDs))]
			}
			tx := domain.Transaction{
				ID:      domain.TxID(fmt.Sprintf("t%02d", i)),
				Date:    mustDate(fmt.Sprintf("2026-02-%02d", 1+rng.Intn(4))), // same-day collisions on purpose
				Account: accIDs[rng.Intn(len(accIDs))], Asset: asset, Kind: k,
				Quantity: decimal.NewFromInt(int64(1 + rng.Intn(9))),
				Amount:   domain.Money{Amount: decimal.NewFromInt(int64(100 * (1 + rng.Intn(9)))), Currency: ccys[rng.Intn(2)]},
			}
			b.Transactions = append(b.Transactions, &tx)
		}
		for _, id := range assetIDs {
			if rng.Intn(2) == 0 {
				b.Market.Price(id).Merge([]domain.PricePoint{
					{Date: mustDate("2026-02-02"), Close: 10 + float64(rng.Intn(50))},
				})
			}
		}

		scopes := []Scope{{Kind: All}, {Kind: ByGroup, Group: "g"}}
		for _, id := range accIDs {
			acc, err := b.Account(string(id))
			if err != nil {
				t.Fatal(err)
			}
			scopes = append(scopes, AccountScope(acc))
		}
		at := mustDate("2026-02-10")
		for _, sc := range scopes {
			want, err := Value(b, sc, at, domain.EUR, fx)
			if err != nil {
				continue
			}
			res, err := Series(b, sc, mustDate("2026-02-01"), at, domain.EUR, fx)
			if err != nil {
				continue
			}
			last := res.Points[len(res.Points)-1]
			if d := last.Gross - want.Gross; d > 0.01 || d < -0.01 {
				fails++
				t.Errorf("seed %d scope %v GROSS: Value %v vs Series %v\n%s", seed, sc.Kind, want.Gross, last.Gross, dumpBook(b))
			}
			if d := last.Net - want.Net; d > 0.01 || d < -0.01 {
				fails++
				t.Errorf("seed %d scope %v NET: Value %v vs Series %v\n%s", seed, sc.Kind, want.Net, last.Net, dumpBook(b))
			}
		}
	}
}

func dumpBook(b *domain.Book) string {
	s := ""
	for _, a := range b.Accounts {
		s += fmt.Sprintf("  acct %s ccy=%s tax=%s\n", a.ID, a.Currency, a.Tax)
	}
	for _, a := range b.Assets {
		s += fmt.Sprintf("  asset %s kind=%s ccy=%s\n", a.ID, a.Kind, a.Currency)
	}
	for _, t := range Sorted(b) {
		s += fmt.Sprintf("  %s %s acc=%s %s asset=%q qty=%s amt=%s\n",
			t.ID, t.Date, t.Account, t.Kind, t.Asset, t.Quantity, t.Amount)
	}
	for _, a := range b.Assets {
		for _, p := range b.Market.Price(a.ID).Points {
			s += fmt.Sprintf("  px %s %s %v\n", a.ID, p.Date, p.Close)
		}
	}
	return s
}
