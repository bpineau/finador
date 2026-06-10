package portfolio

import (
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
	// PEA est suivi : ses trades sont internes.
	// La maison : son premier relevé est au 2026-01-01 (== from) → pas collecté (flux du jour de base).
	// Flux attendus :
	//   [0] 01-05 +12000 : adoption du livret (premier relevé cash, règle D8)
	//   [1] 01-10 +10000 : deposit pea
	//   [2] 01-20 +1100  : buy cto (compte non suivi)
	if len(res.Flows) != 3 {
		t.Fatalf("flows = %+v, attendu 3", res.Flows)
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
}

func TestSeriesExternalFlowsGroupScope(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, "actions"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// tous les trades sur cw8 sont des flux de la poche : +5000, +1100, +2750, −1800
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
	// au 12 janv : aucune clôture cw8 (la série commence le 20 mars) → la
	// position contribue 0 ; cash pea 10000 (déposé le 10), livret 12000
	// (relevé du 5), maison 400000 (relevé du 1er)
	last := res.Points[len(res.Points)-1]
	approx(t, "gross avant données marché", last.Gross, 10000+12000+400000)
}

func TestSeriesDefaultFrom(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), domain.Date{}, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// from zéro → première transaction du ledger (relevé maison du 1er janv)
	if res.Points[0].Date != mustDate("2026-01-01") {
		t.Errorf("premier point = %s", res.Points[0].Date)
	}
}

func TestSeriesAutoDividendFlows(t *testing.T) {
	b := valuationBook(t)
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	// portée groupe : le dividende sort de la poche → flux −(15+2)×2 ?
	// pea détient 15 parts au 1er mars, cto 2 → −34 au total
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
	// la première estimation de la maison (400000 le 1er janv) est une adoption :
	// elle doit apparaître comme flux externe, la seconde (450000) non.
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2025-12-25"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	var adoptions []ExternalFlow
	for _, f := range res.Flows {
		if f.Date == mustDate("2026-01-01") {
			adoptions = append(adoptions, f)
		}
	}
	if len(adoptions) != 1 {
		t.Fatalf("flux d'adoption au 2026-01-01 = %+v, attendu 1 seul", adoptions)
	}
	approx(t, "adoption maison", adoptions[0].Amount, 400000)
	for _, f := range res.Flows {
		if f.Date == mustDate("2026-06-01") {
			t.Fatalf("le 2e relevé ne doit pas être un flux: %+v", f)
		}
	}
}

func TestSeriesAdoptionFlowForCashStatement(t *testing.T) {
	b := valuationBook(t)
	// livret : premier relevé cash 12000 le 5 janv = adoption
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
	// sans la règle d'adoption, le TWR explose (>+4000 %) ; avec, il reste < 20 %
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

func TestSeriesMatchesValueWithWithholdingDividend(t *testing.T) {
	// le test d'or de base n'a pas de dividende : celui-ci verrouille la
	// retenue à la source identique des deux côtés (value.go ↔ series.go)
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
