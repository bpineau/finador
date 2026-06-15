package market

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// fakeSource scripts the responses and records the calls.
type fakeSource struct {
	calls []string // "DAILY sym from", "RESOLVE q"
	daily map[string]DailyData
	fail  map[string]bool
}

func (f *fakeSource) Resolve(_ context.Context, q string) (SymbolInfo, error) {
	f.calls = append(f.calls, "RESOLVE "+q)
	return SymbolInfo{Symbol: strings.ToUpper(q)}, nil
}

func (f *fakeSource) Daily(_ context.Context, ref Ref, from domain.Date) (DailyData, error) {
	f.calls = append(f.calls, "DAILY "+ref.Symbol+" "+from.String())
	if f.fail[ref.Symbol] {
		return DailyData{}, domain.ErrNotFound
	}
	return f.daily[ref.Symbol], nil
}

func bookWithTrade(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-05-15"), Account: "pea", Asset: "cw8",
		Kind: domain.Buy, Quantity: decimal.NewFromInt(10),
		Amount: domain.Money{Amount: decimal.NewFromInt(5500), Currency: domain.EUR}})
	return b
}

func TestRefreshFetchesFromFirstTx(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA":   {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 550}}},
		"EURUSD=X": {Currency: domain.USD, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 1.1}}},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings: %v", sum.Warnings)
	}
	// prices requested from the first transaction − 7 days
	wantCall := "DAILY CW8.PA " + mustDate("2026-05-15").AddDays(-7).String()
	if !contains(src.calls, wantCall) {
		t.Errorf("appels = %v, attendu %q", src.calls, wantCall)
	}
	// series and FX cached, FetchedAt set to today
	if _, _, ok := b.Market.Price("cw8").At(mustDate("2026-05-15")); !ok {
		t.Error("série prix absente")
	}
	if _, _, ok := b.Market.FXSeries(domain.EUR).At(mustDate("2026-05-15")); !ok {
		t.Error("série FX absente")
	}
	if b.Market.Price("cw8").FetchedAt != domain.Today() {
		t.Error("FetchedAt non posé")
	}
}

func TestRefreshSkipsFreshSeries(t *testing.T) {
	b := bookWithTrade(t)
	b.Market.Price("cw8").FetchedAt = domain.Today()
	b.Market.FXSeries(domain.EUR).FetchedAt = domain.Today()
	src := &fakeSource{}
	Refresh(context.Background(), b, src, false)
	if len(src.calls) != 0 {
		t.Fatalf("séries fraîches refetchées: %v", src.calls)
	}
	// force overrides it
	src.daily = map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}
	Refresh(context.Background(), b, src, true)
	if len(src.calls) != 2 {
		t.Fatalf("force inopérant: %v", src.calls)
	}
}

func TestRefreshIncrementalFrom(t *testing.T) {
	b := bookWithTrade(t)
	b.Market.Price("cw8").Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}})
	src := &fakeSource{daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}}
	Refresh(context.Background(), b, src, false)
	// restarts from the LAST known close (it may have moved during the session)
	if !contains(src.calls, "DAILY CW8.PA 2026-06-01") {
		t.Errorf("appels = %v", src.calls)
	}
}

func TestRefreshWarnsAndContinues(t *testing.T) {
	b := bookWithTrade(t)
	if err := b.AddAsset(&domain.Asset{ID: "dead", Kind: domain.Security, Name: "Dead",
		Ticker: "DEAD.PA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{
		daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}},
		fail:  map[string]bool{"DEAD.PA": true},
	}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "DEAD.PA") {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
	if !contains(src.calls, "DAILY EURUSD=X "+mustDate("2026-05-15").AddDays(-7).String()) {
		t.Errorf("le FX aurait dû être rafraîchi malgré l'échec: %v", src.calls)
	}
}

func TestRefreshCurrencyMismatchWarning(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA":   {Currency: domain.USD}, // the asset is declared EUR
		"EURUSD=X": {},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "USD") {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
