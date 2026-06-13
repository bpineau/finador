package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func perfCmd(a *app) *cobra.Command {
	var ccy, from, to, label string
	var exclude []string
	cmd := &cobra.Command{
		Use:   "perf [scope]",
		Short: "Returns (TWR, XIRR) by period and risk metrics",
		Example: "  finador perf\n" +
			"  finador perf \"PEA BforBank\"\n" +
			"  finador perf equities/world\n" +
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
			if ref != "" && label != "" {
				return fmt.Errorf("use either a [scope] argument or --label, not both")
			}
			var scope portfolio.Scope
			if label != "" {
				s, err := portfolio.LabelScope(b, label)
				if err != nil {
					return err
				}
				scope = s
			} else {
				s, err := portfolio.ParseScope(b, ref)
				if err != nil {
					return err
				}
				scope = s
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
			evalTo, err := dateOrToday(to)
			if err != nil {
				return err
			}
			if today := domain.Today(); today.Before(evalTo) {
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ future date clamped to today (%s)\n", today)
				evalTo = today
			}
			ensureDisplayFX(cmd, a, f, display)
			fx := market.Converter{FX: b.Market.FX}

			// série complète depuis l'origine : base des métriques et des périodes
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

			fmt.Fprintf(out, "%s — performance (%s), as of %s\n", scope.Label, display, evalTo)
			fmt.Fprintf(out, "%-9s %14s %14s\n", "PERIOD", "TWR", "XIRR")
			printRow := func(name, twrStr, xirrStr string, ts, xs float64) {
				fmt.Fprintf(out, "%-9s %s %s\n",
					name,
					tint(pad(twrStr, 14), ts, colored),
					tint(pad(xirrStr, 14), xs, colored),
				)
			}
			for _, row := range rows {
				twrStr, xirrStr := "—", "—"
				var ts, xs float64
				if row.HasTWR {
					twrStr = pctSigned(row.TWR)
					ts = row.TWR
				}
				if row.HasXIRR {
					xirrStr = pctSigned(row.XIRR)
					xs = row.XIRR
				}
				printRow(row.Name, twrStr, xirrStr, ts, xs)
			}
			if from != "" {
				wf, err := domain.ParseDate(from)
				if err != nil {
					return err
				}
				printRow("window", twrCell(res, wf, evalTo), xirrCell(res, wf, evalTo), 0, 0)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nCAGR %s   vol %s   Sharpe %.2f   Sortino %.2f   (rf %s)\n",
				pct(metrics.CAGR), pct(metrics.Vol),
				metrics.Sharpe, metrics.Sortino, pct(metrics.RiskFree))
			dd := metrics.Drawdown
			if dd.Depth < 0 {
				rec := "not recovered"
				if dd.Recovered != nil {
					rec = "recovered on " + dd.Recovered.String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "max drawdown %s (%s → %s, %s)\n", pct(dd.Depth), dd.Peak, dd.Trough, rec)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "max drawdown — none")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&from, "from", "", "start of a custom window YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "valuation date YYYY-MM-DD (default: today)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
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
			continue // les flux du jour de base (et avant) sont dans V0
		}
		flows = append(flows, perf.Flow{Date: fl.Date, Amount: fl.Amount})
	}
	return pts, flows
}

func twrCell(res portfolio.SeriesResult, from, to domain.Date) string {
	pts, flows := window(res, from, to)
	if len(pts) < 2 {
		return "—"
	}
	return pctSigned(perf.TWR(pts, flows))
}

// xirrCell: windows shorter than 30 days print "—" (annualizing a daily move
// is meaningless).
func xirrCell(res portfolio.SeriesResult, from, to domain.Date) string {
	if to.Time().Sub(from.Time()).Hours() < 30*24 {
		return "—"
	}
	pts, flows := window(res, from, to)
	if len(pts) < 2 || pts[0].Value <= 0 {
		return "—"
	}
	cfs := []perf.Flow{{Date: pts[0].Date, Amount: -pts[0].Value}}
	for _, fl := range flows {
		cfs = append(cfs, perf.Flow{Date: fl.Date, Amount: -fl.Amount})
	}
	cfs = append(cfs, perf.Flow{Date: pts[len(pts)-1].Date, Amount: pts[len(pts)-1].Value})
	r, err := perf.XIRR(cfs)
	if err != nil {
		return "—"
	}
	return pctSigned(r)
}

// pctSigned formats a fraction as a signed percentage: "+2.00%" or "-1.50%".
func pctSigned(x float64) string {
	return fmt.Sprintf("%+.2f%%", x*100)
}

// pct formats a fraction as an unsigned percentage: "2.00%".
func pct(x float64) string {
	return strconv.FormatFloat(x*100, 'f', 2, 64) + "%"
}
