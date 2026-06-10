package web

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if err := b.AddAccount(&domain.Account{ID: "pea", Name: "PEA BforBank", Currency: domain.EUR}); err != nil {
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

func excerpt(s string) string {
	if len(s) > 800 {
		return s[:800]
	}
	return s
}
