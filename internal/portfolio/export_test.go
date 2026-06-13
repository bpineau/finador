package portfolio

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"finador/internal/domain"
)

func TestAssetRows(t *testing.T) {
	b := valuationBook(t)
	cw8, _ := b.Asset("cw8")
	cw8.ISIN = "LU1681043599" // exercise the ISIN column
	// an asset nobody holds must be skipped (gross == net == 0).
	if err := b.AddAsset(&domain.Asset{ID: "ghost", Kind: domain.Security,
		Name: "Never bought", Ticker: "GHST", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}

	rows, err := AssetRows(b, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (ghost skipped):\n%+v", len(rows), rows)
	}
	// sorted by gross desc: the house (450 000) then cw8 (14 × 560 = 7840).
	house, share := rows[0], rows[1]
	if house.Name != "Maison à Achères" || share.Name != "CW8" {
		t.Fatalf("order = %q, %q", house.Name, share.Name)
	}
	approx(t, "house gross", house.Gross, 450000)
	approx(t, "house net", house.Net, 450000-15000) // gains:30% on the 50 000 gain
	if house.Ticker != "" || house.ISIN != "" {
		t.Errorf("a property carries no ticker/isin: %+v", house)
	}
	approx(t, "cw8 gross", share.Gross, 7840)
	if share.Ticker != "CW8.PA" || share.ISIN != "LU1681043599" {
		t.Errorf("cw8 ticker/isin = %q/%q", share.Ticker, share.ISIN)
	}
	if share.Net >= share.Gross {
		t.Errorf("cw8 net %.2f should sit below gross %.2f (latent tax)", share.Net, share.Gross)
	}
	if share.Currency != domain.EUR {
		t.Errorf("currency = %s", share.Currency)
	}
}

func TestWriteAssetCSV(t *testing.T) {
	rows := []AssetRow{
		{Ticker: "CW8.PA", Name: "Amundi MSCI World", ISIN: "LU1681043599",
			Gross: 7840, Net: 7820.5, Currency: domain.EUR},
		{Name: "Maison à Achères", Gross: 450000, Net: 435000, Currency: domain.EUR},
	}
	var buf bytes.Buffer
	if err := WriteAssetCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v\n%s", err, buf.String())
	}
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3 (header + 2 rows)", len(recs))
	}
	for i, h := range []string{"ticker", "name", "isin", "gross", "net", "currency"} {
		if recs[0][i] != h {
			t.Errorf("header[%d] = %q, want %q", i, recs[0][i], h)
		}
	}
	for i, c := range []string{"CW8.PA", "Amundi MSCI World", "LU1681043599", "7840.00", "7820.50", "EUR"} {
		if recs[1][i] != c {
			t.Errorf("row1[%d] = %q, want %q", i, recs[1][i], c)
		}
	}
	if recs[2][0] != "" || recs[2][2] != "" {
		t.Errorf("property row should have empty ticker/isin: %v", recs[2])
	}
}
