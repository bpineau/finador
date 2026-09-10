package cli

import (
	"fmt"
	"maps"
	"slices"
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
	var ccy, at, by, label, account string
	var gross, tree, extended bool
	var exclude, whatIf, only []string
	cmd := &cobra.Command{
		Use:     "value [scope]",
		Aliases: []string{"values"},
		Short:   "Portfolio value (gross, estimated tax, net) - all, a group, an account or an asset",
		Example: "  finador value                 # gross, estimated tax and net (default)\n" +
			"  finador value --gross         # gross value only\n" +
			"  finador value equities/world  # scope to a group\n" +
			"  finador value --tree          # envelope-grouped tree, gross & net\n" +
			"  finador value --account \"PEA Zephyr\"\n" +
			"  finador value --label retraite\n" +
			"  finador value --asset cw8,aapl  # only those two, compounded\n" +
			"  finador value --extended      # count tonight's after-hours prints\n" +
			"  finador value --at 2024-12-31",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			b := f.Book
			ext := extended
			if !cmd.Flags().Changed("extended") {
				ext, _ = strconv.ParseBool(b.Config["extended-hours"])
			}
			if tree && ext {
				// The tree is built by Breakdown, which takes no price
				// override. Asking for both explicitly is a mistake worth
				// naming; inheriting the config default is not, so the tree
				// just keeps the regular prices there.
				if cmd.Flags().Changed("extended") {
					return fmt.Errorf("--tree is incompatible with --extended")
				}
				ext = false
			}
			spot := a.ensureFreshSpot(cmd, f, ext)
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := resolveScope(b, scopeArgs{
				ref: ref, account: account, label: label, exclude: exclude, only: only,
			})
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
			if tree {
				if gross || by != "group" || len(whatIf) > 0 {
					return fmt.Errorf("--tree is incompatible with --gross, --by and --what-if")
				}
				lines, err := portfolio.Breakdown(b, date, display, market.Converter{FX: b.Market.FX})
				if err != nil {
					return err
				}
				return portfolio.WriteAssetTree(cmd.OutOrStdout(),
					portfolio.FilterScope(lines, scope), display, date)
			}
			var opts []portfolio.ValueOption
			switch by {
			case "group":
			case "account":
				opts = append(opts, portfolio.WithLinesByAccount())
			default:
				return fmt.Errorf("--by %q: expected group or account", by)
			}
			whatIfs, err := parseWhatIf(b, whatIf)
			if err != nil {
				return err
			}
			// An off-hours print prices today and nothing else, so a
			// historical valuation ignores the opt-in entirely.
			offHours := map[domain.AssetID]portfolio.PriceOverride{}
			var notes []string
			if ext && date == domain.Today() {
				offHours, notes = offHoursPrices(b, spot)
			}
			overrides := offHours
			if len(whatIfs) > 0 {
				// A disposable hypothesis outranks an observed price.
				overrides = maps.Clone(offHours)
				maps.Copy(overrides, whatIfs)
			}
			if len(overrides) > 0 {
				opts = append(opts, portfolio.WithPriceOverrides(overrides))
			}
			val, err := portfolio.Value(b, scope, date, display, market.Converter{FX: b.Market.FX}, opts...)
			if err != nil {
				return err
			}
			printValuation(cmd, scope, date, val, !gross, notes)
			if len(whatIfs) > 0 {
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
	cmd.Flags().BoolVar(&gross, "gross", false, "show the gross value only (no estimated tax / net)")
	cmd.Flags().Bool("net", false, "") // net is now the default; kept (hidden) for back-compat
	_ = cmd.Flags().MarkHidden("net")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "asset(s) to exclude from scope (repeatable or comma list)")
	cmd.Flags().StringArrayVar(&only, "asset", nil, "keep only this asset (repeatable or comma list); several are compounded into one figure")
	cmd.Flags().StringVar(&by, "by", "group", "line breakdown: group or account")
	cmd.Flags().StringArrayVar(&whatIf, "what-if", nil, "disposable hypothesis asset=price (repeatable), e.g. vizr=280")
	cmd.Flags().StringVar(&account, "account", "", "restrict scope to this envelope (with a group [scope], their intersection)")
	cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
	cmd.Flags().BoolVar(&tree, "tree", false, "indented, envelope-grouped text (gross & net per line)")
	cmd.Flags().BoolVar(&extended, "extended", false, "count pre-market and after-hours prints (thin, shown labelled, never stored)")
	return cmd
}

// parseWhatIf reads "ref=prix" pairs into asset-ID overrides.
func parseWhatIf(b *domain.Book, pairs []string) (map[domain.AssetID]portfolio.PriceOverride, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := map[domain.AssetID]portfolio.PriceOverride{}
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
		out[asset.ID] = portfolio.PriceOverride{Price: price, Kind: "what-if"}
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

func money(x float64, c domain.Currency) string {
	return strconv.FormatFloat(x, 'f', 2, 64) + " " + string(c)
}

// offHoursPrices turns the extended-hours prints of a spot pass into price
// overrides - a throwaway valuation input, never a stored quote - plus one
// note per instrument so no thin off-hours trade passes for a close.
func offHoursPrices(b *domain.Book, spot market.SpotSummary) (map[domain.AssetID]portfolio.PriceOverride, []string) {
	byID := map[domain.AssetID]*domain.Asset{}
	for _, a := range b.Assets {
		byID[a.ID] = a
	}
	prices := map[domain.AssetID]portfolio.PriceOverride{}
	var notes []string
	for id, q := range spot.Quotes {
		asset, ok := byID[id]
		if !ok || !q.Extended() || q.Price <= 0 {
			continue
		}
		// The Source contract guarantees the declared currency; a quote that
		// somehow escaped it would be a silent unit bug in the total.
		if q.Currency != "" && q.Currency != asset.Currency {
			continue
		}
		// No Kind: the note below already names the session and the instant.
		prices[id] = portfolio.PriceOverride{Price: q.Price}
		notes = append(notes, fmt.Sprintf("%s: %s %s, %s (off-hours print, not a close)",
			assetLabel(asset), q.Session, q.Time.Local().Format("15:04 MST"),
			money(q.Price, asset.Currency)))
	}
	slices.Sort(notes) // map iteration order must not shuffle the output
	return prices, notes
}

// assetLabel names an asset in a note: its ticker when it has one.
func assetLabel(a *domain.Asset) string {
	if a.Ticker != "" {
		return a.Ticker
	}
	return a.Name
}

func printValuation(cmd *cobra.Command, scope portfolio.Scope, date domain.Date, v portfolio.Valuation, net bool, extended []string) {
	out := cmd.OutOrStdout()
	// Said once, and only when an off-hours print really is inside the
	// figures: a flag that changed nothing must not claim it did.
	if len(extended) > 0 {
		fmt.Fprintf(out, "%s - %s (extended hours)\n", scope.Label, date)
	} else {
		fmt.Fprintf(out, "%s - %s\n", scope.Label, date)
	}
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
	for _, s := range extended {
		fmt.Fprintln(errw, "≈", s)
	}
	for _, s := range v.Stale {
		fmt.Fprintln(errw, "≈", s)
	}
	if net && v.TaxNote != "" {
		fmt.Fprintln(errw, "ℹ", v.TaxNote)
	}
}

// ensureDisplayFX fetches the display currency's FX series when the cache
// lacks it - the regular refresh only covers currencies the book uses.
func ensureDisplayFX(cmd *cobra.Command, a *app, f *store.File, display domain.Currency) {
	if a.offline || display == domain.USD {
		return
	}
	if _, ok := f.Book.Market.FXSeries(display).Last(); ok {
		return
	}
	data, err := a.marketSource().Daily(cmd.Context(), market.Ref{Symbol: string(display) + "USD=X"}, domain.Today().AddDays(-30))
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
		return
	}
	if data.Currency != "" && data.Currency != domain.USD {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %sUSD=X quotes in %s but USD expected: quotes ignored\n", display, data.Currency)
		return
	}
	s := f.Book.Market.FXSeries(display)
	s.Merge(data.Closes)
	s.FetchedAt = domain.Today()
	if err := f.SaveCache(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: cache not saved:", err)
	}
}
