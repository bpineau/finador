package portfolio

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

	"finador/internal/domain"
)

// AssetRow is one line of the asset CSV export: an asset and its valuation,
// aggregated across every envelope that holds it.
type AssetRow struct {
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
			Ticker: a.Ticker, Name: a.Name, ISIN: a.ISIN,
			Gross: v.Gross, Net: v.Net, Currency: ccy,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Gross != rows[j].Gross {
			return rows[i].Gross > rows[j].Gross
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// WriteAssetCSV writes rows as CSV with a header: ticker,name,isin,gross,net,currency.
func WriteAssetCSV(w io.Writer, rows []AssetRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"ticker", "name", "isin", "gross", "net", "currency"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.Ticker, r.Name, r.ISIN,
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
