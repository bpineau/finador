// Package portfolio replays the ledger and values any scope of the patrimoine.
// It depends on domain only: prices come from the Book's market cache, currency
// conversion through the FX interface.
package portfolio

import (
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
			continue // survente = erreur de saisie : jamais de position négative
		}
		acc, errA := b.Account(string(k.acc))
		asset, errB := b.Asset(string(k.asset))
		if errA != nil || errB != nil {
			continue // référence orpheline : ignorée, le ledger reste la vérité
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
		return decimal.Zero // survente = erreur de saisie : jamais de position négative
	}
	return q
}

// CashTracked reports whether the account's cash is tracked: any pure-cash
// Statement, Deposit or Withdraw makes it so. Otherwise trades are treated
// as external flows (spec §3) and the account carries no cash.
func CashTracked(b *domain.Book, acc domain.AccountID) bool {
	for _, t := range b.Transactions {
		if t.Account != acc || t.Asset != "" {
			continue
		}
		switch t.Kind {
		case domain.Statement, domain.Deposit, domain.Withdraw:
			return true
		}
	}
	return false
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
