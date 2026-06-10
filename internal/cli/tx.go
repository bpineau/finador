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
	cmd := &cobra.Command{Use: "tx", Short: "Liste et corrige les transactions du ledger"}
	cmd.AddCommand(txList(a), txEdit(a), txRm(a))
	return cmd
}

func parseTxID(s string) (domain.TxID, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("identifiant de transaction %q invalide", s)
	}
	return domain.TxID(id), nil
}

func txList(a *app) *cobra.Command {
	var account, asset, kind string
	cmd := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
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
			fmt.Fprintln(w, "ID\tDATE\tTYPE\tCOMPTE\tACTIF\tQTÉ\tMONTANT\tNOTE")
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
	cmd.Flags().StringVar(&account, "account", "", "filtre par enveloppe")
	cmd.Flags().StringVar(&asset, "asset", "", "filtre par actif")
	cmd.Flags().StringVar(&kind, "kind", "", "filtre par type (buy, sell, statement…)")
	return cmd
}

func txEdit(a *app) *cobra.Command {
	var date, account, asset, qty, total, note, kind string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Corrige les champs passés en flag, laisse les autres intacts",
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
						return fmt.Errorf("quantité %q: %w", qty, err)
					}
					tx.Quantity = q.Abs()
				}
				if total != "" {
					m, err := decimal.NewFromString(total)
					if err != nil {
						return fmt.Errorf("montant %q: %w", total, err)
					}
					tx.Amount.Amount = m.Abs()
				}
				if cmd.Flags().Changed("note") {
					tx.Note = note
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s qté=%s %s\n",
					tx.ID, tx.Date, tx.Kind, tx.Quantity, tx.Amount)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "nouvelle date AAAA-MM-JJ")
	cmd.Flags().StringVar(&account, "account", "", "nouvelle enveloppe")
	cmd.Flags().StringVar(&asset, "asset", "", "nouvel actif")
	cmd.Flags().StringVar(&qty, "qty", "", "nouvelle quantité")
	cmd.Flags().StringVar(&total, "total", "", "nouveau montant total")
	cmd.Flags().StringVar(&note, "note", "", "nouvelle note")
	cmd.Flags().StringVar(&kind, "kind", "", "nouveau type")
	return cmd
}

func txRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Supprime une transaction",
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
				fmt.Fprintf(cmd.OutOrStdout(), "Transaction %d supprimée\n", id)
				return nil
			})
		},
	}
}
