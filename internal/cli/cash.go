package cli

import (
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func cashCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "cash", Short: "Account cash balances"}
	var at, ccy string
	set := &cobra.Command{
		Use:   "set <account> <balance>",
		Short: "Set the observed balance of an account (gaps between statements count as performance — use deposit for external contributions)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("invalid balance %q: %w", args[1], err)
				}
				date, err := dateOrToday(at)
				if err != nil {
					return err
				}
				effectiveCcy, err := currencyOr(ccy, acc.Currency)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: amount, Currency: effectiveCcy},
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s on %s\n", tx.ID, acc.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	set.Flags().StringVar(&at, "at", "", "date YYYY-MM-DD (default: today)")
	set.Flags().StringVar(&ccy, "ccy", "", "currency (default: account currency)")
	cmd.AddCommand(set)
	return cmd
}
