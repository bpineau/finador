package cli

import (
	"github.com/spf13/cobra"

	"finador/internal/market"
	"finador/internal/portfolio"
)

func exportCmd(a *app) *cobra.Command {
	var ccy, at string
	var tree bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export every holding as CSV (kind, ticker, name, ISIN, gross, net) to stdout, cash included",
		Example: "  finador export > assets.csv\n" +
			"  finador export --ccy USD\n" +
			"  finador export --at 2024-12-31\n" +
			"  finador export --tree            # envelope-grouped text, gross & net",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			date, err := dateOrToday(at)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, b.DisplayCurrency())
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			fx := market.Converter{FX: b.Market.FX}
			if tree {
				lines, err := portfolio.Breakdown(b, date, display, fx)
				if err != nil {
					return err
				}
				return portfolio.WriteAssetTree(cmd.OutOrStdout(), lines, display, date)
			}
			rows, err := portfolio.AllRows(b, date, display, fx)
			if err != nil {
				return err
			}
			return portfolio.WriteAssetCSV(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "display currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&at, "at", "", "valuation date YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&tree, "tree", false, "indented, envelope-grouped text instead of CSV")
	return cmd
}
