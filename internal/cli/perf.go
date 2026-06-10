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
	var ccy, from, to string
	var exclude []string
	cmd := &cobra.Command{
		Use:   "perf [portée]",
		Short: "Rendements (TWR, XIRR) par période et métriques de risque",
		Args:  cobra.MaximumNArgs(1),
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
				scope.Label += " (hors " + strings.Join(exclude, ",") + ")"
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
				fmt.Fprintf(cmd.ErrOrStderr(), "≈ borne future ramenée à aujourd'hui (%s)\n", today)
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
				return errors.New("pas assez d'historique pour mesurer une performance")
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

			fmt.Fprintf(out, "%s — performance (%s), évalué au %s\n", scope.Label, display, evalTo)
			fmt.Fprintf(out, "%-9s %14s %14s\n", "PÉRIODE", "TWR", "XIRR")
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
				printRow("fenêtre", twrCell(res, wf, evalTo), xirrCell(res, wf, evalTo), 0, 0)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nCAGR %s   vol %s   Sharpe %.2f   Sortino %.2f   (rf %s)\n",
				pct(metrics.CAGR), pct(metrics.Vol),
				metrics.Sharpe, metrics.Sortino, pct(metrics.RiskFree))
			dd := metrics.Drawdown
			if dd.Depth < 0 {
				rec := "non récupéré"
				if dd.Recovered != nil {
					rec = "récupéré le " + dd.Recovered.String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "max drawdown %s (%s → %s, %s)\n", pct(dd.Depth), dd.Peak, dd.Trough, rec)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "max drawdown — aucun")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&from, "from", "", "début d'une fenêtre libre AAAA-MM-JJ")
	cmd.Flags().StringVar(&to, "to", "", "date d'évaluation AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "actif(s) à exclure de la portée (répétable ou liste à virgules)")
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
