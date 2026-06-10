package cli

import (
	"fmt"

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
	cmd := &cobra.Command{
		Use:   "chart [portée]",
		Short: "Courbe d'évolution de la valeur, dans le terminal",
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
			ensureDisplayFX(cmd, a, f, display)
			res, err := portfolio.Series(b, scope, fromD, toD, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			pts := make([]perf.Point, len(res.Points))
			label := "brut"
			for i, p := range res.Points {
				v := p.Gross
				if net {
					v, label = p.Net, "net"
				}
				pts[i] = perf.Point{Date: p.Date, Value: v}
			}
			last := pts[len(pts)-1]
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s, %s) — dernier point : %s\n",
				scope.Label, label, display, money(last.Value, display))
			fmt.Fprint(cmd.OutOrStdout(), chart.Braille(pts, width, height))
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&from, "from", "", "début AAAA-MM-JJ (défaut : origine)")
	cmd.Flags().StringVar(&to, "to", "", "fin AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().BoolVar(&net, "net", false, "courbe nette d'impôt latent")
	cmd.Flags().IntVar(&width, "width", 70, "largeur en caractères")
	cmd.Flags().IntVar(&height, "height", 12, "hauteur en lignes")
	return cmd
}
