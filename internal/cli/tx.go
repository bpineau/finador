package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func txCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "tx", Short: "List and edit ledger transactions"}
	cmd.AddCommand(txList(a), txEdit(a), txRm(a))
	return cmd
}

func parseTxID(s string) (domain.TxID, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid transaction id %q", s)
	}
	return domain.TxID(id), nil
}

func txList(a *app) *cobra.Command {
	var account, asset, kind string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List transactions",
		Args:  cobra.NoArgs,
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
				return cmp.Compare(x.ID, y.ID)
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDATE\tTYPE\tACCOUNT\tASSET\tQTY\tAMOUNT\tNOTE")
			for _, t := range txs {
				if accID != "" && t.Account != accID ||
					assetID != "" && t.Asset != assetID ||
					k != 0 && t.Kind != k {
					continue
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					t.ID, t.Date, t.Kind, t.Account, t.Asset, t.Quantity, t.Amount, t.Note)
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
		Use:   "edit <id>",
		Short: "Edit the fields passed as flags, leave the others unchanged",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTxID(args[0])
			if err != nil {
				return err
			}
			return a.mutate(func(b *domain.Book) error {
				tx, err := b.Tx(id)
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
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s qty=%s %s\n",
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
		Use:   "rm <id>",
		Short: "Delete a transaction",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTxID(args[0])
			if err != nil {
				return err
			}
			return a.mutate(func(b *domain.Book) error {
				if err := b.RemoveTx(id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Transaction %d deleted\n", id)
				return nil
			})
		},
	}
}
