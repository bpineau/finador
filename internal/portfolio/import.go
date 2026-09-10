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
// quantity, price, amount, currency, group, note - in any column order.
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
		if AddImported(b, tx) {
			added++
		} else {
			skipped++
		}
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
	return nil, fmt.Errorf("unknown account %q - declare it first with `finador account add %q`", ref, ref)
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

// AddImported appends t unless the book already carries its importHash, and
// reports whether it was added. Every importer - the reference CSV one and
// the broker-statement ones - goes through it: it is the single place the
// dedup rule of FORMAT.md 4.5 is applied.
func AddImported(b *domain.Book, t domain.Transaction) (added bool) {
	if t.ImportHash != "" && b.HasImportHash(t.ImportHash) {
		return false
	}
	b.Add(t)
	return true
}

// ManualMatches returns the live hand-entered transactions - the ones with no
// importHash - that already record the same outside-world event as t, a line
// just read from a broker statement.
//
// It exists because the dedup rule of FORMAT.md 4.5 compares fingerprints, and
// a transaction typed by hand carries none: without this guard, a statement
// covering trades already entered by hand books every one of them twice. The
// caller decides what to do with a match - leave the line out, or adopt the
// manual transaction with [Adopt].
//
// The rule is deliberately narrow, and a broker's own rounding is the only
// slack: same account, same kind, same day, same asset (both empty for pure
// cash), plus an amount within 0.5 % of the larger of the two (a commission
// folded into a trade, or a fee rounded to the cent, moves the total by far
// less). A buy or a sell must in addition carry the exact same quantity: it
// is the one field a statement and a human agree on to the digit.
//
// Several matches mean the ledger cannot say which line is which; the caller
// is expected to report the ambiguity rather than pick one. A transaction that
// already carries a DIFFERENT importHash is never a match: it is another
// broker event that happens to look alike, and the statement line is its own.
func ManualMatches(b *domain.Book, t domain.Transaction) []*domain.Transaction {
	var hits []*domain.Transaction
	for _, m := range b.Transactions {
		if m.ImportHash == "" && sameEvent(*m, t) {
			hits = append(hits, m)
		}
	}
	return hits
}

// amountTolerance is the relative slack ManualMatches allows on an amount:
// 0.5 %, which covers a commission or a rounding, never a different trade.
var amountTolerance = decimal.New(5, -3)

// sameEvent reports whether a hand-entered transaction m and a statement line
// t describe the same event. See ManualMatches for the rule and its why.
func sameEvent(m, t domain.Transaction) bool {
	if m.Account != t.Account || m.Kind != t.Kind || m.Date != t.Date || m.Asset != t.Asset {
		return false
	}
	if (t.Kind == domain.Buy || t.Kind == domain.Sell) && !m.Quantity.Equal(t.Quantity) {
		return false
	}
	return closeAmounts(m.Amount, t.Amount)
}

// closeAmounts compares two amounts of the same currency within
// amountTolerance, relative to the larger of the two. The comparison
// cross-multiplies rather than dividing: no precision to choose, so the 0.5 %
// edge is exact and inclusive.
func closeAmounts(a, b domain.Money) bool {
	if a.Currency != b.Currency {
		return false
	}
	larger := decimal.Max(a.Amount.Abs(), b.Amount.Abs())
	return a.Amount.Sub(b.Amount).Abs().LessThanOrEqual(larger.Mul(amountTolerance))
}

// Adopt gives a hand-entered transaction the external reference of the
// statement line that describes the same event, and changes NOTHING else -
// date, quantity, amount and note stay as they were typed.
//
// This is the other half of the guard: the typed transaction becomes the
// imported one, so replaying the same statement skips its line for good (the
// dedup rule now has a fingerprint to compare), while the numbers the user
// checked are left alone. FORMAT.md 4.5 already allows it: an edit carries the
// importHash through, and the field is opaque, writer-chosen and never parsed.
// Read the other way round, a transaction may acquire one.
func Adopt(m *domain.Transaction, importHash string) { m.ImportHash = importHash }
