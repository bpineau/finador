package portfolio

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"finador/internal/domain"
)

// AssetRow is one line of the CSV export: a holding (a security, a property, or
// an account's cash) and its valuation, aggregated across every envelope.
type AssetRow struct {
	Kind               string // "security" | "property" | "cash"
	Ticker, Name, ISIN string
	Gross, Net         float64
	Currency           domain.Currency
}

// AssetRows returns one row per held asset (non-zero value), each valued across
// all the envelopes holding it, converted to ccy. Sorted by gross descending.
func AssetRows(b *domain.Book, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error) {
	rows := make([]AssetRow, 0, len(b.Assets))
	for _, a := range b.Assets {
		v, err := Value(b, Scope{Kind: ByAsset, Asset: a, Label: a.Name}, at, ccy, fx)
		if err != nil {
			return nil, err
		}
		if v.Gross == 0 && v.Net == 0 {
			continue // not held (or worth nothing): skip
		}
		rows = append(rows, AssetRow{
			Kind:   a.Kind.String(),
			Ticker: a.Ticker, Name: a.Name, ISIN: a.ISIN,
			Gross: v.Gross, Net: v.Net, Currency: ccy,
		})
	}
	sortRows(rows)
	return rows, nil
}

// CashRows returns one row per account with a non-zero tracked cash balance,
// valued in ccy (net subtracts the value-tax for a TaxOnValue envelope).
func CashRows(b *domain.Book, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error) {
	v := &valuer{b: b, fx: fx, at: at, ccy: ccy}
	rows := make([]AssetRow, 0, len(b.Accounts))
	for _, acc := range b.Accounts {
		if !CashTracked(b, acc.ID) {
			continue
		}
		gross, err := v.cashValue(acc)
		if err != nil {
			return nil, err
		}
		if gross == 0 {
			continue
		}
		net := gross
		if acc.Tax.Mode == domain.TaxOnValue {
			net = gross - gross*rate(acc.Tax)
		}
		rows = append(rows, AssetRow{Kind: "cash", Name: acc.Name, Gross: gross, Net: net, Currency: ccy})
	}
	return rows, nil
}

// AllRows is everything the portfolio holds: securities, properties AND
// per-account cash, sorted by gross descending. The CSV export dumps this so
// nothing is hidden.
func AllRows(b *domain.Book, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error) {
	assets, err := AssetRows(b, at, ccy, fx)
	if err != nil {
		return nil, err
	}
	cash, err := CashRows(b, at, ccy, fx)
	if err != nil {
		return nil, err
	}
	rows := append(assets, cash...)
	sortRows(rows)
	return rows, nil
}

func sortRows(rows []AssetRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Gross != rows[j].Gross {
			return rows[i].Gross > rows[j].Gross
		}
		return rows[i].Name < rows[j].Name
	})
}

// WriteAssetCSV writes rows as CSV with a header:
// kind,ticker,name,isin,gross,net,currency.
func WriteAssetCSV(w io.Writer, rows []AssetRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"kind", "ticker", "name", "isin", "gross", "net", "currency"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.Kind, r.Ticker, r.Name, r.ISIN,
			strconv.FormatFloat(r.Gross, 'f', 2, 64),
			strconv.FormatFloat(r.Net, 'f', 2, 64),
			string(r.Currency),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// treeItem is one line under an envelope: a position (Name + ISIN) or its cash.
type treeItem struct {
	name, isin string
	gross, net float64
}

// treeEnvelope is one account and the line-items it holds.
type treeEnvelope struct {
	name       string
	gross, net float64
	items      []treeItem
}

// assetTree groups Breakdown lines by envelope, summing gross/net, with every
// level sorted by gross descending (Σ children == parent).
func assetTree(lines []PositionLine) []treeEnvelope {
	byID := map[domain.AccountID]*treeEnvelope{}
	var order []domain.AccountID
	for _, l := range lines {
		env, ok := byID[l.Account.ID]
		if !ok {
			env = &treeEnvelope{name: l.Account.Name}
			byID[l.Account.ID] = env
			order = append(order, l.Account.ID)
		}
		env.gross += l.Gross
		env.net += l.Net
		it := treeItem{name: "cash", gross: l.Gross, net: l.Net}
		if l.Asset != nil {
			it.name, it.isin = l.Asset.Name, l.Asset.ISIN
		}
		env.items = append(env.items, it)
	}
	out := make([]treeEnvelope, 0, len(order))
	for _, id := range order {
		env := byID[id]
		sort.SliceStable(env.items, func(i, j int) bool {
			if env.items[i].gross != env.items[j].gross {
				return env.items[i].gross > env.items[j].gross
			}
			return env.items[i].name < env.items[j].name
		})
		out = append(out, *env)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].gross != out[j].gross {
			return out[i].gross > out[j].gross
		}
		return out[i].name < out[j].name
	})
	return out
}

// label renders a line-item as "Name (ISIN)", or just "Name" without an ISIN.
func (it treeItem) label() string {
	if it.isin == "" {
		return it.name
	}
	return fmt.Sprintf("%s (%s)", it.name, it.isin)
}

// WriteAssetTree renders the holdings as an indented, envelope-grouped tree with
// two right-aligned columns (gross, after-tax net) in ccy at `at`. An envelope
// holding a single line-item is collapsed onto one row (its ISIN kept, if any).
func WriteAssetTree(w io.Writer, lines []PositionLine, ccy domain.Currency, at domain.Date) error {
	envs := assetTree(lines)

	// Materialize every printable row first, to size the columns to content.
	type row struct {
		text       string // label, already indented
		gross, net float64
		hasNum     bool
	}
	var rows []row
	var totGross, totNet float64
	for _, env := range envs {
		totGross += env.gross
		totNet += env.net
		if len(env.items) == 1 {
			it := env.items[0]
			text := env.name
			if it.isin != "" {
				text = fmt.Sprintf("%s (%s)", env.name, it.isin)
			}
			rows = append(rows, row{text: text, gross: env.gross, net: env.net, hasNum: true})
			continue
		}
		rows = append(rows, row{text: env.name, gross: env.gross, net: env.net, hasNum: true})
		for _, it := range env.items {
			rows = append(rows, row{text: "  " + it.label(), gross: it.gross, net: it.net, hasNum: true})
		}
	}

	num := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }
	const grossHdr, netHdr = "gross", "net"
	labelW, numW := len("TOTAL"), max(len(grossHdr), len(netHdr))
	for _, r := range rows {
		labelW = max(labelW, len(r.text))
		if r.hasNum {
			numW = max(numW, len(num(r.gross)), len(num(r.net)))
		}
	}
	numW = max(numW, len(num(totGross)), len(num(totNet)))

	const gap = 2 // spaces between columns
	line := func(label, g, n string) string {
		return fmt.Sprintf("%-*s%*s%*s\n", labelW+gap, label,
			numW, g, numW+gap, n)
	}

	var bld strings.Builder
	fmt.Fprintf(&bld, "Holdings in %s at %s\n\n", ccy, at)
	bld.WriteString(line("", grossHdr, netHdr))
	for _, r := range rows {
		bld.WriteString(line(r.text, num(r.gross), num(r.net)))
	}
	bld.WriteString(strings.Repeat("-", labelW+gap+numW+numW+gap) + "\n")
	bld.WriteString(line("TOTAL", num(totGross), num(totNet)))

	_, err := io.WriteString(w, bld.String())
	return err
}
