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
		"Enregistre un achat (ou une vente : sell, ou quantité négative après --)")
}

func sellCmd(a *app) *cobra.Command {
	return tradeCmd(a, "sell", domain.Sell, "Enregistre une vente")
}

func tradeCmd(a *app, use string, kind domain.TxKind, short string) *cobra.Command {
	var account, note, ccy string
	cmd := &cobra.Command{
		Use:   use + " <actif> <quantité> [@prix-unitaire|total] [date]",
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
					return fmt.Errorf("quantité %q invalide", args[1])
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
	cmd.Flags().StringVar(&account, "account", "", "enveloppe (nom ou id)")
	cmd.Flags().StringVar(&note, "note", "", "note libre")
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise du montant (défaut : celle de l'actif)")
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
				return total, date, fmt.Errorf("prix %q: %w", arg, perr)
			}
			total = p.Mul(qty.Abs())
		} else if d, derr := domain.ParseDate(arg); derr == nil {
			date = d
		} else if t, terr := decimal.NewFromString(arg); terr == nil {
			total = t.Abs()
		} else {
			return total, date, fmt.Errorf("argument %q incompris (attendu @prix, total ou date)", arg)
		}
	}
	if total.IsZero() {
		return total, date, errors.New("prix manquant : @prix-unitaire ou montant total")
	}
	return total, date, nil
}
