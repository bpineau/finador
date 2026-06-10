package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
	"finador/internal/store"
)

func valueCmd(a *app) *cobra.Command {
	var ccy, at string
	var net bool
	var exclude []string
	cmd := &cobra.Command{
		Use:   "value [portée]",
		Short: "Valeur du patrimoine — tout, un groupe, une enveloppe ou un actif",
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
			date, err := dateOrToday(at)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			val, err := portfolio.Value(b, scope, date, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			printValuation(cmd, scope, date, val, net)
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise d'affichage (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&at, "at", "", "date d'évaluation AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().BoolVar(&net, "net", false, "affiche brut, impôt latent estimé et net")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "actif(s) à exclure de la portée (répétable ou liste à virgules)")
	return cmd
}

// displayCurrency: config "currency" si valide, sinon EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}

func money(x float64, c domain.Currency) string {
	return strconv.FormatFloat(x, 'f', 2, 64) + " " + string(c)
}

func printValuation(cmd *cobra.Command, scope portfolio.Scope, date domain.Date, v portfolio.Valuation, net bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s au %s\n", scope.Label, date)
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	if net {
		fmt.Fprintln(w, "LIGNE\tBRUT\tIMPÔT\tNET")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Label,
				money(l.Gross, v.Currency), money(l.Tax, v.Currency), money(l.Net, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\t%s\t%s\n",
			money(v.Gross, v.Currency), money(v.Tax, v.Currency), money(v.Net, v.Currency))
	} else {
		fmt.Fprintln(w, "LIGNE\tVALEUR")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\n", l.Label, money(l.Gross, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\n", money(v.Gross, v.Currency))
	}
	w.Flush()
	errw := cmd.ErrOrStderr()
	for _, s := range v.Stale {
		fmt.Fprintln(errw, "≈", s)
	}
	if net && v.TaxNote != "" {
		fmt.Fprintln(errw, "ℹ", v.TaxNote)
	}
}

// ensureDisplayFX fetches the display currency's FX series when the cache
// lacks it — the regular refresh only covers currencies the book uses.
func ensureDisplayFX(cmd *cobra.Command, a *app, f *store.File, display domain.Currency) {
	if a.offline || display == domain.USD {
		return
	}
	if _, ok := f.Book.Market.FXSeries(display).Last(); ok {
		return
	}
	data, err := a.marketSource().Daily(cmd.Context(), string(display)+"USD=X", domain.Today().AddDays(-30))
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "avertissement:", err)
		return
	}
	s := f.Book.Market.FXSeries(display)
	s.Merge(data.Closes)
	s.FetchedAt = domain.Today()
	if err := f.Save(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "avertissement: cache non sauvegardé:", err)
	}
}
