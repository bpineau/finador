package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func chartCmd(a *app) *cobra.Command {
	var ccy, from, to string
	var net bool
	var width, height int
	var exclude []string
	cmd := &cobra.Command{
		Use:     "chart [scope]",
		Short:   "Value history chart, rendered in the terminal",
		Example: "  finador chart",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := portfolio.ParseScope(b, ref)
			if err != nil {
				return err
			}
			excluded, err := parseExclusions(b, exclude)
			if err != nil {
				return err
			}
			if len(excluded) > 0 {
				scope.Excluded = excluded
				scope.Label += " (excluding " + strings.Join(exclude, ",") + ")"
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			fromD := domain.Date{}
			if from != "" {
				if fromD, err = domain.ParseDate(from); err != nil {
					return err
				}
			}
			toD, err := dateOrToday(to)
			if err != nil {
				return err
			}
			if today := domain.Today(); today.Before(toD) {
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ future date clamped to today (%s)\n", today)
				toD = today
			}
			ensureDisplayFX(cmd, a, f, display)
			res, err := portfolio.Series(b, scope, fromD, toD, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ %s\n", w)
			}
			pts := make([]perf.Point, len(res.Points))
			label := "gross"
			for i, p := range res.Points {
				v := p.Gross
				if net {
					v, label = p.Net, "net"
				}
				pts[i] = perf.Point{Date: p.Date, Value: v}
			}
			last := pts[len(pts)-1]
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s, %s) — last point: %s\n",
				scope.Label, label, display, money(last.Value, display))
			fmt.Fprint(cmd.OutOrStdout(), chart.Braille(pts, width, height))
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&from, "from", "", "start YYYY-MM-DD (default: inception)")
	cmd.Flags().StringVar(&to, "to", "", "end YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&net, "net", false, "net-of-tax curve")
	cmd.Flags().IntVar(&width, "width", 70, "width in characters")
	cmd.Flags().IntVar(&height, "height", 12, "height in lines")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	return cmd
}
