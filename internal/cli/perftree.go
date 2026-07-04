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

// perfTreePeriods are the tree's return columns, shortest first; the array
// doubles as the header row.
var perfTreePeriods = [4]string{"1d", "7d", "1m", "3m"}

// perfTree renders the scope as an envelope-grouped tree: after-tax net
// value per line, then the flow-neutralized TWR over each period column.
// Every number agrees with the flat `perf` table run on the same sub-scope;
// a dash marks a window the line's own history does not cover. Cash lines
// (and cash-only envelopes) show no period returns: their market effect, FX
// included, belongs to the envelope row.
func perfTree(cmd *cobra.Command, a *app, b *domain.Book, scope portfolio.Scope, display domain.Currency, evalTo domain.Date) error {
	fx := market.Converter{FX: b.Market.FX}
	lines, err := portfolio.Breakdown(b, evalTo, display, fx)
	if err != nil {
		return err
	}
	envs := portfolio.AssetTree(portfolio.FilterScope(lines, scope))
	if len(envs) == 0 {
		return errors.New("nothing to show in this scope")
	}

	// cells computes the four period returns of one sub-scope.
	cells := func(sc portfolio.Scope) (texts [4]string, signs [4]float64) {
		for i := range texts {
			texts[i] = "-"
		}
		res, err := portfolio.Series(b, sc, domain.Date{}, evalTo, display, fx)
		if err != nil || len(res.Points) < 2 {
			return texts, signs
		}
		for i, name := range perfTreePeriods {
			from, _, perr := perf.PeriodRange(name, evalTo)
			if perr != nil || from.Before(res.Points[0].Date) {
				continue // window predates this line's track record
			}
			pts, fls := window(res, from, evalTo)
			if len(pts) < 2 {
				continue
			}
			r := perf.TWR(pts, fls)
			texts[i], signs[i] = pctSigned(r), r
		}
		return texts, signs
	}
	dashes := func() (t [4]string, s [4]float64) {
		return [4]string{"-", "-", "-", "-"}, s
	}

	type row struct {
		label string
		net   string
		cells [4]string
		signs [4]float64
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }

	var rows []row
	var totNet float64
	for _, env := range envs {
		totNet += env.Net
		envTexts, envSigns := dashes()
		if !env.CashOnly() { // a cash-only envelope has no market performance to show
			envTexts, envSigns = cells(portfolio.EnvelopeScope(scope, env.Account))
		}
		rows = append(rows, row{label: env.Account.Name, net: num(env.Net), cells: envTexts, signs: envSigns})
		if env.CashOnly() { // its lone cash child would only echo the envelope row
			continue
		}
		for _, it := range env.Items {
			t, s := dashes()
			if it.Asset != nil {
				t, s = cells(portfolio.PairScope(env.Account, it.Asset))
			}
			rows = append(rows, row{label: "  " + it.Label(), net: num(it.Net), cells: t, signs: s})
		}
	}
	totTexts, totSigns := cells(scope)

	out := cmd.OutOrStdout()
	colored := a.colorsEnabled(cmd)
	labelW, netW, cellW := len("TOTAL"), len("NET"), len("+12.34%")
	for _, r := range rows {
		labelW = max(labelW, len([]rune(r.label)))
		netW = max(netW, len(r.net))
		for _, c := range r.cells {
			cellW = max(cellW, len(c))
		}
	}
	netW = max(netW, len(num(totNet)))

	pad := func(s string, w int) string {
		for len([]rune(s)) < w {
			s = " " + s
		}
		return s
	}
	printRow := func(label, net string, texts [4]string, signs [4]float64) {
		fmt.Fprintf(out, "%-*s  %s", labelW, label, pad(net, netW))
		for i, c := range texts {
			fmt.Fprintf(out, "  %s", tint(pad(c, cellW), signs[i], colored))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%s - performance (%s), as of %s\n\n", scope.Label, display, evalTo)
	printRow("", "NET", perfTreePeriods, [4]float64{})
	for _, r := range rows {
		printRow(r.label, r.net, r.cells, r.signs)
	}
	fmt.Fprintln(out, strings.Repeat("-", labelW+2+netW+4*(cellW+2)))
	printRow("TOTAL", num(totNet), totTexts, totSigns)
	return nil
}
