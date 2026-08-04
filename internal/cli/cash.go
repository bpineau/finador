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

An account's cash balance is what you declare here, and nothing else: buys,
sells, dividends and fees never move it. Recording a purchase and recording
the cash that funded it are two independent statements.

Which subcommand to use:
  deposit/withdraw  - the pocket grows or shrinks: an apport, a retrait, a
                      transfer, or the cash you just spent on a purchase.
                      Neutral for performance: they feed the tax basis and the
                      flows but never count as gains or losses.
  set               - the observed balance of the account at a point in time.
                      The gap between two statements counts as performance
                      (e.g. interest earned on a savings account), so use it to
                      record what an account earned, not what you spent.`,
		Example: "  finador cash deposit \"PEA Zephyr\" 10000 2024-01-15",
	}
	cmd.AddCommand(
		flowCmd(a, "deposit", domain.Deposit,
			"Cash entering an account - a contribution, a transfer or sale proceeds (neutral for performance)",
			"  finador cash deposit \"PEA Zephyr\" 10000 2024-01-15"),
		flowCmd(a, "withdraw", domain.Withdraw,
			"Cash leaving an account - a withdrawal, a transfer or what a purchase spent (neutral for performance)",
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
				if n := tradesSinceLastCashStatement(b, acc.ID, date); n > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: %s has %d trade(s) since its last cash statement, and a trade never spends declared cash.\n"+
							"         The gap this statement closes will count as performance. If this balance dropped\n"+
							"         because you invested it, record \"cash withdraw\" instead.\n", acc.Name, n)
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

// tradesSinceLastCashStatement counts the buys and sells recorded on the
// account between its previous pure-cash statement (or the beginning) and at.
// A non-zero count means the new statement is probably closing a gap the user
// created by investing - which `cash set` would book as performance, where
// `cash withdraw` is neutral. See D29.
func tradesSinceLastCashStatement(b *domain.Book, acc domain.AccountID, at domain.Date) int {
	var last domain.Date
	for _, t := range b.Transactions {
		if t.Account != acc || t.Asset != "" || t.Kind != domain.Statement {
			continue
		}
		if !at.Before(t.Date) && last.Before(t.Date) {
			last = t.Date
		}
	}
	n := 0
	for _, t := range b.Transactions {
		if t.Account != acc || at.Before(t.Date) || t.Date.Before(last) {
			continue
		}
		if t.Kind == domain.Buy || t.Kind == domain.Sell {
			n++
		}
	}
	return n
}
