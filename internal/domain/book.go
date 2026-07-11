package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/samber/lo"
)

// Book is the whole persisted state: exactly what the encrypted file contains.
type Book struct {
	Accounts     []*Account        `json:"accounts"`
	Assets       []*Asset          `json:"assets"`
	Transactions []*Transaction    `json:"transactions"`
	Labels       []*Label          `json:"labels,omitempty"`
	Config       map[string]string `json:"config,omitempty"`
	Market       MarketData        `json:"market"`
}

// NewBook returns an empty Book with its config map ready.
func NewBook() *Book { return &Book{Config: map[string]string{}} }

// DisplayCurrency is the currency values are shown in: the configured "currency"
// if valid, otherwise EUR. Single source for the CLI and web front-ends.
func (b *Book) DisplayCurrency() Currency {
	if c, err := ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return EUR
}

// CurrencyOr parses a user-supplied currency code, an empty string meaning fallback.
func CurrencyOr(s string, fallback Currency) (Currency, error) {
	if s == "" {
		return fallback, nil
	}
	return ParseCurrency(s)
}

// CheckAccountRefs verifies that none of a's references (ID, Name, Aliases)
// collides exactly (case-insensitive) with another account - adding or
// editing must not poison resolution. Self is skipped by pointer identity:
// an edit must mutate the account in place (every caller does), and a NEW
// account whose ID collides with an existing one is then still caught.
func (b *Book) CheckAccountRefs(a *Account) error {
	refs := append([]string{string(a.ID), a.Name}, a.Aliases...)
	for _, other := range b.Accounts {
		if other == a { // same pointer: skip self (edit scenario)
			continue
		}
		others := append([]string{string(other.ID), other.Name}, other.Aliases...)
		if r, hit := refsCollide(refs, others); hit {
			return fmt.Errorf("reference %q already used by %s: %w", r, other.ID, ErrDuplicate)
		}
	}
	return nil
}

// refsCollide reports the first non-empty reference colliding, EqualFold.
func refsCollide(refs, others []string) (string, bool) {
	for _, r := range refs {
		if r == "" {
			continue
		}
		for _, o := range others {
			if o != "" && strings.EqualFold(r, o) {
				return r, true
			}
		}
	}
	return "", false
}

// AddAccount rejects an ID or name that collides exactly (case-insensitive)
// with an existing account - delegates to CheckAccountRefs.
func (b *Book) AddAccount(a *Account) error {
	if a.ID == "" {
		return fmt.Errorf("account %q: empty identifier", a.Name)
	}
	if err := b.CheckAccountRefs(a); err != nil {
		return err
	}
	b.Accounts = append(b.Accounts, a)
	return nil
}

// Account resolves a reference: ID first, then aliases, then free-form name -
// all case-insensitive. Alias tier deliberately outranks name so that
// user-chosen short names always win.
func (b *Book) Account(ref string) (*Account, error) {
	return resolve(ref, "account", b.Accounts,
		func(a *Account) []string { return []string{string(a.ID)} },
		func(a *Account) []string { return a.Aliases },
		func(a *Account) []string { return []string{a.Name} },
	)
}

// AddAsset rejects any reference (ID, ticker, ISIN, alias, name) that already
// collides exactly (case-insensitive) with another asset - exact field
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
// another asset - adding or editing must not poison resolution. Self is
// skipped by pointer identity, like CheckAccountRefs: skipping by ID would
// let a NEW asset silently duplicate an existing asset's ID.
func (b *Book) CheckAssetRefs(a *Asset) error {
	refs := append([]string{string(a.ID), a.Ticker, a.ISIN, a.Name}, a.Aliases...)
	for _, other := range b.Assets {
		if other == a { // same pointer: skip self (edit scenario)
			continue
		}
		others := append([]string{string(other.ID), other.Ticker, other.ISIN, other.Name}, other.Aliases...)
		if r, hit := refsCollide(refs, others); hit {
			return fmt.Errorf("reference %q already used by %s: %w", r, other.ID, ErrDuplicate)
		}
	}
	return nil
}

// Asset resolves a reference, trying tiers in order:
// ID, ticker, ISIN, alias, then name - all case-insensitive.
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
// exact tier fails, a unique case-insensitive PREFIX of any reference wins -
// "add cw8" without remembering the full id.
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
		case 0: // next tier
		case 1:
			return matches[0], nil
		default:
			return nil, fmt.Errorf("%s %q: %w", what, ref, ErrAmbiguous)
		}
	}
	// prefix tier: across ALL references combined
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
				hitIDs = append(hitIDs, tiers[0](it)[0]) // the ID, first tier
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

// RemoveAccount deletes an account that has no transactions referencing it.
func (b *Book) RemoveAccount(ref string) error {
	acc, err := b.Account(ref)
	if err != nil {
		return err
	}
	for _, t := range b.Transactions {
		if t.Account == acc.ID {
			return fmt.Errorf("account %s is referenced by transaction %s - delete its transactions first", acc.ID, t.ID)
		}
	}
	b.Accounts = lo.Reject(b.Accounts, func(a *Account, _ int) bool { return a.ID == acc.ID })
	return nil
}

// RemoveAsset deletes an unreferenced asset and purges its market cache.
func (b *Book) RemoveAsset(ref string) error {
	asset, err := b.Asset(ref)
	if err != nil {
		return err
	}
	for _, t := range b.Transactions {
		if t.Asset == asset.ID {
			return fmt.Errorf("asset %s is referenced by transaction %s - delete its transactions first (finador tx list --asset %s)",
				asset.ID, t.ID, asset.ID)
		}
	}
	b.Assets = lo.Reject(b.Assets, func(a *Asset, _ int) bool { return a.ID == asset.ID })
	delete(b.Market.Prices, asset.ID)
	delete(b.Market.Dividends, asset.ID)
	return nil
}

// Add appends t to the ledger with a fresh random ID and returns the stored
// transaction.
func (b *Book) Add(t Transaction) *Transaction {
	t.ID = TxID(NewID())
	stored := &t
	b.Transactions = append(b.Transactions, stored)
	return stored
}

// Tx returns the transaction with exactly this id; ErrNotFound otherwise.
// User-typed references go through ResolveTx (unique-prefix) instead.
func (b *Book) Tx(id TxID) (*Transaction, error) {
	tx, ok := lo.Find(b.Transactions, func(t *Transaction) bool { return t.ID == id })
	if !ok {
		return nil, fmt.Errorf("transaction %s: %w", id, ErrNotFound)
	}
	return tx, nil
}

// ResolveTx returns the transaction whose id equals ref exactly, or - failing
// that - the single transaction whose id has ref as a prefix (like git's short
// SHAs). No match is ErrNotFound; several prefix matches is ErrAmbiguous.
func (b *Book) ResolveTx(ref string) (*Transaction, error) {
	if ref == "" {
		return nil, fmt.Errorf("transaction (empty reference): %w", ErrNotFound)
	}
	if tx, err := b.Tx(TxID(ref)); err == nil {
		return tx, nil
	}
	var hits []*Transaction
	for _, t := range b.Transactions {
		if strings.HasPrefix(string(t.ID), ref) {
			hits = append(hits, t)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("transaction %q: %w", ref, ErrNotFound)
	default:
		ids := lo.Map(hits, func(t *Transaction, _ int) string { return string(t.ID) })
		return nil, fmt.Errorf("transaction %q (candidates: %s): %w", ref, strings.Join(ids, ", "), ErrAmbiguous)
	}
}

// RemoveTx deletes a transaction by exact id; ErrNotFound if absent.
func (b *Book) RemoveTx(id TxID) error {
	if _, err := b.Tx(id); err != nil {
		return err
	}
	b.Transactions = lo.Reject(b.Transactions, func(t *Transaction, _ int) bool { return t.ID == id })
	return nil
}

// HasImportHash reports whether a transaction already carries this CSV-import
// fingerprint - what makes re-importing the same file idempotent.
func (b *Book) HasImportHash(h string) bool {
	return h != "" && lo.SomeBy(b.Transactions, func(t *Transaction) bool { return t.ImportHash == h })
}

// AddLabel appends a label assignment, rejecting an exact duplicate - same
// Account and Asset, and same Name case-insensitively - with ErrDuplicate.
// The caller assigns a fresh NewID().
func (b *Book) AddLabel(l *Label) error {
	if l.ID == "" {
		return fmt.Errorf("label %q: empty identifier", l.Name)
	}
	for _, x := range b.Labels {
		if x.Account == l.Account && x.Asset == l.Asset && strings.EqualFold(x.Name, l.Name) {
			return fmt.Errorf("label %q already on this (account, asset) pair: %w", l.Name, ErrDuplicate)
		}
	}
	b.Labels = append(b.Labels, l)
	return nil
}

// RemoveLabel deletes a label by id; ErrNotFound if absent.
func (b *Book) RemoveLabel(id LabelID) error {
	if !lo.SomeBy(b.Labels, func(l *Label) bool { return l.ID == id }) {
		return fmt.Errorf("label %s: %w", id, ErrNotFound)
	}
	b.Labels = lo.Reject(b.Labels, func(l *Label, _ int) bool { return l.ID == id })
	return nil
}

// ResolveLabel returns the label whose id equals ref exactly, or - failing that
// - the single label whose id has ref as a prefix (like ResolveTx). No match is
// ErrNotFound; several prefix matches is ErrAmbiguous.
func (b *Book) ResolveLabel(ref string) (*Label, error) {
	if ref == "" {
		return nil, fmt.Errorf("label (empty reference): %w", ErrNotFound)
	}
	if l, ok := lo.Find(b.Labels, func(l *Label) bool { return string(l.ID) == ref }); ok {
		return l, nil
	}
	hits := lo.Filter(b.Labels, func(l *Label, _ int) bool {
		return strings.HasPrefix(string(l.ID), ref)
	})
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("label %q: %w", ref, ErrNotFound)
	default:
		ids := lo.Map(hits, func(l *Label, _ int) string { return string(l.ID) })
		return nil, fmt.Errorf("label %q (candidates: %s): %w", ref, strings.Join(ids, ", "), ErrAmbiguous)
	}
}

// LabelsFor returns the label names on a (account, asset) pair, sorted for
// display.
func (b *Book) LabelsFor(account AccountID, asset AssetID) []string {
	var names []string
	for _, l := range b.Labels {
		if l.Account == account && l.Asset == asset {
			names = append(names, l.Name)
		}
	}
	slices.Sort(names)
	return names
}
