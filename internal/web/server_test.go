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

// fakeSource : données déterministes, jamais de réseau dans les tests web.
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

// testServer construit un store réel en /tmp avec un livre représentatif.
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
	srv := NewServer(f, fakeSource{}, true) // offline : pas de refresh auto en test
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
		"FINADOR",    // manchette
		"patrimoine", // libellé héros
		"5", "100",   // 5 100,00 € (formatage français, espaces fines)
		"€", // devise suffixée
		"style.css",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q manquant", want)
		}
	}
	// le formatage français exact : espace fine U+202F entre milliers, virgule
	if !strings.Contains(body, "5 100,00") {
		t.Errorf("montant français manquant (5\\u202f100,00):\n%s", excerpt(body))
	}
}

func TestStyleSheet(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/style.css")
	if code != http.StatusOK || !strings.Contains(body, "--papier") || !strings.Contains(body, "--encre") {
		t.Fatalf("style.css = %d, palette absente", code)
	}
}

func TestNotFound(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/nimporte/quoi")
	if code != http.StatusNotFound || !strings.Contains(body, "introuvable") {
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
		"<svg",           // courbe inline
		"répartition",    // section
		"actions",        // groupe de tête (lié vers /group/actions)
		"/group/actions", // lien de portée
		"liquidités",
		"performance", // section perfs
		"origine",     // ligne du tableau de périodes
		"TWR",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q manquant", want)
		}
	}
	// les courbes portent les couleurs du thème
	if !strings.Contains(body, "#1c1914") || !strings.Contains(body, "#1e6e4e") {
		t.Error("couleurs de courbe hors thème")
	}
}

func TestScopeViews(t *testing.T) {
	srv, _ := testServer(t)
	for path, want := range map[string][]string{
		"/group/actions":       {"actions", "<svg", "Amundi MSCI World", "performance"},
		"/group/actions/monde": {"actions/monde"},
		"/account/pea":         {"PEA Zephyr", "liquidités", "transactions récentes"},
		"/asset/cw8":           {"Amundi MSCI World", "PEA Zephyr"},
	} {
		code, body := get(t, srv, path)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", path, code)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: %q manquant", path, w)
			}
		}
	}
	// portée inconnue → 404 propre
	if code, body := get(t, srv, "/asset/inexistant"); code != http.StatusNotFound || !strings.Contains(body, "introuvable") {
		t.Errorf("scope inconnue = %d\n%s", code, excerpt(body))
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
	part, _ := mw.CreateFormFile("fichier", "txs.csv")
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
		t.Errorf("transactions = %d, attendu 3", len(f.Book.Transactions))
	}
	// le flash de résultat est visible après redirection
	code, body = get(t, srv, rec.Header().Get("Location"))
	if code != http.StatusOK || !strings.Contains(body, "1 importée") {
		t.Errorf("flash absent:\n%s", excerpt(body))
	}
}

func TestRefreshButtonOffline(t *testing.T) {
	srv, _ := testServer(t) // serveur en mode offline
	code, _, loc := postForm(t, srv, "/refresh", url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(loc, "hors+ligne") && !strings.Contains(loc, "hors%20ligne") {
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
	// le formulaire propose comptes et actifs
	for _, want := range []string{"PEA Zephyr", "Amundi MSCI World", "<form", `name="kind"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/tx: %q manquant", want)
		}
	}

	// saisie d'un achat → 303 puis visible, et persisté dans le fichier
	code, body, loc := postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"2"}, "amount": {"1115"}, "note": {"via web"},
	})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("POST /tx = %d → %q\n%s", code, loc, excerpt(body))
	}
	if _, body = get(t, srv, "/tx"); !strings.Contains(body, "via web") {
		t.Error("transaction ajoutée invisible")
	}
	if len(f.Book.Transactions) != 3 {
		t.Errorf("transactions = %d, attendu 3", len(f.Book.Transactions))
	}

	// saisie invalide → 400 avec message, rien d'écrit
	code, body, _ = postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"abc"}, "amount": {"10"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "quantité") {
		t.Fatalf("POST invalide = %d\n%s", code, excerpt(body))
	}
	if len(f.Book.Transactions) != 3 {
		t.Error("la saisie invalide a écrit quelque chose")
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
		t.Errorf("transactions = %d, attendu 1", len(f.Book.Transactions))
	}
	// id inconnu → 404
	if code, _, _ := postForm(t, srv, "/tx/999/delete", url.Values{}); code != http.StatusNotFound {
		t.Errorf("delete inconnu = %d", code)
	}
}
