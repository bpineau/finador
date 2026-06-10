package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/store"
)

// fakeSource: deterministic data, never hits the network in web tests.
type fakeSource struct{}

func (fakeSource) Resolve(context.Context, string) (market.SymbolInfo, error) {
	return market.SymbolInfo{}, domain.ErrNotFound
}

func (fakeSource) Daily(_ context.Context, sym string, _ domain.Date) (market.DailyData, error) {
	day := func(s string) domain.Date { d, _ := domain.ParseDate(s); return d }
	if sym == "CW8.PA" {
		return market.DailyData{Currency: domain.EUR, Closes: []domain.PricePoint{
			{Date: day("2026-06-01"), Close: 550}, {Date: day("2026-06-05"), Close: 560},
		}}, nil
	}
	return market.DailyData{}, domain.ErrNotFound
}

func day(t *testing.T, s string) domain.Date {
	t.Helper()
	d, err := domain.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// testServer builds a real store in /tmp with a representative book.
func testServer(t *testing.T) (*Server, *store.File) {
	t.Helper()
	f, err := store.Create(filepath.Join(t.TempDir(), "web.fin"), "test")
	if err != nil {
		t.Fatal(err)
	}
	b := f.Book
	if err := b.AddAccount(&domain.Account{ID: "pea", Name: "PEA Zephyr", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	pea, _ := b.Account("pea")
	pea.Tax, _ = domain.ParseTaxRule("gains:17.2%")
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions/monde"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: day(t, "2026-01-10"), Account: "pea", Kind: domain.Deposit,
		Amount: domain.Money{Amount: dec("5000"), Currency: domain.EUR}})
	b.Add(domain.Transaction{Date: day(t, "2026-06-01"), Account: "pea", Asset: "cw8", Kind: domain.Buy,
		Quantity: dec("10"), Amount: domain.Money{Amount: dec("5500"), Currency: domain.EUR}})
	b.Market.Price("cw8").Merge([]domain.PricePoint{
		{Date: day(t, "2026-06-01"), Close: 550}, {Date: day(t, "2026-06-05"), Close: 560},
	})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f, fakeSource{}, true) // offline: no auto-refresh in tests
	return srv, f
}

func get(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestDashboard(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d\n%s", code, body)
	}
	for _, want := range []string{
		"FINADOR",   // brand
		"net worth", // hero label
		"5", "100",  // 5,100.00 € (English formatting with comma)
		"€", // currency suffix
		"style.css",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q missing", want)
		}
	}
	// exact English format: comma thousands separator, point decimal, U+00A0 before symbol
	if !strings.Contains(body, "5,100.00") {
		t.Errorf("English amount format missing (5,100.00):\n%s", excerpt(body))
	}
}

func TestStyleSheet(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/style.css")
	if code != http.StatusOK || !strings.Contains(body, "--papier") || !strings.Contains(body, "--encre") {
		t.Fatalf("style.css = %d, palette missing", code)
	}
}

func TestFavicon(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/x-icon" {
		t.Fatalf("favicon = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.Bytes()
	if len(body) < 6 || body[0] != 0 || body[1] != 0 || body[2] != 1 || body[3] != 0 {
		t.Error("not an ICO (bad magic)")
	}
	// le lien force le rechargement par une URL versionnée
	if _, home := get(t, srv, "/"); !strings.Contains(home, `href="/favicon.ico?v=1"`) {
		t.Error("base template must reference /favicon.ico?v=1 (cache-buster)")
	}
}

func TestNotFound(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/nimporte/quoi")
	if code != http.StatusNotFound || !strings.Contains(body, "not found") {
		t.Fatalf("404 = %d\n%s", code, body)
	}
}

func TestDashboardComplete(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		"<svg",           // inline curve
		"allocation",     // section
		"actions",        // leading group (linked to /group/actions)
		"/group/actions", // scope link
		"cash",
		"performance", // perf section
		"inception",   // period table row
		"TWR",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q missing", want)
		}
	}
	// curves carry theme colours
	if !strings.Contains(body, "#1c1914") || !strings.Contains(body, "#1e6e4e") {
		t.Error("curve colours out of theme")
	}
}

func TestScopeViews(t *testing.T) {
	srv, _ := testServer(t)
	for path, want := range map[string][]string{
		"/group/actions":       {"actions", "<svg", "Amundi MSCI World", "performance"},
		"/group/actions/monde": {"actions/monde"},
		"/account/pea":         {"PEA Zephyr", "cash", "recent transactions"},
		"/asset/cw8":           {"Amundi MSCI World", "PEA Zephyr"},
	} {
		code, body := get(t, srv, path)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", path, code)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: %q missing", path, w)
			}
		}
	}
	// unknown scope → clean 404
	if code, body := get(t, srv, "/asset/inexistant"); code != http.StatusNotFound || !strings.Contains(body, "unknown scope") {
		t.Errorf("unknown scope = %d\n%s", code, excerpt(body))
	}
}

func excerpt(s string) string {
	if len(s) > 800 {
		return s[:800]
	}
	return s
}

func TestImportUpload(t *testing.T) {
	srv, f := testServer(t)
	code, body := get(t, srv, "/import")
	if code != http.StatusOK || !strings.Contains(body, "multipart/form-data") {
		t.Fatalf("GET /import = %d", code)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "txs.csv")
	part.Write([]byte("date,kind,account,asset,quantity,price,amount,currency,group,note\n" +
		"2026-02-01,buy,PEA Zephyr,CW8.PA,3,540,,EUR,actions/monde,import web\n"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /import = %d\n%s", rec.Code, excerpt(rec.Body.String()))
	}
	if len(f.Book.Transactions) != 3 {
		t.Errorf("transactions = %d, want 3", len(f.Book.Transactions))
	}
	// flash result visible after redirect
	code, body = get(t, srv, rec.Header().Get("Location"))
	if code != http.StatusOK || !strings.Contains(body, "1 imported") {
		t.Errorf("flash missing:\n%s", excerpt(body))
	}
}

func TestRefreshButtonOffline(t *testing.T) {
	srv, _ := testServer(t) // offline server
	code, _, loc := postForm(t, srv, "/refresh", url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(loc, "offline") && !strings.Contains(loc, "cannot+refresh") && !strings.Contains(loc, "cannot%20refresh") {
		t.Fatalf("refresh offline = %d → %q", code, loc)
	}
}

func postForm(t *testing.T, srv *Server, path string, form url.Values) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header().Get("Location")
}

func TestTxListAndAdd(t *testing.T) {
	srv, f := testServer(t)
	code, body := get(t, srv, "/tx")
	if code != http.StatusOK || !strings.Contains(body, "buy") || !strings.Contains(body, "deposit") {
		t.Fatalf("GET /tx = %d\n%s", code, excerpt(body))
	}
	// form offers accounts and assets
	for _, want := range []string{"PEA Zephyr", "Amundi MSCI World", "<form", `name="kind"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/tx: %q missing", want)
		}
	}

	// add a buy → 303 then visible, persisted in file
	code, body, loc := postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"2"}, "amount": {"1115"}, "note": {"via web"},
	})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("POST /tx = %d → %q\n%s", code, loc, excerpt(body))
	}
	if _, body = get(t, srv, "/tx"); !strings.Contains(body, "via web") {
		t.Error("added transaction not visible")
	}
	if len(f.Book.Transactions) != 3 {
		t.Errorf("transactions = %d, want 3", len(f.Book.Transactions))
	}

	// invalid input → 400 with message, nothing written
	code, body, _ = postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"abc"}, "amount": {"10"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "quantity") {
		t.Fatalf("POST invalid = %d\n%s", code, excerpt(body))
	}
	if len(f.Book.Transactions) != 3 {
		t.Error("invalid input wrote something")
	}
}

func TestDashboardByAccount(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/?by=account")
	if code != http.StatusOK {
		t.Fatalf("GET /?by=account = %d", code)
	}
	if !strings.Contains(body, "PEA Zephyr") || !strings.Contains(body, "/account/") {
		t.Errorf("by-account breakdown missing:\n%s", excerpt(body))
	}
	// toggle link is present
	if !strings.Contains(body, "by group") {
		t.Errorf("toggle link missing")
	}
}

func TestStylesheetThemesLinksAndTrees(t *testing.T) {
	srv, _ := testServer(t)
	_, css := get(t, srv, "/style.css")
	for _, want := range []string{"main a {", "details", "summary", "--garance"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css: %q missing", want)
		}
	}
}

func TestIntersectionScopeView(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/account/pea/group/actions")
	if code != http.StatusOK {
		t.Fatalf("GET intersection = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{"PEA Zephyr", "actions", "Amundi MSCI World", "performance", "<svg"} {
		if !strings.Contains(body, want) {
			t.Errorf("intersection: %q missing", want)
		}
	}
	// cash from the account must NOT appear in a crossed scope
	if strings.Contains(body, "cash") {
		t.Error("cash present in an account∩group intersection scope")
	}
	// unknown account → 404
	if code, _ := get(t, srv, "/account/zz9/group/actions"); code != http.StatusNotFound {
		t.Errorf("unknown intersection = %d", code)
	}
}

func TestDashboardTreeModes(t *testing.T) {
	srv, _ := testServer(t)
	// group mode (default): tree with details and intersection link
	_, body := get(t, srv, "/")
	for _, want := range []string{"<details", "/account/pea/group/actions"} {
		if !strings.Contains(body, want) {
			t.Errorf("group mode: %q missing", want)
		}
	}
	// "by asset" tab must be GONE
	if strings.Contains(body, "by asset") {
		t.Error("group mode: 'by asset' tab must not appear")
	}
	// ?by=asset is silently normalized to group: still shows the tree
	_, body = get(t, srv, "/?by=asset")
	if !strings.Contains(body, "<details") {
		t.Errorf("asset mode normalized to group: tree missing:\n%s", excerpt(body))
	}
	if strings.Contains(body, "by asset") {
		t.Error("?by=asset should not render by-asset tab")
	}
	// active tab is not a link
	if !strings.Contains(body, `actif-onglet`) {
		t.Errorf("active tab not marked")
	}
}

func TestTxEditWeb(t *testing.T) {
	srv, f := testServer(t)
	id := f.Book.Transactions[1].ID // the cw8 buy

	code, body := get(t, srv, fmt.Sprintf("/tx/%d/edit", id))
	if code != http.StatusOK {
		t.Fatalf("GET edit = %d", code)
	}
	for _, want := range []string{`value="2026-06-01"`, `value="10"`, "Amundi MSCI World", "entry slip"} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form: %q missing", want)
		}
	}

	// update quantity + amount
	code, body, loc := postForm(t, srv, fmt.Sprintf("/tx/%d/edit", id), url.Values{
		"date": {"2026-06-01"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"12"}, "amount": {"6600"}, "note": {"edited via web"},
	})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("POST edit = %d → %q\n%s", code, loc, excerpt(body))
	}
	tx, err := f.Book.Tx(id)
	if err != nil || tx.Quantity.String() != "12" || tx.Amount.Amount.String() != "6600" || tx.Note != "edited via web" {
		t.Fatalf("tx after edit: %+v, %v", tx, err)
	}

	// validation failure → 400, nothing changed
	code, body, _ = postForm(t, srv, fmt.Sprintf("/tx/%d/edit", id), url.Values{
		"date": {"not-a-date"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"12"}, "amount": {"6600"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("POST edit invalid = %d", code)
	}
	// unknown id → 404
	if code, _ := get(t, srv, "/tx/999/edit"); code != http.StatusNotFound {
		t.Errorf("GET edit unknown = %d", code)
	}
	// /tx list has the edit link
	if _, body := get(t, srv, "/tx"); !strings.Contains(body, fmt.Sprintf("/tx/%d/edit", id)) {
		t.Errorf("edit link missing from /tx")
	}
}

func TestAssetsPage(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/assets")
	if code != http.StatusOK {
		t.Fatalf("GET /assets = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{
		"actions/monde",        // en-tête de section : chemin complet
		"/group/actions/monde", // cliquable
		"Amundi MSCI World",    // une ligne d'actif
		"/asset/cw8",           // nom cliquable
		"assets-table",         // table dense
		"GROSS", "NET", "1W", "1M", "1Y",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets: %q missing", want)
		}
	}
	// trois sparklines pour l'unique actif valorisé
	if got := strings.Count(body, "<polyline"); got != 3 {
		t.Errorf("polylines = %d, want 3", got)
	}
	// montants brut et net de la position (10×560 = 5600 ; base 5500 → gain 100
	// → tax 17.20 si gains:17.2% → net 5582.80)
	for _, want := range []string{"5,600.00", "5,582.80"} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets amounts: %q missing", want)
		}
	}
	// densité : sparkline 72×20, nom indenté
	if !strings.Contains(body, `viewBox="0 0 72 20"`) {
		t.Error("sparklines should be 72x20")
	}
	// table-layout:fixed lit la PREMIÈRE rangée : les th doivent porter la classe
	if !strings.Contains(body, `<th class="sparkcell">1W</th>`) {
		t.Error("header cells must carry sparkcell so fixed layout sizes the columns")
	}
	if !strings.Contains(body, `class="refresh-quiet"`) {
		t.Error("quiet refresh button should live at the bottom of /assets")
	}
	_, css := get(t, srv, "/style.css")
	for _, want := range []string{".assets-table .asset-name { padding-left:", "width: 76px"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css: %q missing", want)
		}
	}
	// l'onglet est dans la manchette de toutes les pages
	if _, home := get(t, srv, "/"); !strings.Contains(home, `href="/assets"`) {
		t.Error("nav link /assets missing on dashboard")
	}
}

func TestChartRanges(t *testing.T) {
	srv, _ := testServer(t)
	_, full := get(t, srv, "/")
	code, m1 := get(t, srv, "/?range=1m")
	if code != http.StatusOK {
		t.Fatalf("range=1m = %d", code)
	}
	// le sélecteur est présent, l'actif est marqué, les liens préservent by/range
	for _, want := range []string{`class="ranges"`, `active-range`, `range=3m`} {
		if !strings.Contains(m1, want) {
			t.Errorf("?range=1m: %q missing", want)
		}
	}
	// la courbe 1m diffère de la courbe complète (moins de points)
	// On compare le nombre de virgules dans le HTML comme proxy du nombre de
	// points SVG : une courbe plus courte produit moins de coordonnées "x,y".
	// Si la série de test est trop courte pour la différence soit visible avec
	// des virgules (ex. même période), on peut toujours vérifier la présence du
	// sélecteur — l'assertion de virgules est commentée et remplacée par une
	// assertion que full contient au moins autant de virgules.
	if strings.Count(m1, ",") >= strings.Count(full, ",") {
		t.Error("1m curve should carry fewer svg points than the full curve")
	}
	// les onglets de répartition préservent le range
	if !strings.Contains(m1, "by=account&amp;range=1m") {
		t.Errorf("tabs should carry the range:\n%s", excerpt(m1))
	}
	// portée : le sélecteur existe aussi
	if _, sc := get(t, srv, "/asset/cw8?range=3m"); !strings.Contains(sc, "active-range") {
		t.Error("scope pages should have the range selector")
	}
	// invalide → all (200, pas d'erreur)
	if code, _ := get(t, srv, "/?range=zz"); code != http.StatusOK {
		t.Errorf("invalid range = %d", code)
	}
}

func TestTxCreateOnTheFly(t *testing.T) {
	srv, f := testServer(t)
	code, _, loc := postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"deposit"}, "account": {"Brand New Bank"},
		"amount": {"500"},
	})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("create-on-the-fly = %d → %q", code, loc)
	}
	if _, err := f.Book.Account("Brand New Bank"); err != nil {
		t.Fatalf("account not created: %v", err)
	}
	// actif à la volée
	code, _, _ = postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"},
		"asset": {"NVDA"}, "qty": {"2"}, "amount": {"300"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("asset on the fly = %d", code)
	}
	if a, err := f.Book.Asset("NVDA"); err != nil || a.Kind != domain.Security {
		t.Fatalf("asset not created: %v %v", a, err)
	}
	// le formulaire est en datalist, plus en select
	_, body := get(t, srv, "/tx")
	if !strings.Contains(body, "<datalist") || strings.Contains(body, `<select id="account"`) {
		t.Error("account field should be a datalist input")
	}
}

func TestTxDelete(t *testing.T) {
	srv, f := testServer(t)
	id := f.Book.Transactions[0].ID
	code, _, loc := postForm(t, srv, fmt.Sprintf("/tx/%d/delete", id), url.Values{})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("delete = %d → %q", code, loc)
	}
	if len(f.Book.Transactions) != 1 {
		t.Errorf("transactions = %d, want 1", len(f.Book.Transactions))
	}
	// unknown id → 404
	if code, _, _ := postForm(t, srv, "/tx/999/delete", url.Values{}); code != http.StatusNotFound {
		t.Errorf("delete unknown = %d", code)
	}
}
