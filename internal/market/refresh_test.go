package market

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// fakeSource scripts the responses and records the calls.
type fakeSource struct {
	calls      []string                   // "DAILY sym from", "RESOLVE q", "LATEST sym"
	currencies map[string]domain.Currency // ref.Symbol → ref.Currency received
	daily      map[string]DailyData
	latest     map[string]Quote
	fail       map[string]bool
}

func (f *fakeSource) recordCurrency(ref Ref) {
	if f.currencies == nil {
		f.currencies = map[string]domain.Currency{}
	}
	f.currencies[ref.Symbol] = ref.Currency
}

func (f *fakeSource) Resolve(_ context.Context, q string) (SymbolInfo, error) {
	f.calls = append(f.calls, "RESOLVE "+q)
	return SymbolInfo{Symbol: strings.ToUpper(q)}, nil
}

func (f *fakeSource) Daily(_ context.Context, ref Ref, from domain.Date) (DailyData, error) {
	f.calls = append(f.calls, "DAILY "+ref.Symbol+" "+from.String())
	f.recordCurrency(ref)
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
	f.recordCurrency(ref)
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
	batchErrs  map[Ref]error
	batchCalls int
	batchRefs  int
}

func (b *batchSource) LatestBatch(_ context.Context, refs []Ref) BatchQuotes {
	b.batchCalls++
	b.batchRefs += len(refs)
	out := BatchQuotes{Quotes: map[Ref]Quote{}, Errs: map[Ref]error{}}
	for _, r := range refs {
		if q, ok := b.batch[r]; ok {
			out.Quotes[r] = q
			continue
		}
		if err, ok := b.batchErrs[r]; ok {
			out.Errs[r] = err
		}
	}
	return out
}

// TestSpotRefreshBatch: one batch call serves every ref; a miss is final
// (the batch already exhausted the source's fallbacks) - no per-ref retry,
// and a miss the source could not explain stays quiet: an instrument no
// provider covers must not warn on every command. The last close stands.
func TestSpotRefreshBatch(t *testing.T) {
	b := bookWithTrade(t) // cw8 (CW8.PA) + the EUR account → EURUSD=X ref
	at := domain.Today().Time().Add(15 * time.Hour)
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA", Currency: domain.EUR}: {Price: 555.5, Time: at, Currency: domain.EUR, Live: true},
		// EURUSD=X deliberately absent from the batch: an authoritative miss.
	}}

	sum := SpotRefresh(context.Background(), b, src)

	if src.batchCalls != 1 || src.batchRefs != 2 {
		t.Fatalf("batch calls = %d (refs %d), want 1 call covering both refs", src.batchCalls, src.batchRefs)
	}
	if len(src.calls) != 0 {
		t.Fatalf("per-ref calls behind a batch = %v, want none", src.calls)
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

// A currency mismatch (the source served a twin listing on another
// exchange) must never poison the series: one foreign-currency point makes
// valuations and day moves wrong by the FX rate. Warn, skip, retry later.
func TestRefreshCurrencyMismatchSkipsMerge(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA": { // the asset is declared EUR
			Currency: domain.USD,
			Closes:   []domain.PricePoint{{Date: domain.Today(), Close: 599}},
		},
		"EURUSD=X": {},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "USD") {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
	if len(b.Market.Price("cw8").Points) != 0 {
		t.Fatal("mismatched-currency closes were merged into the series")
	}
	if !b.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("a skipped merge must not stamp FetchedAt (retry later)")
	}
}

// The spot pass applies the same guard: a live quote in the wrong currency
// (a twin listing) is dropped, not merged.
func TestSpotRefreshCurrencyMismatchSkipsMerge(t *testing.T) {
	b := bookWithTrade(t)
	at := domain.Today().Time().Add(15 * time.Hour)
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA", Currency: domain.EUR}: {Price: 25.44, Time: at, Currency: domain.USD, Live: true},
	}}
	sum := SpotRefresh(context.Background(), b, src)
	if len(b.Market.Price("cw8").Points) != 0 {
		t.Fatal("mismatched-currency spot was merged into the series")
	}
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

// The twin-listing retry now lives in the Source (pofo's FetchAny under
// NoConvert); finador's contract is to hand the Source the declared
// currency on every ref, so it CAN enforce native lines.
func TestRefreshPassesDeclaredCurrencyToSource(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{"CW8.PA": {}, "EURUSD=X": {}}}
	Refresh(context.Background(), b, src, false)
	if got := src.currencies["CW8.PA"]; got != domain.EUR {
		t.Errorf("asset ref currency = %q, want the declared EUR", got)
	}
	// FX series hold the USD value of one unit: the ref demands USD.
	if got := src.currencies["EURUSD=X"]; got != domain.USD {
		t.Errorf("FX ref currency = %q, want USD", got)
	}

	src2 := &fakeSource{latest: map[string]Quote{}}
	SpotRefresh(context.Background(), b, src2)
	if got := src2.currencies["CW8.PA"]; got != domain.EUR {
		t.Errorf("spot asset ref currency = %q, want the declared EUR", got)
	}
	if got := src2.currencies["EURUSD=X"]; got != domain.USD {
		t.Errorf("spot FX ref currency = %q, want USD", got)
	}
}

// A batch that could not serve a ref AND knows why must say so. Silence here
// is how a whole portfolio ends up showing yesterday's prices while every
// command reports success.
func TestSpotRefreshReportsBatchFailures(t *testing.T) {
	b := bookWithTrade(t)
	at := domain.Today().Time().Add(15 * time.Hour)
	src := &batchSource{
		batch:     map[Ref]Quote{{Symbol: "EURUSD=X", Currency: domain.USD}: {Price: 1.15, Time: at, Currency: domain.USD, Live: true}},
		batchErrs: map[Ref]error{{Symbol: "CW8.PA", Currency: domain.EUR}: errors.New("yahoo quote: HTTP 401")},
	}

	sum := SpotRefresh(context.Background(), b, src)

	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "HTTP 401") || !strings.Contains(sum.Warnings[0], "CW8.PA") {
		t.Fatalf("warnings = %v, want the failing ref and its reason", sum.Warnings)
	}
}

// A quote that is not today's live price still merges - it is the best known
// price - but it is reported as stale, so an explicit refresh can answer the
// only question that matters when a figure looks wrong: is this current?
func TestSpotRefreshFlagsStaleQuotes(t *testing.T) {
	b := bookWithTrade(t)
	now := domain.Today().Time().Add(15 * time.Hour)
	yesterday := domain.Today().AddDays(-1).Time()
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA", Currency: domain.EUR}:   {Price: 555.5, Time: yesterday, Currency: domain.EUR, Live: false},
		{Symbol: "EURUSD=X", Currency: domain.USD}: {Price: 1.15, Time: now, Currency: domain.USD, Live: true},
	}}

	sum := SpotRefresh(context.Background(), b, src)

	if len(sum.Warnings) != 0 {
		t.Fatalf("a stale price is not a failure: %v", sum.Warnings)
	}
	if len(sum.Stale) != 1 || !strings.Contains(sum.Stale[0], "CW8.PA") {
		t.Fatalf("stale = %v, want only CW8.PA", sum.Stale)
	}
	if close, _, ok := b.Market.Price("cw8").At(domain.Today()); !ok || close != 555.5 {
		t.Errorf("the stale close must still be merged, got %v (ok=%v)", close, ok)
	}
}

// extendedBatchSource answers both batch shapes, recording which was used, so
// a test can prove the opt-in reaches the source (and only then).
type extendedBatchSource struct {
	batchSource
	extended      map[Ref]Quote
	extendedCalls int
}

func (e *extendedBatchSource) LatestBatchExtended(_ context.Context, refs []Ref) BatchQuotes {
	e.extendedCalls++
	out := BatchQuotes{Quotes: map[Ref]Quote{}, Errs: map[Ref]error{}}
	for _, r := range refs {
		if q, ok := e.extended[r]; ok {
			out.Quotes[r] = q
		}
	}
	return out
}

// TestSpotRefreshExtendedNeverStored: an off-hours print is reported for
// display and left out of the persisted series - the whole safety property of
// the opt-in. The regular pass on the same source is untouched.
func TestSpotRefreshExtendedNeverStored(t *testing.T) {
	cw8 := Ref{Symbol: "CW8.PA", Currency: domain.EUR}
	at := domain.Today().Time().Add(19*time.Hour + 59*time.Minute)
	newSrc := func() *extendedBatchSource {
		return &extendedBatchSource{
			batchSource: batchSource{batch: map[Ref]Quote{
				cw8: {Price: 550, Time: at, Currency: domain.EUR, Live: true, Session: "regular"},
			}},
			extended: map[Ref]Quote{
				cw8: {Price: 561, Time: at, Currency: domain.EUR, Live: true, Session: SessionPost},
			},
		}
	}

	// Opt-in ON: the post print is served, reported, and NOT merged.
	b := bookWithTrade(t)
	src := newSrc()
	sum := SpotRefreshExtended(context.Background(), b, src)
	if src.extendedCalls != 1 || src.batchCalls != 0 {
		t.Fatalf("calls: extended=%d regular=%d, want the extended batch only", src.extendedCalls, src.batchCalls)
	}
	q, ok := sum.Quotes["cw8"]
	if !ok || q.Price != 561 || q.Session != SessionPost || !q.Extended() {
		t.Fatalf("quote = %+v (ok=%v), want the post print", q, ok)
	}
	if _, _, ok := b.Market.Price("cw8").At(domain.Today()); ok {
		t.Error("the off-hours print reached the price series: it must never be stored")
	}
	if len(sum.Stale) != 0 {
		t.Errorf("stale = %v, want none: an off-hours print is today's", sum.Stale)
	}

	// Opt-in OFF on the same source: the regular batch, merged as before.
	b = bookWithTrade(t)
	src = newSrc()
	sum = SpotRefresh(context.Background(), b, src)
	if src.extendedCalls != 0 || src.batchCalls != 1 {
		t.Fatalf("calls: extended=%d regular=%d, want the regular batch only", src.extendedCalls, src.batchCalls)
	}
	if q := sum.Quotes["cw8"]; q.Price != 550 || q.Extended() {
		t.Fatalf("quote = %+v, want the regular 550", q)
	}
	if close, _, ok := b.Market.Price("cw8").At(domain.Today()); !ok || close != 550 {
		t.Errorf("today's close = %v (ok=%v), want the regular spot 550", close, ok)
	}
}

// TestSpotRefreshExtendedDegrades: a source that cannot serve off-hours
// prints keeps its regular batch rather than losing batching.
func TestSpotRefreshExtendedDegrades(t *testing.T) {
	b := bookWithTrade(t)
	at := domain.Today().Time().Add(15 * time.Hour)
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA", Currency: domain.EUR}: {Price: 555.5, Time: at, Currency: domain.EUR, Live: true},
	}}
	sum := SpotRefreshExtended(context.Background(), b, src)
	if src.batchCalls != 1 {
		t.Fatalf("batch calls = %d, want 1", src.batchCalls)
	}
	if q, ok := sum.Quotes["cw8"]; !ok || q.Price != 555.5 || q.Extended() {
		t.Fatalf("quote = %+v (ok=%v), want the regular batched 555.5", q, ok)
	}
	if close, _, ok := b.Market.Price("cw8").At(domain.Today()); !ok || close != 555.5 {
		t.Errorf("today's close = %v, want it merged as usual", close)
	}
}

// TestSpotSummaryStale pins the two ways a "current" price is not current,
// told apart on purpose: a closed market still answers with a live field, so
// calling that "no live quote" would be plainly false.
func TestSpotSummaryStale(t *testing.T) {
	today := domain.Today()
	cases := []struct {
		name string
		q    Quote
		want string // "" = not stale
	}{
		{"live, traded today", Quote{Live: true, Time: today.Time().Add(15 * time.Hour)}, ""},
		{"live, market shut since yesterday", Quote{Live: true, Time: today.AddDays(-1).Time()}, "last traded"},
		{"a daily close, no live quote", Quote{Time: today.Time()}, "no live quote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sum SpotSummary
			sum.stale("CW8.PA", tc.q)
			if tc.want == "" {
				if len(sum.Stale) != 0 {
					t.Fatalf("stale = %v, want none", sum.Stale)
				}
				return
			}
			if len(sum.Stale) != 1 || !strings.Contains(sum.Stale[0], tc.want) ||
				!strings.Contains(sum.Stale[0], "CW8.PA") {
				t.Fatalf("stale = %v, want one entry naming CW8.PA and %q", sum.Stale, tc.want)
			}
		})
	}
}

// An undated quote is refused on both legs: dated at the Unix epoch it would
// splice a 1970 point into the series, and nothing ever removes it.
func TestSpotRefreshRejectsUndatedQuotes(t *testing.T) {
	b := bookWithTrade(t)
	src := &batchSource{batch: map[Ref]Quote{
		{Symbol: "CW8.PA", Currency: domain.EUR}:   {Price: 555.5, Currency: domain.EUR, Live: true},
		{Symbol: "EURUSD=X", Currency: domain.USD}: {Price: 1.15, Currency: domain.USD, Live: true},
	}}

	sum := SpotRefresh(context.Background(), b, src)

	if len(sum.Warnings) != 2 {
		t.Fatalf("warnings = %v, want one per undated quote", sum.Warnings)
	}
	for _, w := range sum.Warnings {
		if !strings.Contains(w, "undated quote ignored") {
			t.Errorf("warning = %q, want it to name the reason", w)
		}
	}
	if len(sum.Quotes) != 0 {
		t.Errorf("quotes = %v, want none reported", sum.Quotes)
	}
	if _, _, ok := b.Market.Price("cw8").At(domain.Today()); ok {
		t.Error("an undated quote reached the price series")
	}
	if b.Market.SpotAt.IsZero() {
		t.Error("SpotAt must be stamped even when every quote is refused")
	}
}

// An off-hours FX print is dropped outright rather than reported: an FX series
// has no display path of its own, and a thin off-hours cross must not become
// the rate every conversion of the day uses.
func TestSpotRefreshExtendedDropsFXPrints(t *testing.T) {
	b := bookWithTrade(t)
	at := domain.Today().Time().Add(19 * time.Hour)
	eurusd := Ref{Symbol: "EURUSD=X", Currency: domain.USD}
	src := &extendedBatchSource{extended: map[Ref]Quote{
		eurusd: {Price: 1.15, Time: at, Currency: domain.USD, Live: true, Session: SessionPost},
	}}

	sum := SpotRefreshExtended(context.Background(), b, src)

	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none: a dropped print is not a failure", sum.Warnings)
	}
	if _, _, ok := b.Market.FXSeries(domain.EUR).At(domain.Today()); ok {
		t.Error("an off-hours cross entered the FX series")
	}
}

// TestRefLabel: warnings must name the instrument the user typed, and an
// instrument declared by ISIN alone still gets named.
func TestRefLabel(t *testing.T) {
	cases := []struct {
		ref  Ref
		want string
	}{
		{Ref{Symbol: "CW8.PA", ISIN: "FR0010315770"}, "CW8.PA"},
		{Ref{ISIN: "FR0010315770"}, "FR0010315770"},
		{Ref{}, ""},
	}
	for _, tc := range cases {
		if got := refLabel(tc.ref); got != tc.want {
			t.Errorf("refLabel(%+v) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// TestFXFetchFrom: an FX series refreshes from its last close, back-fills from
// a week before the book's first transaction when it is empty, and falls back
// to a short window on a book with no history at all.
func TestFXFetchFrom(t *testing.T) {
	filled := &domain.PriceSeries{}
	filled.Merge([]domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 1.1}})

	traded := bookWithTrade(t) // first transaction 2026-05-15
	cases := []struct {
		name   string
		book   *domain.Book
		series *domain.PriceSeries
		want   domain.Date
	}{
		{"incremental from the last close", traded, filled, mustDate("2026-06-01")},
		{"back-fill a week before the first trade", traded, &domain.PriceSeries{}, mustDate("2026-05-08")},
		{"empty book: a short window", domain.NewBook(), &domain.PriceSeries{}, domain.Today().AddDays(-30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fxFetchFrom(tc.book, tc.series); got != tc.want {
				t.Errorf("fxFetchFrom = %s, want %s", got, tc.want)
			}
		})
	}
}

// The price-history floor is a generous lookback, but a security bought
// before it must still be covered from a week before its first transaction:
// otherwise the position's own history has a hole no later run fills.
func TestPriceHistoryFloorReachesTheFirstTrade(t *testing.T) {
	b := bookWithTrade(t)
	old := domain.DateOf(domain.Today().Time().AddDate(-priceHistoryYears-2, 0, 0))
	b.Add(domain.Transaction{Date: old, Account: "pea", Asset: "cw8",
		Kind: domain.Buy, Quantity: decimal.NewFromInt(1),
		Amount: domain.Money{Amount: decimal.NewFromInt(100), Currency: domain.EUR}})

	if got, want := priceHistoryFloor(b, "cw8"), old.AddDays(-7); got != want {
		t.Errorf("floor = %s, want %s (a week before the oldest trade)", got, want)
	}
	// Another asset's trades never deepen this one's floor.
	deep := domain.DateOf(domain.Today().Time().AddDate(-priceHistoryYears, 0, 0))
	if got := priceHistoryFloor(b, "other"); got != deep {
		t.Errorf("floor of an untraded asset = %s, want the %d-year lookback %s",
			got, priceHistoryYears, deep)
	}
}

// TestPriceFetchFromFloorThenIncremental: deep history is (back-)filled once,
// then every run refreshes from the last close - which still moves intraday.
func TestPriceFetchFromFloorThenIncremental(t *testing.T) {
	b := bookWithTrade(t)
	floor := priceHistoryFloor(b, "cw8")
	s := &domain.PriceSeries{}

	if got := priceFetchFrom(b, "cw8", s); got != floor {
		t.Fatalf("first fetch from %s, want the floor %s", got, floor)
	}
	// Deep enough but empty (a security younger than the floor): the floor
	// again, and never a re-request deeper than it.
	s.HistFrom = floor
	if got := priceFetchFrom(b, "cw8", s); got != floor {
		t.Fatalf("empty deep series fetches from %s, want %s", got, floor)
	}
	s.Merge([]domain.PricePoint{{Date: mustDate("2026-06-02"), Close: 100}})
	if got := priceFetchFrom(b, "cw8", s); got != mustDate("2026-06-02") {
		t.Fatalf("incremental fetch from %s, want the last close", got)
	}
}

// TestMergeDividends: events are upserted by ex-date and kept sorted, so a
// re-fetch that restates an amount corrects it instead of doubling the income.
func TestMergeDividends(t *testing.T) {
	var m domain.MarketData
	mergeDividends(&m, "cw8", nil)
	if m.Dividends != nil {
		t.Fatalf("an empty fetch allocated %v", m.Dividends)
	}

	mergeDividends(&m, "cw8", []domain.DividendEvent{
		{ExDate: mustDate("2026-06-03"), Amount: 0.5},
		{ExDate: mustDate("2026-03-02"), Amount: 0.4},
	})
	// A later fetch restates June and adds one in between.
	mergeDividends(&m, "cw8", []domain.DividendEvent{
		{ExDate: mustDate("2026-06-03"), Amount: 0.55},
		{ExDate: mustDate("2026-04-01"), Amount: 0.45},
	})

	got := m.Dividends["cw8"]
	want := []domain.DividendEvent{
		{ExDate: mustDate("2026-03-02"), Amount: 0.4},
		{ExDate: mustDate("2026-04-01"), Amount: 0.45},
		{ExDate: mustDate("2026-06-03"), Amount: 0.55},
	}
	if len(got) != len(want) {
		t.Fatalf("dividends = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dividend %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Refresh feeds the dividend map through the same path: one fetch, both
// series and events, and nothing merged when the currency check rejects it.
func TestRefreshStoresDividends(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA": {Currency: domain.EUR,
			Closes:    []domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}},
			Dividends: []domain.DividendEvent{{ExDate: mustDate("2026-06-01"), Amount: 1.25}}},
	}}

	Refresh(context.Background(), b, src, false)

	evs := b.Market.Dividends["cw8"]
	if len(evs) != 1 || evs[0].Amount != 1.25 {
		t.Fatalf("dividends = %+v, want the fetched event", evs)
	}
}

// Only quoted securities are fetched: a property has no market, and a
// security whose ticker is unknown has nothing to fetch it by. Neither may
// cost a request, on either pass.
func TestRefreshSkipsUnquotableAssets(t *testing.T) {
	b := bookWithTrade(t)
	if err := b.AddAsset(&domain.Asset{ID: "flat", Kind: domain.Property, Name: "Flat",
		Currency: domain.EUR, Group: "immo"}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&domain.Asset{ID: "opaque", Kind: domain.Security, Name: "Unlisted fund",
		Currency: domain.EUR, Group: "actions"}); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{daily: map[string]DailyData{
		"CW8.PA": {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}}},
	}}

	Refresh(context.Background(), b, src, false)
	SpotRefresh(context.Background(), b, src)

	for _, call := range src.calls {
		if strings.Contains(call, "flat") || strings.Contains(call, "opaque") {
			t.Errorf("unquotable asset fetched: %q", call)
		}
	}
	// One daily fetch per quoted security plus the FX cross, then one spot
	// call each: nothing for the property or the tickerless fund.
	if len(src.calls) != 4 {
		t.Errorf("calls = %v, want 2 daily + 2 spot", src.calls)
	}
}

// An FX leg fails exactly like a price leg: a warning, the cached rate left
// alone, and the pass carrying on with the rest.
func TestRefreshFXFailureWarns(t *testing.T) {
	b := bookWithTrade(t)
	src := &fakeSource{
		daily: map[string]DailyData{
			"CW8.PA": {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 550}}},
		},
		fail: map[string]bool{"EURUSD=X": true},
	}

	sum := Refresh(context.Background(), b, src, false)

	if len(sum.Fetched) != 1 || sum.Fetched[0] != "CW8.PA" {
		t.Errorf("fetched = %v, want the price leg to have succeeded", sum.Fetched)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "EURUSD=X") {
		t.Fatalf("warnings = %v, want the FX failure", sum.Warnings)
	}
	if b.Market.FXSeries(domain.EUR).FetchedAt == domain.Today() {
		t.Error("a failed FX leg must stay unstamped so a later run retries")
	}
}

// An FX series holds the USD value of one unit: a cross served in any other
// currency would corrupt every conversion, so it is refused on both passes
// rather than merged.
func TestRefreshFXOffCurrencyRefused(t *testing.T) {
	eur := Ref{Symbol: "EURUSD=X", Currency: domain.USD}
	at := domain.Today().Time().Add(15 * time.Hour)

	b := bookWithTrade(t)
	src := &fakeSource{daily: map[string]DailyData{
		// The cross answers in EUR: nonsense for a EUR->USD rate.
		"EURUSD=X": {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 1.15}}},
	}}
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "USD expected") {
		t.Fatalf("warnings = %v, want the off-currency cross refused", sum.Warnings)
	}
	if _, _, ok := b.Market.FXSeries(domain.EUR).At(mustDate("2026-06-01")); ok {
		t.Error("an off-currency cross reached the FX series")
	}

	b = bookWithTrade(t)
	spot := &batchSource{batch: map[Ref]Quote{
		eur: {Price: 1.15, Time: at, Currency: domain.EUR, Live: true},
	}}
	ssum := SpotRefresh(context.Background(), b, spot)
	if len(ssum.Warnings) != 1 || !strings.Contains(ssum.Warnings[0], "USD expected") {
		t.Fatalf("warnings = %v, want the off-currency spot refused", ssum.Warnings)
	}
	if _, _, ok := b.Market.FXSeries(domain.EUR).At(domain.Today()); ok {
		t.Error("an off-currency spot reached the FX series")
	}
}
