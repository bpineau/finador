package cli

import (
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func cashCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cash",
		Short: "Record external cash flows and observed balances",
		Long: `Record cash activity on an account envelope.

Which subcommand to use:
  deposit/withdraw  - external cash entering or leaving an envelope (an apport
                      or retrait). These are neutral for performance: they feed
                      the tax basis and XIRR but do not count as gains or losses.
  set               - the observed balance of the account at a point in time.
                      The gap between two statements counts as performance
                      (e.g. interest earned on a savings account).`,
		Example: "  finador cash deposit \"PEA Zephyr\" 10000 2024-01-15",
	}
	cmd.AddCommand(
		flowCmd(a, "deposit", domain.Deposit,
			"External contribution to an account (tax basis, XIRR)",
			"  finador cash deposit \"PEA Zephyr\" 10000 2024-01-15"),
		flowCmd(a, "withdraw", domain.Withdraw,
			"External withdrawal from an account",
			"  finador cash withdraw \"Livret A\" 2000"),
		cashSet(a),
	)
	return cmd
}

func cashSet(a *app) *cobra.Command {
	var at, ccy string
	cmd := &cobra.Command{
		Use:     "set <account> <balance>",
		Short:   "Set the observed balance of an account (gaps between statements count as performance - use deposit for external contributions)",
		Example: "  finador cash set \"Livret A\" 15000 --at 2026-06-01",
		Args:    cobra.ExactArgs(2),
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
	cmd.Flags().StringVar(&at, "at", "", "date YYYY-MM-DD (default: today)")
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: account currency)")
	return cmd
}
