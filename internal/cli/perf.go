package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func perfCmd(a *app) *cobra.Command {
	var ccy, from, to, label string
	var exclude []string
	var tree bool
	cmd := &cobra.Command{
		Use:   "perf [scope]",
		Short: "Returns (TWR, gain) by period and risk metrics",
		Example: "  finador perf\n" +
			"  finador perf \"PEA Zephyr\"\n" +
			"  finador perf equities/world\n" +
			"  finador perf --tree            # per-envelope tree: net, 1d/7d/1m/3m\n" +
			"  finador perf --label retraite\n" +
			"  finador perf --exclude CW8,AAPL",
		Args: cobra.MaximumNArgs(1),
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
			scope, err := resolveScope(b, ref, label, exclude)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, b.DisplayCurrency())
			if err != nil {
				return err
			}
			evalTo, err := dateOrToday(to)
			if err != nil {
				return err
			}
			if today := domain.Today(); today.Before(evalTo) {
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ future date clamped to today (%s)\n", today)
				evalTo = today
			}
			// Measure as of the last settled close, not calendar today: the flat
			// table, the tree, the custom window and the "as of" header all end
			// there (see perf.CloseAnchor).
			evalTo = perf.CloseAnchor(&b.Market, evalTo)
			ensureDisplayFX(cmd, a, f, display)
			if tree {
				if from != "" {
					return errors.New("--tree shows fixed windows (1d, 7d, 1m, 3m): --from does not apply")
				}
				return perfTree(cmd, a, b, scope, display, evalTo)
			}
			fx := market.Converter{FX: b.Market.FX}

			// full series since inception: basis for both metrics and periods
			res, err := portfolio.Series(b, scope, domain.Date{}, evalTo, display, fx)
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ %s\n", w)
			}
			if len(res.Points) < 2 {
				return errors.New("not enough history to measure performance")
			}

			pts := res.PerfPoints(false)
			fls := res.PerfFlows()
			rf := perf.RiskFreeFromConfig(b.Config)
			rows, metrics := perf.Report(pts, fls, evalTo, rf)

			out := cmd.OutOrStdout()
			colored := a.colorsEnabled(cmd)

			// pad pads s to visible width w (using rune count, ignoring ANSI sequences).
			pad := func(s string, w int) string {
				for len([]rune(s)) < w {
					s = " " + s
				}
				return s
			}

			fmt.Fprintf(out, "%s - performance (%s), as of %s\n", scope.Label, display, evalTo)
			fmt.Fprintf(out, "%-9s %14s %16s\n", "PERIOD", "TWR", "GAIN ("+string(display)+")")
			printRow := func(name, twrStr, gainStr string, ts, gs float64) {
				fmt.Fprintf(out, "%-9s %s %s\n",
					name,
					tint(pad(twrStr, 14), ts, colored),
					tint(pad(gainStr, 16), gs, colored),
				)
			}
			for _, row := range rows {
				twrStr, gainStr := "-", "-"
				var ts, gs float64
				if row.HasTWR {
					twrStr = pctSigned(row.TWR)
					ts = row.TWR
				}
				if row.HasGain {
					gainStr = fmt.Sprintf("%+.2f", row.Gain)
					gs = row.Gain
				}
				printRow(row.Name, twrStr, gainStr, ts, gs)
			}
			if from != "" {
				wf, err := domain.ParseDate(from)
				if err != nil {
					return err
				}
				gainStr, gv := gainCell(res, wf, evalTo)
				printRow("window", twrCell(res, wf, evalTo), gainStr, 0, gv)
			}

			summary := cmd.OutOrStdout()
			fmt.Fprintf(summary, "\ntracking since %s (%d d)", metrics.Since, metrics.Days)
			if metrics.HasCAGR {
				fmt.Fprintf(summary, "   CAGR %s", pct(metrics.CAGR))
			}
			if metrics.HasRisk {
				fmt.Fprintf(summary, "   vol %s   Sharpe %.2f   Sortino %.2f   (rf %s)",
					pct(metrics.Vol), metrics.Sharpe, metrics.Sortino, pct(metrics.RiskFree))
			}
			fmt.Fprintln(summary)
			dd := metrics.Drawdown
			if dd.Depth < 0 {
				rec := "not recovered"
				if dd.Recovered != nil {
					rec = "recovered on " + dd.Recovered.String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "max drawdown %s (%s → %s, %s)\n", pct(dd.Depth), dd.Peak, dd.Trough, rec)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "max drawdown - none")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&from, "from", "", "start of a custom window YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "valuation date YYYY-MM-DD (default: today)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
	cmd.Flags().BoolVar(&tree, "tree", false, "envelope-grouped tree: net value and 1d/7d/1m/3m returns per line")
	return cmd
}

// window slices the series to [from, to]; clamped to available history.
func window(res portfolio.SeriesResult, from, to domain.Date) ([]perf.Point, []perf.Flow) {
	var pts []perf.Point
	for _, p := range res.Points {
		if p.Date.Before(from) || to.Before(p.Date) {
			continue
		}
		pts = append(pts, perf.Point{Date: p.Date, Value: p.Gross})
	}
	var flows []perf.Flow
	for _, fl := range res.Flows {
		if to.Before(fl.Date) || !from.Before(fl.Date) {
			continue // flows on the base day (and before) are already in V0
		}
		flows = append(flows, perf.Flow{Date: fl.Date, Amount: fl.Amount})
	}
	return pts, flows
}

func twrCell(res portfolio.SeriesResult, from, to domain.Date) string {
	pts, flows := window(res, from, to)
	if len(pts) < 2 {
		return "-"
	}
	return pctSigned(perf.TWR(pts, flows))
}

// gainCell: money P&L over [from, to], net of contributions; "-" if too short.
func gainCell(res portfolio.SeriesResult, from, to domain.Date) (string, float64) {
	pts, flows := window(res, from, to)
	if len(pts) < 2 {
		return "-", 0
	}
	var nf float64
	for _, f := range flows {
		nf += f.Amount
	}
	g := pts[len(pts)-1].Value - pts[0].Value - nf
	return fmt.Sprintf("%+.2f", g), g
}

// pctSigned formats a fraction as a signed percentage: "+2.00%" or "-1.50%".
func pctSigned(x float64) string {
	return fmt.Sprintf("%+.2f%%", x*100)
}

// pct formats a fraction as an unsigned percentage: "2.00%".
func pct(x float64) string {
	return strconv.FormatFloat(x*100, 'f', 2, 64) + "%"
}
