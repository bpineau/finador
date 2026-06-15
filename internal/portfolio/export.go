package portfolio

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

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
