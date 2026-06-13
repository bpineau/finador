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
	var ccy, at, by, label string
	var net bool
	var exclude, whatIf []string
	cmd := &cobra.Command{
		Use:   "value [scope]",
		Short: "Portfolio value — all, a group, an account or an asset",
		Example: "  finador value --net\n" +
			"  finador value --at 2024-12-31\n" +
			"  finador value equities/world\n" +
			"  finador value --label retraite\n" +
			"  finador value --exclude CW8",
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
			date, err := dateOrToday(at)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			var opts []portfolio.ValueOption
			switch by {
			case "group":
			case "account":
				opts = append(opts, portfolio.WithLinesByAccount())
			default:
				return fmt.Errorf("--by %q: expected group or account", by)
			}
			overrides, err := parseWhatIf(b, whatIf)
			if err != nil {
				return err
			}
			if len(overrides) > 0 {
				opts = append(opts, portfolio.WithPriceOverrides(overrides))
			}
			val, err := portfolio.Value(b, scope, date, display, market.Converter{FX: b.Market.FX}, opts...)
			if err != nil {
				return err
			}
			printValuation(cmd, scope, date, val, net)
			if len(overrides) > 0 {
				base, err := portfolio.Value(b, scope, date, display, market.Converter{FX: b.Market.FX})
				if err == nil {
					printWhatIfDelta(cmd, val, base)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "display currency (default: config currency, otherwise EUR)")
	cmd.Flags().StringVar(&at, "at", "", "valuation date YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&net, "net", false, "show gross, estimated tax and net")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	cmd.Flags().StringVar(&by, "by", "group", "line breakdown: group or account")
	cmd.Flags().StringArrayVar(&whatIf, "what-if", nil, "disposable hypothesis asset=price (repeatable), e.g. ddog=280")
	cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
	return cmd
}

// parseWhatIf reads "ref=prix" pairs into asset-ID overrides.
func parseWhatIf(b *domain.Book, pairs []string) (map[domain.AssetID]float64, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := map[domain.AssetID]float64{}
	for _, p := range pairs {
		ref, val, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("--what-if %q: expected asset=price", p)
		}
		asset, err := b.Asset(strings.TrimSpace(ref))
		if err != nil {
			return nil, fmt.Errorf("--what-if %s: %w", ref, err)
		}
		price, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil || price < 0 {
			return nil, fmt.Errorf("--what-if %s: invalid price %q", ref, val)
		}
		out[asset.ID] = price
	}
	return out, nil
}

// printWhatIfDelta compares the hypothesis with reality.
func printWhatIfDelta(cmd *cobra.Command, hyp, base portfolio.Valuation) {
	out := cmd.OutOrStdout()
	dg, dn := hyp.Gross-base.Gross, hyp.Net-base.Net
	fmt.Fprintf(out, "\nvs actual: gross %+.2f %s", dg, string(hyp.Currency))
	if base.Gross != 0 {
		fmt.Fprintf(out, " (%+.2f%%)", dg/base.Gross*100)
	}
	fmt.Fprintf(out, " · net %+.2f %s", dn, string(hyp.Currency))
	if base.Net != 0 {
		fmt.Fprintf(out, " (%+.2f%%)", dn/base.Net*100)
	}
	fmt.Fprintln(out)
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
	fmt.Fprintf(out, "%s — %s\n", scope.Label, date)
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	if net {
		fmt.Fprintln(w, "LINE\tGROSS\tTAX\tNET")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Label,
				money(l.Gross, v.Currency), money(l.Tax, v.Currency), money(l.Net, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\t%s\t%s\n",
			money(v.Gross, v.Currency), money(v.Tax, v.Currency), money(v.Net, v.Currency))
	} else {
		fmt.Fprintln(w, "LINE\tVALUE")
		for _, l := range v.Lines {
			fmt.Fprintf(w, "%s\t%s\n", l.Label, money(l.Gross, v.Currency))
		}
		fmt.Fprintf(w, "TOTAL\t%s\n", money(v.Gross, v.Currency))
	}
	_ = w.Flush()
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
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
		return
	}
	s := f.Book.Market.FXSeries(display)
	s.Merge(data.Closes)
	s.FetchedAt = domain.Today()
	if err := f.Save(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: cache not saved:", err)
	}
}
