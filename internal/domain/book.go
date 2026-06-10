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
	Market       MarketData        `json:"market"`
	LastTxID     TxID              `json:"lastTxId"`
}

func NewBook() *Book { return &Book{Config: map[string]string{}} }

// AddAccount rejects an ID or name that collides exactly (case-insensitive)
// with an existing account — exact field comparison avoids false duplicates
// from the prefix tier.
func (b *Book) AddAccount(a *Account) error {
	if a.ID == "" {
		return fmt.Errorf("account %q: empty identifier", a.Name)
	}
	for _, other := range b.Accounts {
		if strings.EqualFold(string(other.ID), string(a.ID)) {
			return fmt.Errorf("account %q: %w", string(a.ID), ErrDuplicate)
		}
		if strings.EqualFold(other.Name, a.Name) {
			return fmt.Errorf("account %q: %w", a.Name, ErrDuplicate)
		}
	}
	b.Accounts = append(b.Accounts, a)
	return nil
}

// Account resolves a reference: ID first, then free-form name — both case-insensitive.
func (b *Book) Account(ref string) (*Account, error) {
	return resolve(ref, "account", b.Accounts,
		func(a *Account) []string { return []string{string(a.ID)} },
		func(a *Account) []string { return []string{a.Name} },
	)
}

// AddAsset rejects any reference (ID, ticker, ISIN, alias, name) that already
// collides exactly (case-insensitive) with another asset — exact field
// comparison avoids false duplicates from the prefix tier.
func (b *Book) AddAsset(a *Asset) error {
	if a.ID == "" {
		return fmt.Errorf("asset %q: empty identifier", a.Name)
	}
	if err := b.CheckAssetRefs(a); err != nil {
		return err
	}
	b.Assets = append(b.Assets, a)
	return nil
}

// CheckAssetRefs verifies that none of a's references collides exactly with
// another asset — adding or editing must not poison resolution.
func (b *Book) CheckAssetRefs(a *Asset) error {
	refs := append([]string{string(a.ID), a.Ticker, a.ISIN, a.Name}, a.Aliases...)
	for _, other := range b.Assets {
		if other.ID == a.ID {
			continue
		}
		others := append([]string{string(other.ID), other.Ticker, other.ISIN, other.Name}, other.Aliases...)
		for _, r := range refs {
			if r == "" {
				continue
			}
			for _, o := range others {
				if o != "" && strings.EqualFold(r, o) {
					return fmt.Errorf("reference %q already used by %s: %w", r, other.ID, ErrDuplicate)
				}
			}
		}
	}
	return nil
}

// Asset resolves a reference, trying tiers in order:
// ID, ticker, ISIN, alias, then name — all case-insensitive.
func (b *Book) Asset(ref string) (*Asset, error) {
	return resolve(ref, "asset", b.Assets,
		func(a *Asset) []string { return []string{string(a.ID)} },
		func(a *Asset) []string { return []string{a.Ticker} },
		func(a *Asset) []string { return []string{a.ISIN} },
		func(a *Asset) []string { return a.Aliases },
		func(a *Asset) []string { return []string{a.Name} },
	)
}

// resolve returns the single item matching ref on the first tier that yields
// any match; several matches on the same tier is an ambiguity. When every
// exact tier fails, a unique case-insensitive PREFIX of any reference wins —
// « add cw8 » without remembering the full id.
func resolve[T any](ref, what string, items []*T, tiers ...func(*T) []string) (*T, error) {
	if ref == "" {
		return nil, fmt.Errorf("%s (empty reference): %w", what, ErrNotFound)
	}
	for _, tier := range tiers {
		matches := lo.Filter(items, func(it *T, _ int) bool {
			return lo.SomeBy(tier(it), func(s string) bool {
				return s != "" && strings.EqualFold(s, ref)
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
	// tier préfixe : sur TOUTES les références confondues
	low := strings.ToLower(ref)
	var hits []*T
	var hitIDs []string
	for _, it := range items {
		for _, tier := range tiers {
			found := lo.SomeBy(tier(it), func(s string) bool {
				return s != "" && strings.HasPrefix(strings.ToLower(s), low)
			})
			if found {
				hits = append(hits, it)
				hitIDs = append(hitIDs, tiers[0](it)[0]) // l'ID, premier tier
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("%s %q: %w", what, ref, ErrNotFound)
	default:
		return nil, fmt.Errorf("%s %q (candidates: %s): %w",
			what, ref, strings.Join(hitIDs, ", "), ErrAmbiguous)
	}
}

// RemoveAsset deletes an unreferenced asset and purges its market cache.
func (b *Book) RemoveAsset(ref string) error {
	asset, err := b.Asset(ref)
	if err != nil {
		return err
	}
	for _, t := range b.Transactions {
		if t.Asset == asset.ID {
			return fmt.Errorf("asset %s is referenced by transaction %d — delete its transactions first (finador tx list --asset %s)",
				asset.ID, t.ID, asset.ID)
		}
	}
	b.Assets = lo.Reject(b.Assets, func(a *Asset, _ int) bool { return a.ID == asset.ID })
	delete(b.Market.Prices, asset.ID)
	delete(b.Market.Dividends, asset.ID)
	return nil
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
