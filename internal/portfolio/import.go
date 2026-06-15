package portfolio

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// ImportCSV reads header-mapped transactions: date, kind, account, asset,
// quantity, price, amount, currency, group, note — in any column order.
// Unknown accounts and assets are created on the fly; lines whose content
// hash is already in the book are skipped.
func ImportCSV(b *domain.Book, r io.Reader) (added, skipped int, err error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return 0, 0, fmt.Errorf("CSV header: %w", err)
	}
	col := map[string]int{}
	for i, name := range header {
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for line := 2; ; line++ {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return added, skipped, err
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		tx, err := rowToTx(b, get)
		if err != nil {
			return added, skipped, fmt.Errorf("line %d: %w", line, err)
		}
		if b.HasImportHash(tx.ImportHash) {
			skipped++
			continue
		}
		b.Add(tx)
		added++
	}
	return added, skipped, nil
}

func rowToTx(b *domain.Book, get func(string) string) (domain.Transaction, error) {
	var zero domain.Transaction
	date, err := domain.ParseDate(get("date"))
	if err != nil {
		return zero, err
	}
	kind, err := domain.ParseTxKind(get("kind"))
	if err != nil {
		return zero, err
	}
	acc, err := ResolveAccount(b, get("account"))
	if err != nil {
		return zero, err
	}
	ccy, err := domain.CurrencyOr(get("currency"), acc.Currency)
	if err != nil {
		return zero, err
	}

	tx := domain.Transaction{Date: date, Account: acc.ID, Kind: kind, Note: get("note")}

	if ref := get("asset"); ref != "" {
		asset, err := EnsureAsset(b, ref, ccy, get("group"))
		if err != nil {
			return zero, err
		}
		tx.Asset = asset.ID
	}

	qty := decimal.Zero
	if q := get("quantity"); q != "" {
		if qty, err = decimal.NewFromString(q); err != nil {
			return zero, fmt.Errorf("invalid quantity %q: %w", q, err)
		}
	}
	tx.Quantity = qty.Abs()

	var amount decimal.Decimal
	switch {
	case get("amount") != "":
		if amount, err = decimal.NewFromString(get("amount")); err != nil {
			return zero, fmt.Errorf("invalid amount %q: %w", get("amount"), err)
		}
	case get("price") != "":
		price, err := decimal.NewFromString(get("price"))
		if err != nil {
			return zero, fmt.Errorf("invalid price %q: %w", get("price"), err)
		}
		amount = price.Mul(tx.Quantity)
	default:
		return zero, errors.New("neither amount nor price")
	}
	tx.Amount = domain.Money{Amount: amount.Abs(), Currency: ccy}
	tx.ImportHash = hashTx(tx)
	return tx, nil
}

// hashTx fingerprints the canonical content of a row, for idempotent re-imports.
// Two genuinely identical operations the same day must differ by their note.
func hashTx(t domain.Transaction) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		t.Date.String(), t.Kind.String(), string(t.Account), string(t.Asset),
		t.Quantity.String(), t.Amount.Amount.String(), string(t.Amount.Currency), t.Note,
	}, "|")))
	return hex.EncodeToString(sum[:8])
}

// ResolveAccount resolves an account reference; ambiguity propagates and
// unknown accounts are rejected with an actionable error (accounts must be
// declared explicitly with `finador account add`).
func ResolveAccount(b *domain.Book, ref string) (*domain.Account, error) {
	if ref == "" {
		return nil, errors.New("empty account column")
	}
	acc, err := b.Account(ref)
	if err == nil {
		return acc, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err // ambiguity: don't mask it
	}
	return nil, fmt.Errorf("unknown account %q — declare it first with `finador account add %q`", ref, ref)
}

// EnsureAsset resolves an asset reference or creates a security with the
// reference as ticker; ambiguity always propagates instead of creating.
func EnsureAsset(b *domain.Book, ref string, ccy domain.Currency, group string) (*domain.Asset, error) {
	if asset, err := b.Asset(ref); err == nil {
		return asset, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err // ambiguity: don't mask it by creating
	}
	asset := &domain.Asset{ID: domain.AssetID(domain.Slugify(ref)), Kind: domain.Security,
		Name: ref, Ticker: ref, Currency: ccy, Group: group}
	return asset, b.AddAsset(asset)
}
