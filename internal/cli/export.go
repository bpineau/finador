package cli

import (
	"github.com/spf13/cobra"

	"finador/internal/market"
	"finador/internal/portfolio"
)

func exportCmd(a *app) *cobra.Command {
	var ccy, at string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export your assets as CSV (ticker, name, ISIN, gross, net) to stdout",
		Example: "  finador export > assets.csv\n" +
			"  finador export --ccy USD\n" +
			"  finador export --at 2024-12-31",
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
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			rows, err := portfolio.AssetRows(b, date, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			return portfolio.WriteAssetCSV(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "display currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&at, "at", "", "valuation date YYYY-MM-DD (default: today)")
	return cmd
}
