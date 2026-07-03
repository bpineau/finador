package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"finador/internal/domain"
)

func TestSortSectionsPropertyLast(t *testing.T) {
	secs := []assetSection{
		{Group: "realty", Gross: 500000, PropertyOnly: true},
		{Group: "equities", Gross: 10000},
		{Group: "bonds", Gross: 20000},
		{Group: "land", Gross: 900000, PropertyOnly: true},
	}
	sortSections(secs)
	got := []string{secs[0].Group, secs[1].Group, secs[2].Group, secs[3].Group}
	want := []string{"bonds", "equities", "land", "realty"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestAssetsListAndCreate(t *testing.T) {
	srv, f := testServer(t)

	// the assets page shows the fixture asset, the creation form, and the manage list.
	code, body := get(t, srv, "/assets")
	if code != 200 {
		t.Fatalf("GET /assets = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{"Amundi MSCI World", "new asset", "manage assets", "<form", `name="kind"`, `name="withholding"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets: %q missing", want)
		}
	}

	// create a US security with an ISIN, an alias, a group and a withholding rate.
	code, body, loc := postForm(t, srv, "/assets", url.Values{
		"name": {"Vanguard S&P 500"}, "kind": {"security"}, "ticker": {"VUSA.AS"},
		"isin": {"IE00B3XXRP09"}, "ccy": {"USD"}, "group": {"actions/us"},
		"aliases": {"sp500, vusa"}, "withholding": {"15%"},
	})
	if code != 303 || !strings.HasPrefix(loc, "/assets") {
		t.Fatalf("POST /assets = %d → %q\n%s", code, loc, excerpt(body))
	}
	a, err := f.Book.Asset("sp500") // resolves by alias
	if err != nil {
		t.Fatalf("created asset not found by alias: %v", err)
	}
	if a.Name != "Vanguard S&P 500" || a.Kind != domain.Security || a.Currency != "USD" {
		t.Errorf("created asset = %+v", a)
	}
	if a.Ticker != "VUSA.AS" || a.ISIN != "IE00B3XXRP09" || a.Group != "actions/us" {
		t.Errorf("created asset fields = %+v", a)
	}
	if a.Withholding != 0.15 {
		t.Errorf("withholding = %v, want 0.15", a.Withholding)
	}
	if len(a.Aliases) != 2 || a.Aliases[0] != "sp500" || a.Aliases[1] != "vusa" {
		t.Errorf("aliases = %v, want [sp500 vusa]", a.Aliases)
	}

	// empty name → 400, nothing added.
	before := len(f.Book.Assets)
	code, body, _ = postForm(t, srv, "/assets", url.Values{"name": {""}, "ccy": {"EUR"}})
	if code != 400 || !strings.Contains(body, "name is required") {
		t.Fatalf("empty name = %d\n%s", code, excerpt(body))
	}
	if len(f.Book.Assets) != before {
		t.Error("invalid create added an asset")
	}

	// collision: re-using the existing ticker is rejected.
	code, _, _ = postForm(t, srv, "/assets", url.Values{"name": {"Dup"}, "ticker": {"CW8.PA"}, "ccy": {"EUR"}})
	if code != 400 {
		t.Errorf("colliding ticker = %d, want 400", code)
	}
}

func TestAssetEditWeb(t *testing.T) {
	srv, f := testServer(t)

	// edit page pre-fills name, the selected kind and the ticker.
	code, body := get(t, srv, "/assets/cw8/edit")
	if code != 200 {
		t.Fatalf("GET edit = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{`value="Amundi MSCI World"`, `value="security" selected`, `value="CW8.PA"`} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form: %q missing\n%s", want, excerpt(body))
		}
	}

	// change name + group, add an alias, set a withholding rate.
	code, body, loc := postForm(t, srv, "/assets/cw8/edit", url.Values{
		"name": {"World Tracker"}, "kind": {"security"}, "ticker": {"CW8.PA"},
		"ccy": {"EUR"}, "group": {"actions/global"}, "aliases": {"cw8, monde"}, "withholding": {"30"},
	})
	if code != 303 || !strings.HasPrefix(loc, "/assets") {
		t.Fatalf("POST edit = %d → %q\n%s", code, loc, excerpt(body))
	}
	a, _ := f.Book.Asset("cw8")
	if a.Name != "World Tracker" || a.Group != "actions/global" || a.Withholding != 0.30 {
		t.Errorf("after edit: %+v", a)
	}
	if _, err := f.Book.Asset("monde"); err != nil {
		t.Errorf("added alias not resolvable: %v", err)
	}

	// unknown asset → 404.
	if code, _ := get(t, srv, "/assets/nope/edit"); code != 404 {
		t.Errorf("GET edit unknown = %d", code)
	}
}

func TestAssetDeleteGuard(t *testing.T) {
	srv, f := testServer(t)

	// cw8 has a buy transaction → delete refused with a clear message, asset kept.
	code, body, _ := postForm(t, srv, "/assets/cw8/delete", url.Values{})
	if code != 400 || !strings.Contains(body, "delete its transactions first") {
		t.Fatalf("guarded delete = %d\n%s", code, excerpt(body))
	}
	if _, err := f.Book.Asset("cw8"); err != nil {
		t.Error("guarded asset was removed")
	}

	// a fresh asset with no transactions deletes cleanly.
	id := domain.NewID()
	if err := f.Book.AddAsset(&domain.Asset{ID: domain.AssetID(id), Kind: domain.Property,
		Name: "Maison", Currency: domain.EUR, Group: "immo"}); err != nil {
		t.Fatal(err)
	}
	code, _, loc := postForm(t, srv, fmt.Sprintf("/assets/%s/delete", id), url.Values{})
	if code != 303 || !strings.HasPrefix(loc, "/assets") {
		t.Fatalf("clean delete = %d → %q", code, loc)
	}
	if _, err := f.Book.Asset(id); err == nil {
		t.Error("asset not removed")
	}
}

// TestAssetsStaleQuoteShown: an instrument whose last published close
// predates today (NAV lag) shows that close's day move, muted and dated,
// instead of an empty 1D cell.
func TestAssetsStaleQuoteShown(t *testing.T) {
	srv, _ := testServer(t) // cw8's last close is today-5: 550 → 560 (+1.82%)

	code, body := get(t, srv, "/assets")
	if code != 200 {
		t.Fatalf("GET /assets = %d\n%s", code, excerpt(body))
	}
	last := domain.Today().AddDays(-5)
	wantDate := fmt.Sprintf("%02d-%02d", last.Month, last.Day)
	for _, want := range []string{"stale-1d", "1.82%", wantDate} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets: %q missing (stale 1D cell):\n%s", want, excerpt(body))
		}
	}
}
