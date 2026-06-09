package cli

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func importCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "import <fichier.csv>",
		Short: "Importe des transactions (colonnes par en-tête ; ré-import sans doublon)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			var added, skipped int
			// mutate n'écrit le fichier que si tout l'import a réussi.
			if err := a.mutate(func(b *domain.Book) error {
				added, skipped, err = importCSV(b, file)
				return err
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d importée(s), %d ignorée(s) (doublons)\n", added, skipped)
			return nil
		},
	}
}

// importCSV reads header-mapped transactions: date, kind, account, asset,
// quantity, price, amount, currency, group, note — in any column order.
// Unknown accounts and assets are created on the fly; lines whose content
// hash is already in the book are skipped.
func importCSV(b *domain.Book, r io.Reader) (added, skipped int, err error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return 0, 0, fmt.Errorf("en-tête CSV: %w", err)
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
			return added, skipped, fmt.Errorf("ligne %d: %w", line, err)
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
	acc, err := ensureAccount(b, get("account"), get("currency"))
	if err != nil {
		return zero, err
	}
	ccy, err := currencyOr(get("currency"), acc.Currency)
	if err != nil {
		return zero, err
	}

	tx := domain.Transaction{Date: date, Account: acc.ID, Kind: kind, Note: get("note")}

	if ref := get("asset"); ref != "" {
		asset, err := ensureAsset(b, ref, ccy, get("group"))
		if err != nil {
			return zero, err
		}
		tx.Asset = asset.ID
	}

	qty := decimal.Zero
	if q := get("quantity"); q != "" {
		if qty, err = decimal.NewFromString(q); err != nil {
			return zero, fmt.Errorf("quantité %q: %w", q, err)
		}
	}
	tx.Quantity = qty.Abs()

	var amount decimal.Decimal
	switch {
	case get("amount") != "":
		if amount, err = decimal.NewFromString(get("amount")); err != nil {
			return zero, fmt.Errorf("montant %q: %w", get("amount"), err)
		}
	case get("price") != "":
		price, err := decimal.NewFromString(get("price"))
		if err != nil {
			return zero, fmt.Errorf("prix %q: %w", get("price"), err)
		}
		amount = price.Mul(tx.Quantity)
	default:
		return zero, errors.New("ni amount ni price")
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

func ensureAccount(b *domain.Book, ref, ccy string) (*domain.Account, error) {
	if ref == "" {
		return nil, errors.New("colonne account vide")
	}
	if acc, err := b.Account(ref); err == nil {
		return acc, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err // ambiguïté : ne pas masquer en création
	}
	parsedCcy, err := currencyOr(ccy, domain.EUR)
	if err != nil {
		return nil, err
	}
	acc := &domain.Account{ID: domain.AccountID(domain.Slugify(ref)), Name: ref,
		Currency: parsedCcy}
	return acc, b.AddAccount(acc)
}

func ensureAsset(b *domain.Book, ref string, ccy domain.Currency, group string) (*domain.Asset, error) {
	if asset, err := b.Asset(ref); err == nil {
		return asset, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err // ambiguïté : ne pas masquer en création
	}
	asset := &domain.Asset{ID: domain.AssetID(domain.Slugify(ref)), Kind: domain.Security,
		Name: ref, Ticker: ref, Currency: ccy, Group: group}
	return asset, b.AddAsset(asset)
}
