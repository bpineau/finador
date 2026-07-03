package market

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// fakeSource scripts the responses and records the calls.
type fakeSource struct {
	calls  []string // "DAILY sym from", "RESOLVE q", "LATEST sym"
	daily  map[string]DailyData
	latest map[string]Quote
	fail   map[string]bool
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

func (f *fakeSource) Intraday(_ context.Context, _ Ref) (IntradayData, error) {
	return IntradayData{}, ErrNotCovered
}

func (f *fakeSource) Latest(_ context.Context, ref Ref) (Quote, error) {
	f.calls = append(f.calls, "LATEST "+ref.Symbol)
	if f.fail[ref.Symbol] {
		return Quote{}, domain.ErrNotFound
	}
	q, ok := f.latest[ref.Symbol]
	if !ok {
		return Quote{}, ErrNotCovered
	}
	return q, nil
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

func TestRefreshBackfillsDeepPriceHistory(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA":   {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 550}}},
		"EURUSD=X": {Currency: domain.USD, Closes: []domain.PricePoint{{Date: mustDate("2026-05-15"), Close: 1.1}}},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings: %v", sum.Warnings)
	}
	// prices requested from the deep history floor (years back), not just from
	// the first transaction, so the price chart has real history to show.
	floor := domain.DateOf(domain.Today().Time().AddDate(-priceHistoryYears, 0, 0))
	wantCall := "DAILY CW8.PA " + floor.String()
	if !contains(src.calls, wantCall) {
		t.Errorf("appels = %v, attendu %q", src.calls, wantCall)
	}
	if b.Market.Price("cw8").HistFrom != floor {
		t.Errorf("HistFrom = %v, attendu %v", b.Market.Price("cw8").HistFrom, floor)
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

func TestSpotRefreshMergesTodayAndReportsQuotes(t *testing.T) {
	b := bookWithTrade(t)
	series := b.Market.Price("cw8")
	series.Merge([]domain.PricePoint{{Date: domain.Today(), Close: 550}})
	at := domain.Today().Time().Add(15 * time.Hour) // an intraday instant
	src := &fakeSource{latest: map[string]Quote{
		"CW8.PA":   {Price: 555.5, Time: at, Currency: domain.EUR, Live: true},
		"EURUSD=X": {Price: 1.12, Time: at, Currency: domain.USD, Live: true},
	}}

	sum := SpotRefresh(context.Background(), b, src)

	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings: %v", sum.Warnings)
	}
	if close, _, ok := series.At(domain.Today()); !ok || close != 555.5 {
		t.Errorf("today's close = %v, want the live spot 555.5", close)
	}
	if rate, _, ok := b.Market.FXSeries(domain.EUR).At(domain.Today()); !ok || rate != 1.12 {
		t.Errorf("today's FX = %v, want the live spot 1.12", rate)
	}
	q, ok := sum.Quotes["cw8"]
	if !ok || !q.Live || !q.Time.Equal(at) {
		t.Errorf("quote metadata: %+v (ok=%v)", q, ok)
	}
	// A spot pass never stamps the daily fetch: Refresh stays due.
	if !b.Market.Price("cw8").FetchedAt.IsZero() {
		t.Error("SpotRefresh must not stamp FetchedAt")
	}
}

func TestSpotRefreshDegradesToWarnings(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{fail: map[string]bool{"CW8.PA": true}}
	sum := SpotRefresh(context.Background(), b, src)
	// The failed asset warns; the unscripted FX is ErrNotCovered, which is a
	// normal condition (its last daily close stands) and stays silent.
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "CW8.PA") {
		t.Fatalf("warnings = %v, want the failed asset only", sum.Warnings)
	}
	if len(sum.Quotes) != 0 {
		t.Fatalf("quotes = %v, want none", sum.Quotes)
	}
}

// batchSource wraps fakeSource with a scripted batch answer and counters.
type batchSource struct {
	fakeSource
	batch      map[Ref]Quote
	batchCalls int
	batchRefs  int
}

func (b *batchSource) LatestBatch(_ context.Context, refs []Ref) map[Ref]Quote {
	b.batchCalls++
	b.batchRefs += len(refs)
	out := map[Ref]Quote{}
	for _, r := range refs {
		if q, ok := b.batch[r]; ok {
			out[r] = q
		}
	}
	return out
}

// TestSpotRefreshBatch: one batch call serves the covered instruments; only
// the misses fall back to Latest, and a not-covered miss stays silent.
func TestSpotRefreshBatch(t *testing.T) {
	b := bookWithTrade(t) // cw8 (CW8.PA) + the EUR account → EURUSD=X ref
	at := domain.Today().Time().Add(15 * time.Hour)
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA"}: {Price: 555.5, Time: at, Currency: domain.EUR, Live: true},
		// EURUSD=X deliberately absent from the batch: per-ref fallback,
		// which the embedded fakeSource answers with ErrNotCovered.
	}}

	sum := SpotRefresh(context.Background(), b, src)

	if src.batchCalls != 1 || src.batchRefs != 2 {
		t.Fatalf("batch calls = %d (refs %d), want 1 call covering both refs", src.batchCalls, src.batchRefs)
	}
	if got := src.calls; len(got) != 1 || got[0] != "LATEST EURUSD=X" {
		t.Fatalf("fallback calls = %v, want only the batch miss", got)
	}
	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings: %v", sum.Warnings)
	}
	if close, _, ok := b.Market.Price("cw8").At(domain.Today()); !ok || close != 555.5 {
		t.Errorf("today's close = %v, want the batched spot 555.5", close)
	}
	if q, ok := sum.Quotes["cw8"]; !ok || !q.Live {
		t.Errorf("quote metadata: %+v (ok=%v)", q, ok)
	}
}

func TestRefreshIncrementalFrom(t *testing.T) {
	b := bookWithTrade(t)
	series := b.Market.Price("cw8")
	series.Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}})
	series.HistFrom = mustDate("2010-01-01") // already back-filled deep
	src := &fakeSource{daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}}
	Refresh(context.Background(), b, src, false)
	// already deep enough: restarts from the LAST known close (it moves intraday)
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
