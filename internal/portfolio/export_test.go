package portfolio

import (
	"bytes"
	"encoding/csv"
	"strconv"
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
	if house.Name != "Maison à Rénover" || share.Name != "CW8" {
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
		{Kind: "security", Ticker: "CW8.PA", Name: "Amundi MSCI World", ISIN: "LU1681043599",
			Gross: 7840, Net: 7820.5, Currency: domain.EUR},
		{Kind: "cash", Name: "Livret A", Gross: 8000, Net: 8000, Currency: domain.EUR},
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
	for i, h := range []string{"kind", "ticker", "name", "isin", "gross", "net", "currency"} {
		if recs[0][i] != h {
			t.Errorf("header[%d] = %q, want %q", i, recs[0][i], h)
		}
	}
	for i, c := range []string{"security", "CW8.PA", "Amundi MSCI World", "LU1681043599", "7840.00", "7820.50", "EUR"} {
		if recs[1][i] != c {
			t.Errorf("row1[%d] = %q, want %q", i, recs[1][i], c)
		}
	}
	// the cash row carries kind=cash and no ticker/isin
	if recs[2][0] != "cash" || recs[2][1] != "" || recs[2][3] != "" {
		t.Errorf("cash row malformed: %v", recs[2])
	}
}

func TestWriteAssetTree(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	cw8, _ := b.Asset("cw8")
	cw8.ISIN = "LU1681043599"
	lines, err := Breakdown(b, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteAssetTree(&buf, lines, domain.EUR, at); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// header advertises the currency and valuation date
	if !strings.Contains(out, "Holdings in EUR at 2026-06-05") {
		t.Errorf("missing header:\n%s", out)
	}
	// the PEA envelope holds the cw8 position (with its ISIN) AND cash: not
	// collapsed, so a child line carries "CW8 (LU1681043599)".
	if !strings.Contains(out, "CW8 (LU1681043599)") {
		t.Errorf("expected the held asset with its ISIN:\n%s", out)
	}
	if !strings.Contains(out, "cash") {
		t.Errorf("tracked cash must surface as a child line:\n%s", out)
	}
	// the Immo envelope holds a single property: collapsed onto the account
	// name (a property carries no ISIN).
	if !strings.Contains(out, "Immo") {
		t.Errorf("single-position envelope should collapse onto its name:\n%s", out)
	}
	// a TOTAL footer with a net below the gross (latent tax on the house gain)
	var totG, totN float64
	for _, l := range lines {
		totG += l.Gross
		totN += l.Net
	}
	if totN >= totG {
		t.Fatalf("net %.0f should sit below gross %.0f", totN, totG)
	}
	want := "TOTAL"
	i := strings.LastIndex(out, want)
	if i < 0 {
		t.Fatalf("no TOTAL footer:\n%s", out)
	}
	footer := out[i:]
	if !strings.Contains(footer, strconv.FormatFloat(totG, 'f', 0, 64)) ||
		!strings.Contains(footer, strconv.FormatFloat(totN, 'f', 0, 64)) {
		t.Errorf("TOTAL must show Σgross and Σnet (%s):\n%s", want, footer)
	}
}

func TestAllRowsIncludesCash(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	assets, err := AssetRows(b, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	all, err := AllRows(b, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) <= len(assets) {
		t.Fatalf("AllRows (%d) must add cash rows on top of AssetRows (%d)", len(all), len(assets))
	}
	// the Livret's tracked cash (statement 12000) must surface as a cash row.
	found := false
	for _, r := range all {
		if r.Kind == "cash" && r.Gross > 11999 && r.Gross < 12001 {
			found = true
		}
	}
	if !found {
		t.Error("AllRows must expose the Livret tracked cash (12000)")
	}
}

// ScopedRows with All must be exactly the full export; a narrower scope
// keeps only its own positions and cash.
func TestScopedRows(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	fx := fxStub{}

	all, err := AllRows(b, at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := ScopedRows(b, Scope{Kind: All}, at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != len(all) {
		t.Fatalf("All scope: %d rows, want %d", len(scoped), len(all))
	}
	for i := range all {
		if all[i] != scoped[i] {
			t.Errorf("row %d differs: %+v vs %+v", i, all[i], scoped[i])
		}
	}

	pea, _ := b.Account("pea")
	got, err := ScopedRows(b, Scope{Kind: ByAccount, Account: pea}, at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Kind == "cash" && r.Name != "PEA" {
			t.Errorf("foreign cash row leaked: %+v", r)
		}
		if r.Kind != "cash" && r.Name != "CW8" {
			t.Errorf("foreign asset row leaked: %+v", r)
		}
	}
	// pea holds cw8 (12 units after the edit-free sample trades) and cash.
	if len(got) != 2 {
		t.Fatalf("ByAccount rows = %+v, want cw8 + pea cash", got)
	}
}
