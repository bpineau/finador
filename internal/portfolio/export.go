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
	// Note labels an estimated price (see Prices), in the CSV's last column
	// too: the downloaded file is read away from the page that labelled it.
	Note string
}

// ScopedRows values what the scope holds - securities and properties
// aggregated per asset, plus one cash row per account whose cash the scope
// keeps - sorted by gross descending. With an All scope it is the whole
// export, CLI and web alike.
func ScopedRows(b *domain.Book, s Scope, at domain.Date, ccy domain.Currency, fx FX) ([]AssetRow, error) {
	lines, err := ScopedBreakdown(b, s, at, ccy, fx)
	if err != nil {
		return nil, err
	}
	var rows []AssetRow
	assetRows := map[domain.AssetID]int{} // asset → index in rows, for aggregation
	for _, l := range lines {
		if l.Asset == nil {
			rows = append(rows, AssetRow{Kind: "cash", Name: l.Account.Name, Gross: l.Gross, Net: l.Net, Currency: ccy})
			continue
		}
		if i, ok := assetRows[l.Asset.ID]; ok {
			rows[i].Gross += l.Gross
			rows[i].Net += l.Net
			continue
		}
		assetRows[l.Asset.ID] = len(rows)
		rows = append(rows, AssetRow{
			Kind:   l.Asset.Kind.String(),
			Ticker: l.Asset.Ticker, Name: l.Asset.Name, ISIN: l.Asset.ISIN,
			Gross: l.Gross, Net: l.Net, Currency: ccy,
			Note: l.Note,
		})
	}
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
// kind,ticker,name,isin,gross,net,currency,note.
func WriteAssetCSV(w io.Writer, rows []AssetRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"kind", "ticker", "name", "isin", "gross", "net", "currency", "note"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.Kind, r.Ticker, r.Name, r.ISIN,
			strconv.FormatFloat(r.Gross, 'f', 2, 64),
			strconv.FormatFloat(r.Net, 'f', 2, 64),
			string(r.Currency),
			r.Note,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// TreeItem is one line under an envelope: a valued position, or the
// envelope's cash when Asset is nil.
type TreeItem struct {
	Asset      *domain.Asset // nil: the envelope's cash line
	Gross, Net float64
}

// TreeEnvelope is one account and the line-items it holds
// (Σ Items == envelope).
type TreeEnvelope struct {
	Account    *domain.Account
	Gross, Net float64
	Items      []TreeItem
}

// AssetTree groups Breakdown lines by envelope, summing gross/net, with
// every level sorted by gross descending then name. The raw material of the
// tree views (export --tree, value --tree, perf --tree).
func AssetTree(lines []PositionLine) []TreeEnvelope {
	byID := map[domain.AccountID]*TreeEnvelope{}
	var order []domain.AccountID
	for _, l := range lines {
		env, ok := byID[l.Account.ID]
		if !ok {
			env = &TreeEnvelope{Account: l.Account}
			byID[l.Account.ID] = env
			order = append(order, l.Account.ID)
		}
		env.Gross += l.Gross
		env.Net += l.Net
		env.Items = append(env.Items, TreeItem{Asset: l.Asset, Gross: l.Gross, Net: l.Net})
	}
	out := make([]TreeEnvelope, 0, len(order))
	for _, id := range order {
		env := byID[id]
		sort.SliceStable(env.Items, func(i, j int) bool {
			if env.Items[i].Gross != env.Items[j].Gross {
				return env.Items[i].Gross > env.Items[j].Gross
			}
			return env.Items[i].name() < env.Items[j].name()
		})
		out = append(out, *env)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Gross != out[j].Gross {
			return out[i].Gross > out[j].Gross
		}
		return out[i].Account.Name < out[j].Account.Name
	})
	return out
}

// Tree is the envelope-grouped view of a scope, with its exact figures: each
// envelope's Net and the total follow the per-envelope tax rule (Value), while
// the item lines keep the per-position estimate. TaxNote says so when the two
// readings differ; Notes are the price estimates the lines use.
type Tree struct {
	Envelopes  []TreeEnvelope
	Gross, Net float64
	TaxNote    string
	Notes      []string
}

// ScopedTree builds the Tree of scope at `at` in ccy: the lines scope keeps,
// grouped by envelope, with each envelope's net and the total read from Value
// so a tree view never shows a total `value` would contradict.
func ScopedTree(b *domain.Book, scope Scope, at domain.Date, ccy domain.Currency, fx FX) (Tree, error) {
	lines, err := ScopedBreakdown(b, scope, at, ccy, fx)
	if err != nil {
		return Tree{}, err
	}
	t := Tree{Envelopes: AssetTree(lines), Notes: Notes(lines)}
	for i := range t.Envelopes {
		v, err := Value(b, EnvelopeScope(scope, t.Envelopes[i].Account), at, ccy, fx)
		if err != nil {
			return Tree{}, err
		}
		t.Envelopes[i].Net = v.Net
	}
	total, err := Value(b, scope, at, ccy, fx)
	if err != nil {
		return Tree{}, err
	}
	t.Gross, t.Net, t.TaxNote = total.Gross, total.Net, total.TaxNote
	return t, nil
}

// name is the item's sort key: the asset name, "cash" for a cash line.
func (it TreeItem) name() string {
	if it.Asset == nil {
		return "cash"
	}
	return it.Asset.Name
}

// Label renders a line-item as "Name (ISIN)", "Name" without an ISIN, or
// "cash" for a cash line.
func (it TreeItem) Label() string {
	if it.Asset == nil {
		return "cash"
	}
	if it.Asset.ISIN == "" {
		return it.Asset.Name
	}
	return fmt.Sprintf("%s (%s)", it.Asset.Name, it.Asset.ISIN)
}

// CashOnly reports whether the envelope holds nothing but cash. Such an
// envelope, whose sole indented child would merely echo the account line,
// is rendered as a single row by the tree views instead of a header.
func (e TreeEnvelope) CashOnly() bool {
	for _, it := range e.Items {
		if it.Asset != nil {
			return false
		}
	}
	return true
}

// WriteAssetTree renders a Tree as an indented, envelope-grouped list with two
// right-aligned columns (gross, after-tax net) in ccy at `at`. Each envelope is
// a bare header over its indented holdings, which carry the figures. A
// cash-only envelope, whose lone child would merely echo it, collapses onto a
// single numbered row. The TOTAL is the tree's exact one (see ScopedTree).
func WriteAssetTree(w io.Writer, tree Tree, ccy domain.Currency, at domain.Date) error {
	envs := tree.Envelopes

	// Materialize every printable row first, to size the columns to content.
	type row struct {
		text       string // label, already indented
		gross, net float64
		hasNum     bool
	}
	var rows []row
	totGross, totNet := tree.Gross, tree.Net
	for _, env := range envs {
		if env.CashOnly() {
			rows = append(rows, row{text: env.Account.Name, gross: env.Gross, net: env.Net, hasNum: true})
			continue
		}
		rows = append(rows, row{text: env.Account.Name}) // bare header, no total of its own
		for _, it := range env.Items {
			rows = append(rows, row{text: "  " + it.Label(), gross: it.Gross, net: it.Net, hasNum: true})
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
	fmt.Fprintf(&bld, "Holdings in %s at %s\n", ccy, at)
	bld.WriteString(line("", grossHdr, netHdr))
	for _, r := range rows {
		if r.hasNum {
			bld.WriteString(line(r.text, num(r.gross), num(r.net)))
		} else {
			bld.WriteString(line(r.text, "", "")) // header row: label only
		}
	}
	bld.WriteString(strings.Repeat("-", labelW+gap+numW+numW+gap) + "\n")
	bld.WriteString(line("TOTAL", num(totGross), num(totNet)))

	_, err := io.WriteString(w, bld.String())
	return err
}
