package web

import (
	"net/http"
	"strings"
	"testing"

	"finador/internal/domain"
)

// collidingBook adds an asset whose ID is also a group path, and an account
// whose ID is one too. A CSV import mints ids by slugifying the reference
// (portfolio.ImportCSV), so "bonds" as an id next to a "bonds" group is
// reachable, not hypothetical.
func collidingBook(t *testing.T) *Server {
	t.Helper()
	srv, f := testServer(t)
	b := f.Book
	// Two assets in a "bonds" group, one of them with the id "bonds".
	if err := b.AddAsset(&domain.Asset{ID: "bonds", Kind: domain.Security,
		Name: "Zephyr Bond Fund", Ticker: "ZBF", Currency: domain.EUR, Group: "bonds"}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "gtwr", Kind: domain.Security,
		Name: "Meridia Gilt", Ticker: "GTWR", Currency: domain.EUR, Group: "bonds"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.AssetID{"bonds", "gtwr"} {
		b.Add(domain.Transaction{Date: day(t, "2026-06-01"), Account: "pea", Asset: id,
			Kind: domain.Buy, Quantity: dec("10"), Amount: domain.Money{Amount: dec("1000"), Currency: domain.EUR}})
		b.Market.Price(id).Merge([]domain.PricePoint{
			{Date: domain.Today().AddDays(-20), Close: 100},
			{Date: domain.Today().AddDays(-5), Close: 100},
		})
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	return srv
}

// The assets page scopes each row by asset. Routing the row's id through the
// free-form scope parser gave the GROUP prefix the first try, so the row of
// an asset whose id equals a group path showed that whole group's value -
// here 2000 EUR of "bonds" on a 1000 EUR line.
func TestAssetsPageScopesByAssetNotByGroup(t *testing.T) {
	srv := collidingBook(t)
	code, body := get(t, srv, "/assets")
	if code != http.StatusOK {
		t.Fatalf("GET /assets = %d\n%s", code, excerpt(body))
	}
	row := assetRowOf(t, body, "Zephyr Bond Fund")
	if strings.Contains(row, "2,000") {
		t.Errorf("the row of Zephyr Bond Fund carries its whole group's value:\n%s", row)
	}
	if !strings.Contains(row, "1,000") {
		t.Errorf("the row of Zephyr Bond Fund does not carry its own value:\n%s", row)
	}
}

// /asset/{ref}, /account/{ref} and /group/{ref...} each say which KIND of
// scope they carry; resolving them all through the free-form parser let the
// group tier answer for an asset or an account of the same name.
func TestScopeRoutesResolveTheirOwnKind(t *testing.T) {
	srv := collidingBook(t)

	code, body := get(t, srv, "/asset/bonds")
	if code != http.StatusOK {
		t.Fatalf("GET /asset/bonds = %d\n%s", code, excerpt(body))
	}
	if !strings.Contains(body, "Zephyr Bond Fund") || strings.Contains(body, "2,000") {
		t.Errorf("/asset/bonds rendered the group, not the asset:\n%s", excerpt(body))
	}

	code, body = get(t, srv, "/group/bonds")
	if code != http.StatusOK {
		t.Fatalf("GET /group/bonds = %d\n%s", code, excerpt(body))
	}
	if !strings.Contains(body, "2,000") {
		t.Errorf("/group/bonds does not render the whole group:\n%s", excerpt(body))
	}

	// An unknown reference is still a 404 on every route.
	for _, path := range []string{"/asset/nope", "/account/nope", "/group/nope"} {
		if code, _ := get(t, srv, path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
}

// assetRowOf returns the <tr> block naming the asset.
func assetRowOf(t *testing.T, body, name string) string {
	t.Helper()
	i := strings.Index(body, name)
	if i < 0 {
		t.Fatalf("%q not found in the page", name)
	}
	start := strings.LastIndex(body[:i], "<tr")
	end := strings.Index(body[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("no row around %q", name)
	}
	return body[start : i+end]
}
