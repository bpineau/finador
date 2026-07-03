package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/market"
	"finador/internal/portfolio"
)

func exportCmd(a *app) *cobra.Command {
	var ccy, at, label string
	var exclude []string
	var tree, script bool
	cmd := &cobra.Command{
		Use:   "export [scope]",
		Short: "Export every holding as CSV (kind, ticker, name, ISIN, gross, net) to stdout, cash included",
		Example: "  finador export > assets.csv\n" +
			"  finador export --ccy USD\n" +
			"  finador export --at 2024-12-31\n" +
			"  finador export --tree            # envelope-grouped text, gross & net\n" +
			"  finador export pea --tree        # same, scoped to one envelope or group\n" +
			"  finador export --script          # replayable finador commands (rebuild recipe)",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			if script {
				if tree || len(args) == 1 || label != "" || len(exclude) > 0 {
					return fmt.Errorf("--script dumps the whole book: no scope, --tree, --label or --exclude")
				}
				return writeScript(cmd.OutOrStdout(), f.Book)
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := resolveScope(b, ref, label, exclude)
			if err != nil {
				return err
			}
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
				return portfolio.WriteAssetTree(cmd.OutOrStdout(),
					portfolio.FilterScope(lines, scope), display, date)
			}
			rows, err := portfolio.ScopedRows(b, scope, date, display, fx)
			if err != nil {
				return err
			}
			return portfolio.WriteAssetCSV(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "display currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&at, "at", "", "valuation date YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&tree, "tree", false, "indented, envelope-grouped text instead of CSV")
	cmd.Flags().BoolVar(&script, "script", false, "replayable finador commands that rebuild the portfolio")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
	return cmd
}
