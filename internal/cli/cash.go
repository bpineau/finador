package cli

import (
	"cmp"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func cashCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "cash", Short: "Soldes de liquidités des comptes"}
	var at, ccy string
	set := &cobra.Command{
		Use:   "set <compte> <solde>",
		Short: "Pose le solde constaté d'un compte à une date",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("solde %q: %w", args[1], err)
				}
				date, err := dateOrToday(at)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: amount, Currency: cmp.Or(domain.Currency(ccy), acc.Currency)},
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s : %s au %s\n", tx.ID, acc.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	set.Flags().StringVar(&at, "at", "", "date AAAA-MM-JJ (défaut : aujourd'hui)")
	set.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle du compte)")
	cmd.AddCommand(set)
	return cmd
}
