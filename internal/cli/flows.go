package cli

import (
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func depositCmd(a *app) *cobra.Command {
	return flowCmd(a, "deposit", domain.Deposit, "Apport externe vers un compte (base fiscale, XIRR)")
}

func withdrawCmd(a *app) *cobra.Command {
	return flowCmd(a, "withdraw", domain.Withdraw, "Retrait externe d'un compte")
}

func flowCmd(a *app, use string, kind domain.TxKind, short string) *cobra.Command {
	var ccy, note string
	cmd := &cobra.Command{
		Use:   use + " <compte> <montant> [date]",
		Short: short,
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("montant %q: %w", args[1], err)
				}
				date := domain.Today()
				if len(args) == 3 {
					if date, err = domain.ParseDate(args[2]); err != nil {
						return err
					}
				}
				effectiveCcy, err := currencyOr(ccy, acc.Currency)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Kind: kind,
					Amount: domain.Money{Amount: amount.Abs(), Currency: effectiveCcy},
					Note:   note,
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s : %s le %s\n", tx.ID, tx.Kind, acc.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle du compte)")
	cmd.Flags().StringVar(&note, "note", "", "note libre")
	return cmd
}
