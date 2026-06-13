package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func txCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tx",
		Short:   "List and edit ledger transactions",
		Example: "  finador tx list --account \"PEA BforBank\"",
	}
	cmd.AddCommand(txList(a), txEdit(a), txRm(a))
	return cmd
}

func txList(a *app) *cobra.Command {
	var account, asset, kind string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List transactions",
		Example: "  finador tx list --account \"PEA BforBank\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			b := f.Book

			var accID domain.AccountID
			if account != "" {
				acc, err := b.Account(account)
				if err != nil {
					return err
				}
				accID = acc.ID
			}
			var assetID domain.AssetID
			if asset != "" {
				as, err := b.Asset(asset)
				if err != nil {
					return err
				}
				assetID = as.ID
			}
			var k domain.TxKind
			if kind != "" {
				if k, err = domain.ParseTxKind(kind); err != nil {
					return err
				}
			}

			txs := slices.Clone(b.Transactions)
			slices.SortStableFunc(txs, func(x, y *domain.Transaction) int {
				if c := x.Date.Time().Compare(y.Date.Time()); c != 0 {
					return c
				}
				return strings.Compare(string(x.ID), string(y.ID))
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDATE\tTYPE\tACCOUNT\tASSET\tQTY\tAMOUNT\tNOTE")
			for _, t := range txs {
				if accID != "" && t.Account != accID ||
					assetID != "" && t.Asset != assetID ||
					k != 0 && t.Kind != k {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					t.ID, t.Date, t.Kind, txAccountName(b, t.Account), txAssetName(b, t.Asset),
					t.Quantity, t.Amount, t.Note)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "filter by account")
	cmd.Flags().StringVar(&asset, "asset", "", "filter by asset")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by type (buy, sell, statement…)")
	return cmd
}

func txEdit(a *app) *cobra.Command {
	var date, account, asset, qty, total, note, kind string
	cmd := &cobra.Command{
		Use:     "edit <id>",
		Short:   "Edit the fields passed as flags, leave the others unchanged",
		Example: "  finador tx edit 8x3k --qty 100 --total 4567.80",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				tx, err := b.ResolveTx(args[0])
				if err != nil {
					return err
				}
				if date != "" {
					if tx.Date, err = domain.ParseDate(date); err != nil {
						return err
					}
				}
				if account != "" {
					acc, err := b.Account(account)
					if err != nil {
						return err
					}
					tx.Account = acc.ID
				}
				if asset != "" {
					as, err := b.Asset(asset)
					if err != nil {
						return err
					}
					tx.Asset = as.ID
				}
				if kind != "" {
					if tx.Kind, err = domain.ParseTxKind(kind); err != nil {
						return err
					}
				}
				if qty != "" {
					q, err := decimal.NewFromString(qty)
					if err != nil {
						return fmt.Errorf("invalid quantity %q: %w", qty, err)
					}
					tx.Quantity = q.Abs()
				}
				if total != "" {
					m, err := decimal.NewFromString(total)
					if err != nil {
						return fmt.Errorf("invalid amount %q: %w", total, err)
					}
					tx.Amount.Amount = m.Abs()
				}
				if cmd.Flags().Changed("note") {
					tx.Note = note
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s %s qty=%s %s\n",
					tx.ID, tx.Date, tx.Kind, tx.Quantity, tx.Amount)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "new date YYYY-MM-DD")
	cmd.Flags().StringVar(&account, "account", "", "new account")
	cmd.Flags().StringVar(&asset, "asset", "", "new asset")
	cmd.Flags().StringVar(&qty, "qty", "", "new quantity")
	cmd.Flags().StringVar(&total, "total", "", "new total amount")
	cmd.Flags().StringVar(&note, "note", "", "new note")
	cmd.Flags().StringVar(&kind, "kind", "", "new type")
	return cmd
}

func txRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>",
		Short:   "Delete a transaction",
		Example: "  finador tx rm 8x3k",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				tx, err := b.ResolveTx(args[0])
				if err != nil {
					return err
				}
				if err := b.RemoveTx(tx.ID); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Transaction %s deleted\n", tx.ID)
				return nil
			})
		},
	}
}

// txAccountName resolves an account id to its display name, falling back to the
// raw id when the account is unknown (a stored reference can outlive a removal).
func txAccountName(b *domain.Book, id domain.AccountID) string {
	if acc, err := b.Account(string(id)); err == nil {
		return acc.Name
	}
	return string(id)
}

// txAssetName resolves an asset id to its display name (empty for cash lines),
// falling back to the raw id when the asset is unknown.
func txAssetName(b *domain.Book, id domain.AssetID) string {
	if id == "" {
		return ""
	}
	if asset, err := b.Asset(string(id)); err == nil {
		return asset.Name
	}
	return string(id)
}
