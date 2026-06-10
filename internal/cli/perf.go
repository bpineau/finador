package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func perfCmd(a *app) *cobra.Command {
	var ccy, from, to string
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
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			evalTo, err := dateOrToday(to)
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			fx := market.Converter{FX: b.Market.FX}

			// série complète depuis l'origine : base des métriques et des périodes
			res, err := portfolio.Series(b, scope, domain.Date{}, evalTo, display, fx)
			if err != nil {
				return err
			}
			if len(res.Points) < 2 {
				return errors.New("pas assez d'historique pour mesurer une performance")
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintf(cmd.OutOrStdout(), "%s — performance (%s), évalué au %s\n", scope.Label, display, evalTo)
			fmt.Fprintln(w, "PÉRIODE\tTWR\tXIRR")
			for _, name := range perf.Names() {
				pf, pt, err := perf.PeriodRange(name, evalTo)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", name, twrCell(res, pf, pt), xirrCell(res, pf, pt))
			}
			origin := res.Points[0].Date
			fmt.Fprintf(w, "origine\t%s\t%s\n", twrCell(res, origin, evalTo), xirrCell(res, origin, evalTo))
			if from != "" {
				wf, err := domain.ParseDate(from)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "fenêtre\t%s\t%s\n", twrCell(res, wf, evalTo), xirrCell(res, wf, evalTo))
			}
			w.Flush()

			rf := riskFree(b)
			allPts, allFlows := window(res, origin, evalTo)
			returns := perf.DailyReturns(allPts, allFlows)
			twrTotal := perf.TWR(allPts, allFlows)
			days := int(res.Points[len(res.Points)-1].Date.Time().Sub(origin.Time()).Hours() / 24)
			fmt.Fprintf(cmd.OutOrStdout(), "\nCAGR %s   vol %s   Sharpe %.2f   Sortino %.2f   (rf %s)\n",
				pct(perf.CAGR(twrTotal, days)), pct(perf.Vol(returns)),
				perf.Sharpe(returns, rf), perf.Sortino(returns, rf), pct(rf))
			dd := perf.MaxDrawdown(allPts)
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

// riskFree reads config "risk-free" ("2.4%"), defaulting to zero.
func riskFree(b *domain.Book) float64 {
	s := strings.TrimSuffix(strings.TrimSpace(b.Config["risk-free"]), "%")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v / 100
}
