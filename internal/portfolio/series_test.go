package portfolio

import (
	"testing"

	"finador/internal/domain"
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
	// PEA est suivi : ses trades sont internes. Seuls flux : deposit pea
	// +10000 (01-10) et buy cto +1100 (01-20, compte non suivi).
	if len(res.Flows) != 2 {
		t.Fatalf("flows = %+v, attendu 2", res.Flows)
	}
	if res.Flows[0].Date != mustDate("2026-01-10") {
		t.Errorf("flow[0] = %+v", res.Flows[0])
	}
	approx(t, "flow deposit", res.Flows[0].Amount, 10000)
	if res.Flows[1].Date != mustDate("2026-01-20") {
		t.Errorf("flow[1] = %+v", res.Flows[1])
	}
	approx(t, "flow buy cto", res.Flows[1].Amount, 1100)
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
