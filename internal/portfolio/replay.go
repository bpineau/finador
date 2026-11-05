// Package portfolio replays the ledger and values any scope of the patrimoine.
// It depends on domain only: prices come from the Book's market cache, currency
// conversion through the FX interface.
package portfolio

import (
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// Holding is the replayed quantity of one (account, asset) pair.
type Holding struct {
	Account *domain.Account
	Asset   *domain.Asset
	Qty     decimal.Decimal
}

// Holdings replays Buy/Sell up to asOf and returns the non-zero positions,
// in first-seen ledger order.
func Holdings(b *domain.Book, asOf domain.Date) []Holding {
	type key struct {
		acc   domain.AccountID
		asset domain.AssetID
	}
	qty := map[key]decimal.Decimal{}
	var order []key
	for _, t := range Sorted(b) {
		if asOf.Before(t.Date) || t.Asset == "" {
			continue
		}
		k := key{t.Account, t.Asset}
		switch t.Kind {
		case domain.Buy:
			if _, seen := qty[k]; !seen {
				order = append(order, k)
			}
			qty[k] = qty[k].Add(t.Quantity)
		case domain.Sell:
			if _, seen := qty[k]; !seen {
				order = append(order, k)
			}
			qty[k] = qty[k].Sub(t.Quantity)
		}
	}
	var out []Holding
	for _, k := range order {
		if !qty[k].IsPositive() {
			continue // oversell = input error: never a negative position
		}
		acc, errA := b.Account(string(k.acc))
		asset, errB := b.Asset(string(k.asset))
		if errA != nil || errB != nil {
			continue // orphaned reference: ignored, the ledger stays the truth
		}
		out = append(out, Holding{Account: acc, Asset: asset, Qty: qty[k]})
	}
	return out
}

// Quantity replays the quantity of one asset inside one account at asOf.
func Quantity(b *domain.Book, acc domain.AccountID, asset domain.AssetID, asOf domain.Date) decimal.Decimal {
	q := decimal.Zero
	for _, t := range Sorted(b) {
		if asOf.Before(t.Date) || t.Account != acc || t.Asset != asset {
			continue
		}
		switch t.Kind {
		case domain.Buy:
			q = q.Add(t.Quantity)
		case domain.Sell:
			q = q.Sub(t.Quantity)
		}
	}
	if q.IsNegative() {
		return decimal.Zero // oversell = input error: never a negative position
	}
	return q
}

// Sorted returns the ledger in replay order: (date, id).
func Sorted(b *domain.Book) []*domain.Transaction {
	txs := slices.Clone(b.Transactions)
	slices.SortStableFunc(txs, func(x, y *domain.Transaction) int {
		if c := x.Date.Time().Compare(y.Date.Time()); c != 0 {
			return c
		}
		switch {
		case x.ID < y.ID:
			return -1
		case x.ID > y.ID:
			return 1
		}
		return 0
	})
	return txs
}

// CheckSecurityStatement refuses a valuation statement on a security that the
// account does not hold at that date. A security's quantity comes from its
// buys and sells only: such a statement would be read as the value of zero
// shares, and the holding would silently count for nothing. Buy it first
// (`asset buy`, at cost), then the statement re-prices it per share (D27).
func CheckSecurityStatement(b *domain.Book, acc *domain.Account, asset *domain.Asset, at domain.Date) error {
	if asset.Kind != domain.Security || Quantity(b, acc.ID, asset.ID, at).IsPositive() {
		return nil
	}
	return fmt.Errorf("%s holds no %s on %s: a security's quantity comes from its buys - record it first "+
		"(finador asset buy %q <qty> <total> %s --account %q), then set its value", acc.Name, asset.Name, at, asset.Name, at, acc.Name)
}
