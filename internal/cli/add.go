package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func addCmd(a *app) *cobra.Command {
	return tradeCmd(a, "add", domain.Buy,
		"Record a buy (or a sell: use sell, or a negative quantity after --)")
}

func sellCmd(a *app) *cobra.Command {
	return tradeCmd(a, "sell", domain.Sell, "Record a sell")
}

func tradeCmd(a *app, use string, kind domain.TxKind, short string) *cobra.Command {
	var account, note, ccy string
	cmd := &cobra.Command{
		Use:   use + " <asset> <quantity> [@unit-price|total] [date]",
		Short: short,
		Args:  cobra.RangeArgs(2, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				qty, err := decimal.NewFromString(args[1])
				if err != nil || qty.IsZero() {
					return fmt.Errorf("invalid quantity %q", args[1])
				}
				total, date, err := parseTradeTail(args[2:], qty)
				if err != nil {
					return err
				}
				acc, err := accountFor(b, account, asset)
				if err != nil {
					return err
				}
				effective := kind
				if qty.IsNegative() {
					effective = domain.Sell
				}
				effectiveCcy, err := currencyOr(ccy, asset.Currency)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Asset: asset.ID, Kind: effective,
					Quantity: qty.Abs(),
					Amount:   domain.Money{Amount: total, Currency: effectiveCcy},
					Note:     note,
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s × %s = %s (%s)\n",
					tx.ID, tx.Kind, asset.Name, tx.Quantity, tx.Amount, acc.Name)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "account (name or id)")
	cmd.Flags().StringVar(&note, "note", "", "free note")
	cmd.Flags().StringVar(&ccy, "ccy", "", "amount currency (default: asset currency)")
	return cmd
}

// parseTradeTail reads the optional price and date arguments, in any order:
// "@550" is a unit price (total = |qty| × 550), "5500" a total, "2026-06-01" the date.
func parseTradeTail(rest []string, qty decimal.Decimal) (total decimal.Decimal, date domain.Date, err error) {
	date = domain.Today()
	for _, arg := range rest {
		if unit, ok := strings.CutPrefix(arg, "@"); ok {
			p, perr := decimal.NewFromString(unit)
			if perr != nil {
				return total, date, fmt.Errorf("invalid price %q: %w", arg, perr)
			}
			total = p.Mul(qty.Abs())
		} else if d, derr := domain.ParseDate(arg); derr == nil {
			date = d
		} else if t, terr := decimal.NewFromString(arg); terr == nil {
			total = t.Abs()
		} else {
			return total, date, fmt.Errorf("unexpected argument %q (expected @price, total or date)", arg)
		}
	}
	if total.IsZero() {
		return total, date, errors.New("missing price: @unit-price or total amount")
	}
	return total, date, nil
}
