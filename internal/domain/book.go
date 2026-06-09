package domain

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
)

// Book is the whole persisted state: exactly what the encrypted file contains.
type Book struct {
	Accounts     []*Account        `json:"accounts"`
	Assets       []*Asset          `json:"assets"`
	Transactions []*Transaction    `json:"transactions"`
	Config       map[string]string `json:"config,omitempty"`
	LastTxID     TxID              `json:"lastTxId"`
}

func NewBook() *Book { return &Book{Config: map[string]string{}} }

func (b *Book) AddAccount(a *Account) error {
	if a.ID == "" {
		return fmt.Errorf("compte %q: identifiant vide: %w", a.Name, ErrNotFound)
	}
	for _, ref := range []string{string(a.ID), a.Name} {
		if _, err := b.Account(ref); err == nil {
			return fmt.Errorf("compte %q: %w", ref, ErrDuplicate)
		}
	}
	b.Accounts = append(b.Accounts, a)
	return nil
}

// Account resolves a reference: ID first (exact), then free-form name (exact).
func (b *Book) Account(ref string) (*Account, error) {
	return resolveExact(ref, "compte", b.Accounts,
		func(a *Account) []string { return []string{string(a.ID)} },
		func(a *Account) []string { return []string{a.Name} },
	)
}

func (b *Book) AddAsset(a *Asset) error {
	if a.ID == "" {
		return fmt.Errorf("actif %q: identifiant vide: %w", a.Name, ErrNotFound)
	}
	if _, err := b.Asset(string(a.ID)); err == nil {
		return fmt.Errorf("actif %q: %w", a.ID, ErrDuplicate)
	}
	b.Assets = append(b.Assets, a)
	return nil
}

// Asset resolves a reference, trying tiers in order:
// ID, ticker, ISIN, alias, then name — all case-insensitive.
func (b *Book) Asset(ref string) (*Asset, error) {
	return resolve(ref, "actif", b.Assets,
		func(a *Asset) []string { return []string{string(a.ID)} },
		func(a *Asset) []string { return []string{a.Ticker} },
		func(a *Asset) []string { return []string{a.ISIN} },
		func(a *Asset) []string { return a.Aliases },
		func(a *Asset) []string { return []string{a.Name} },
	)
}

// resolve returns the single item matching ref on the first tier that yields
// any match (case-insensitive); several matches on the same tier is an ambiguity.
func resolve[T any](ref, what string, items []*T, tiers ...func(*T) []string) (*T, error) {
	return resolveWith(ref, what, items, strings.EqualFold, tiers...)
}

// resolveExact is like resolve but uses exact (case-sensitive) string matching.
func resolveExact[T any](ref, what string, items []*T, tiers ...func(*T) []string) (*T, error) {
	return resolveWith(ref, what, items, func(a, b string) bool { return a == b }, tiers...)
}

// resolveWith is the generic resolution engine parameterised by a comparison function.
func resolveWith[T any](ref, what string, items []*T, eq func(string, string) bool, tiers ...func(*T) []string) (*T, error) {
	if ref == "" {
		return nil, fmt.Errorf("%s (référence vide): %w", what, ErrNotFound)
	}
	for _, tier := range tiers {
		matches := lo.Filter(items, func(it *T, _ int) bool {
			return lo.SomeBy(tier(it), func(s string) bool {
				return s != "" && eq(s, ref)
			})
		})
		switch len(matches) {
		case 0: // tier suivant
		case 1:
			return matches[0], nil
		default:
			return nil, fmt.Errorf("%s %q: %w", what, ref, ErrAmbiguous)
		}
	}
	return nil, fmt.Errorf("%s %q: %w", what, ref, ErrNotFound)
}

// Add appends t to the ledger with a fresh ID and returns the stored transaction.
func (b *Book) Add(t Transaction) *Transaction {
	b.LastTxID++
	t.ID = b.LastTxID
	stored := &t
	b.Transactions = append(b.Transactions, stored)
	return stored
}

func (b *Book) Tx(id TxID) (*Transaction, error) {
	tx, ok := lo.Find(b.Transactions, func(t *Transaction) bool { return t.ID == id })
	if !ok {
		return nil, fmt.Errorf("transaction %d: %w", id, ErrNotFound)
	}
	return tx, nil
}

func (b *Book) RemoveTx(id TxID) error {
	if _, err := b.Tx(id); err != nil {
		return err
	}
	b.Transactions = lo.Reject(b.Transactions, func(t *Transaction, _ int) bool { return t.ID == id })
	return nil
}

func (b *Book) HasImportHash(h string) bool {
	return h != "" && lo.SomeBy(b.Transactions, func(t *Transaction) bool { return t.ImportHash == h })
}
