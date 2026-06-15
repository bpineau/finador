package portfolio

import (
	"fmt"
	"testing"

	"finador/internal/domain"
	"finador/internal/perf"
)

func TestSeriesMatchesValueAtEndpoint(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	want, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	if last.Date != at {
		t.Fatalf("dernier point au %s, attendu %s", last.Date, at)
	}
	approx(t, "gross fin de série vs Value", last.Gross, want.Gross)
	approx(t, "net fin de série vs Value", last.Net, want.Net)
}

func TestSeriesAccountScopeMatchesValue(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	want, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scopeOf(t, b, "PEA"), mustDate("2026-01-01"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	approx(t, "gross", last.Gross, want.Gross)
	approx(t, "net", last.Net, want.Net)
}

func TestSeriesExternalFlowsAllScope(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA is tracked: its trades are internal.
	// The house: its first statement is on 2026-01-01 (== from) → not collected (base-day flow);
	// its second statement (450000 on 06-01) re-bases the value → adjustment flow +50000.
	// Expected flows:
	//   [0] 01-05 +12000: livret adoption (first cash statement, rule D8)
	//   [1] 01-10 +10000: pea deposit
	//   [2] 01-20 +1100 : cto buy (untracked account)
	//   [3] 06-01 +50000: house re-base (declared revaluation, not a return)
	if len(res.Flows) != 4 {
		t.Fatalf("flows = %+v, attendu 4", res.Flows)
	}
	if res.Flows[0].Date != mustDate("2026-01-05") {
		t.Errorf("flow[0] = %+v", res.Flows[0])
	}
	approx(t, "flow adoption livret", res.Flows[0].Amount, 12000)
	if res.Flows[1].Date != mustDate("2026-01-10") {
		t.Errorf("flow[1] = %+v", res.Flows[1])
	}
	approx(t, "flow deposit", res.Flows[1].Amount, 10000)
	if res.Flows[2].Date != mustDate("2026-01-20") {
		t.Errorf("flow[2] = %+v", res.Flows[2])
	}
	approx(t, "flow buy cto", res.Flows[2].Amount, 1100)
	if res.Flows[3].Date != mustDate("2026-06-01") {
		t.Errorf("flow[3] = %+v", res.Flows[3])
	}
	approx(t, "flow re-base maison", res.Flows[3].Amount, 50000)
}

func TestSeriesExternalFlowsGroupScope(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, "actions"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// all trades on cw8 are pocket flows: +5000, +1100, +2750, −1800
	wantFlows := []struct {
		date string
		amt  float64
	}{
		{"2026-01-15", 5000}, {"2026-01-20", 1100}, {"2026-02-15", 2750}, {"2026-03-15", -1800},
	}
	if len(res.Flows) != len(wantFlows) {
		t.Fatalf("flows = %+v", res.Flows)
	}
	for i, w := range wantFlows {
		if res.Flows[i].Date != mustDate(w.date) {
			t.Errorf("flow[%d].Date = %s", i, res.Flows[i].Date)
		}
		approx(t, "flow", res.Flows[i].Amount, w.amt)
	}
}

func TestSeriesBeforeMarketData(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-01-12"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// on Jan 12: no cw8 close (the series starts on March 20) → the
	// position contributes 0; pea cash 10000 (deposited on the 10th), livret 12000
	// (statement on the 5th), house 400000 (statement on the 1st)
	last := res.Points[len(res.Points)-1]
	approx(t, "gross avant données marché", last.Gross, 10000+12000+400000)
}

func TestSeriesDefaultFrom(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), domain.Date{}, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// from zero → first ledger transaction (house statement on Jan 1)
	if res.Points[0].Date != mustDate("2026-01-01") {
		t.Errorf("premier point = %s", res.Points[0].Date)
	}
}

func TestSeriesAutoDividendFlows(t *testing.T) {
	b := valuationBook(t)
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	// group scope: the dividend leaves the pocket → flow −(15+2)×2 ?
	// pea holds 15 shares on March 1, cto 2 → −34 in total
	res, err := Series(b, scopeOf(t, b, "actions"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	var divFlow float64
	for _, f := range res.Flows {
		if f.Date == mustDate("2026-03-01") {
			divFlow += f.Amount
		}
	}
	approx(t, "flux dividende sortant", divFlow, -34)
}

func TestSeriesAdoptionFlowsForProperty(t *testing.T) {
	b := valuationBook(t)
	// The house is valued by declaration, not by a market: each statement
	// re-bases the value (a contribution), it never yields a "return".
	//   - 1st statement (400000 on Jan 1) = adoption (full contribution)
	//   - 2nd statement (450000 on June 1) = re-base → adjustment flow +50000
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2025-12-25"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	flowAt := func(d string) (ExternalFlow, int) {
		var hits []ExternalFlow
		for _, f := range res.Flows {
			if f.Date == mustDate(d) {
				hits = append(hits, f)
			}
		}
		if len(hits) == 1 {
			return hits[0], 1
		}
		return ExternalFlow{}, len(hits)
	}
	if f, n := flowAt("2026-01-01"); n != 1 {
		t.Fatalf("flux au 2026-01-01 = %d, attendu 1 (adoption)", n)
	} else {
		approx(t, "adoption maison", f.Amount, 400000)
	}
	if f, n := flowAt("2026-06-01"); n != 1 {
		t.Fatalf("flux au 2026-06-01 = %d, attendu 1 (re-base)", n)
	} else {
		approx(t, "re-base maison", f.Amount, 50000)
	}
}

func TestSeriesAdoptionFlowForCashStatement(t *testing.T) {
	b := valuationBook(t)
	// livret: first cash statement 12000 on Jan 5 = adoption
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2025-12-25"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range res.Flows {
		if f.Date == mustDate("2026-01-05") && f.Amount > 11999 && f.Amount < 12001 {
			found = true
		}
	}
	if !found {
		t.Fatalf("adoption du livret absente des flux: %+v", res.Flows)
	}
}

func TestSeriesTWRSaneWithAdoptedProperty(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2025-12-25"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	pts := make([]perf.Point, len(res.Points))
	for i, p := range res.Points {
		pts[i] = perf.Point{Date: p.Date, Value: p.Gross}
	}
	flows := make([]perf.Flow, len(res.Flows))
	for i, f := range res.Flows {
		flows[i] = perf.Flow{Date: f.Date, Amount: f.Amount}
	}
	twr := perf.TWR(pts, flows)
	// without the adoption rule, the TWR explodes (>+4000%); with it, it stays < 20%
	if twr > 0.20 || twr < -0.20 {
		t.Fatalf("TWR patrimoine = %+.2f%%, attendu raisonnable", twr*100)
	}
}

func TestSeriesWarnsOnConversionFailure(t *testing.T) {
	b := valuationBook(t)
	if err := b.AddAccount(&domain.Account{ID: "us", Name: "US Bank", Currency: domain.USD}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-02-01"), Account: "us", Kind: domain.Statement,
		Amount: domain.Money{Amount: dec("1000"), Currency: domain.USD}})
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("aucun avertissement de conversion")
	}
}

func TestSeriesExternalFlowsLabelScope(t *testing.T) {
	b := valuationBook(t)
	// Tag pea/cw8 with label "retraite"; cto/cw8 has no label.
	_ = b.AddLabel(&domain.Label{ID: "lbl1", Account: "pea", Asset: "cw8", Name: "retraite"})

	scope, err := LabelScope(b, "retraite")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scope, mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// pea/cw8: buy 5000 on 01-15, buy 2750 on 02-15, sell -1800 on 03-15
	// cto/cw8 is NOT in the label set → its buy on 01-20 must NOT appear.
	wantFlows := []struct {
		date string
		amt  float64
	}{
		{"2026-01-15", 5000},
		{"2026-02-15", 2750},
		{"2026-03-15", -1800},
	}
	if len(res.Flows) != len(wantFlows) {
		t.Fatalf("flows = %+v, want %d flows", res.Flows, len(wantFlows))
	}
	for i, w := range wantFlows {
		if res.Flows[i].Date != mustDate(w.date) {
			t.Errorf("flow[%d].Date = %s, want %s", i, res.Flows[i].Date, w.date)
		}
		approx(t, fmt.Sprintf("flow[%d]", i), res.Flows[i].Amount, w.amt)
	}
}

func TestSeriesMatchesValueWithWithholdingDividend(t *testing.T) {
	// the base golden test has no dividend: this one locks the
	// withholding tax identical on both sides (value.go ↔ series.go)
	b := valuationBook(t)
	cw8, _ := b.Asset("cw8")
	cw8.Withholding = 0.15
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	at := mustDate("2026-06-05")
	for _, ref := range []string{"", "PEA"} {
		want, err := Value(b, scopeOf(t, b, ref), at, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Series(b, scopeOf(t, b, ref), mustDate("2026-01-01"), at, domain.EUR, fxStub{})
		if err != nil {
			t.Fatal(err)
		}
		last := res.Points[len(res.Points)-1]
		approx(t, "gross("+ref+")", last.Gross, want.Gross)
		approx(t, "net("+ref+")", last.Net, want.Net)
	}
}

// A property is valued by declaration: entering an acquisition price and then a
// current value is onboarding, not a multi-year gain compressed into one day.
// Every statement re-bases the value (a flow), so TWR stays flat.
func TestSeriesPropertyRevaluationIsNotPerformance(t *testing.T) {
	b := domain.NewBook()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.AddAccount(&domain.Account{ID: "immo", Name: "Immo", Currency: domain.EUR}))
	must(b.AddAsset(&domain.Asset{ID: "house", Kind: domain.Property, Name: "House", Currency: domain.EUR, Group: "immo"}))
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "immo", Asset: "house",
		Kind: domain.Statement, Amount: eur("200000")}) // acquisition
	b.Add(domain.Transaction{Date: mustDate("2026-03-01"), Account: "immo", Asset: "house",
		Kind: domain.Statement, Amount: eur("260000")}) // current value, declared on onboarding day

	res, err := Series(b, scopeOf(t, b, "immo"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// The +60000 re-statement is an adjustment flow, not a return.
	twr := perf.TWR(res.PerfPoints(false), res.PerfFlows())
	approx(t, "property TWR (declared revaluation ≠ perf)", twr, 0)
}

// Onboarding a position at its (stale) average cost must not fabricate
// performance: the external flow is the shares' MARKET value when they enter
// the scope, so TWR stays flat instead of booking the cost→market gap.
func TestSeriesOpeningBuyValuedAtMarket(t *testing.T) {
	b := domain.NewBook()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.AddAccount(&domain.Account{ID: "cto", Name: "CTO", Currency: domain.EUR}))
	must(b.AddAsset(&domain.Asset{ID: "aa", Kind: domain.Security, Name: "A", Currency: domain.EUR, Group: "g"}))
	must(b.AddAsset(&domain.Asset{ID: "bb", Kind: domain.Security, Name: "B", Currency: domain.EUR, Group: "g"}))

	// A is bought at market (cost == value) and held flat — it gives the window
	// a positive base value so the next day's return is actually measured.
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "cto", Asset: "aa",
		Kind: domain.Buy, Quantity: dec("10"), Amount: eur("1000")})
	// B is onboarded mid-window at a STALE average cost (500) while the market
	// already says 1000 — the classic "declare today's positions" case.
	b.Add(domain.Transaction{Date: mustDate("2026-01-05"), Account: "cto", Asset: "bb",
		Kind: domain.Buy, Quantity: dec("10"), Amount: eur("500")})

	b.Market.Price("aa").Merge([]domain.PricePoint{{Date: mustDate("2026-01-01"), Close: 100}}) // 10×100, flat
	b.Market.Price("bb").Merge([]domain.PricePoint{{Date: mustDate("2026-01-05"), Close: 100}}) // 10×100 at entry, flat

	res, err := Series(b, scopeOf(t, b, "g"), mustDate("2026-01-01"), mustDate("2026-01-10"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// The mid-window opening is a value transfer (+1000 market), not the 500 cost.
	if len(res.Flows) != 1 {
		t.Fatalf("flows = %+v, want exactly one (B's entry)", res.Flows)
	}
	approx(t, "opening flow", res.Flows[0].Amount, 1000)
	// Everything is flat at market → TWR ~0, not the +50 % the cost→market gap
	// would fabricate on the day B appears.
	got := perf.TWR(res.PerfPoints(false), res.PerfFlows())
	approx(t, "TWR", got, 0)
}
