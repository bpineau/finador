package portfolio

import (
	"errors"
	"strings"
	"testing"

	"finador/internal/domain"
)

// fxDaily crosses USD into EUR at a rate that moves by date, and knows no
// rate on 2026-06-09: what the estimate's conversion must be checked against.
type fxDaily struct{}

func (fxDaily) Convert(amount float64, from, to domain.Currency, d domain.Date) (float64, error) {
	if from == to {
		return amount, nil
	}
	rates := map[string]float64{"2026-06-05": 0.90, "2026-06-08": 0.95}
	if from != domain.USD || to != domain.EUR {
		return 0, errors.New("no such cross")
	}
	r, ok := rates[d.String()]
	if !ok {
		return 0, errors.New("no rate on " + d.String())
	}
	return amount * r, nil
}

// lagged makes cw8 (last published 560 EUR on 2026-06-05) a fund carried by
// a USD proxy closing at 100 that day, 105 the next session and 110 the one
// after, a day no rate covers.
func lagged(t *testing.T, onOpen bool, opens ...domain.PricePoint) *domain.Book {
	t.Helper()
	b := valuationBook(t)
	b.Market.Proxies = map[domain.AssetID]*domain.ProxyQuotes{"cw8": {
		Symbol: "URTH", Currency: domain.USD, OnOpen: onOpen,
		Closes: &domain.PriceSeries{Points: []domain.PricePoint{
			{Date: mustDate("2026-06-04"), Close: 99},
			{Date: mustDate("2026-06-05"), Close: 100},
			{Date: mustDate("2026-06-08"), Close: 105},
			{Date: mustDate("2026-06-09"), Close: 110},
		}},
		Opens: &domain.PriceSeries{Points: opens},
	}}
	return b
}

func TestPricesCarriesTheNAVByTheProxy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		onOpen bool
		opens  []domain.PricePoint
		want   float64
	}{
		// 560 × (105 × 0.95) / (100 × 0.90): the proxy's move in EUR.
		{"close anchor", false, nil, 560 * 105 * 0.95 / 90},
		// The NAV was struck on the open, 98 % of that day's close.
		{"open anchor", true, []domain.PricePoint{{Date: mustDate("2026-06-05"), Close: 0.98}}, 560 * 105 * 0.95 / (90 * 0.98)},
		// No opening print on the NAV's day: the close stands.
		{"open missing", true, []domain.PricePoint{{Date: mustDate("2026-06-04"), Close: 0.98}}, 560 * 105 * 0.95 / 90},
		// The open is ignored for a fund valued at the close.
		{"open unused", false, []domain.PricePoint{{Date: mustDate("2026-06-05"), Close: 0.98}}, 560 * 105 * 0.95 / 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := lagged(t, tc.onOpen, tc.opens...)
			prices, estimates := Prices(b, fxDaily{})
			s := prices["cw8"]
			if n := len(s.Points); n != 3 {
				t.Fatalf("points = %v, want the 2 published + 1 estimated (no rate on 06-09)", s.Points)
			}
			approx(t, "estimate", s.Points[2].Close, tc.want)
			if e, ok := estimates["cw8"]; !ok || e.NAV.Close != 560 || e.Proxy != "URTH" {
				t.Errorf("estimate = %+v, want the 560 NAV carried by URTH", e)
			}
			if pub := b.Market.Prices["cw8"]; len(pub.Points) != 2 {
				t.Errorf("the cached series was touched: %v", pub.Points)
			}
		})
	}
}

func TestPricesWithoutProxyIsTheCache(t *testing.T) {
	b := valuationBook(t)
	prices, estimates := Prices(b, fxDaily{})
	if prices["cw8"] != b.Market.Prices["cw8"] || len(estimates) != 0 {
		t.Errorf("no proxy: want the cached series as is, and no estimate")
	}
}

// TestValueAndSeriesAgreeOnTheEstimate: both read the same estimated day,
// both label it, and a valuation on the NAV's own day claims no estimate.
func TestValueAndSeriesAgreeOnTheEstimate(t *testing.T) {
	b := lagged(t, false)
	fx := fxDaily{}
	at := mustDate("2026-06-08")
	scope := scopeOf(t, b, "cw8")
	v, err := Value(b, scope, at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scope, mustDate("2026-06-05"), at, domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	approx(t, "series end vs value", last.Gross, v.Gross)
	approx(t, "gross", v.Gross, 14*560*105*0.95/90) // 12 in the PEA + 2 in the CTO
	note := "estimate for 2026-06-08"
	if !strings.Contains(strings.Join(v.Stale, "|"), note) {
		t.Errorf("value notes = %v, want %q", v.Stale, note)
	}
	if !strings.Contains(strings.Join(res.Warnings, "|"), note) {
		t.Errorf("series warnings = %v, want %q", res.Warnings, note)
	}

	v, err = Value(b, scope, mustDate("2026-06-05"), domain.EUR, fx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(v.Stale, "|"), "estimate") {
		t.Errorf("a valuation on the NAV's day claims an estimate: %v", v.Stale)
	}
}

// TestBreakdownLabelsTheEstimate: the tree's lines carry the label, said once
// for an asset held in two envelopes.
func TestBreakdownLabelsTheEstimate(t *testing.T) {
	b := lagged(t, false)
	lines, err := Breakdown(b, mustDate("2026-06-08"), domain.EUR, fxDaily{})
	if err != nil {
		t.Fatal(err)
	}
	notes := Notes(lines)
	if len(notes) != 1 || !strings.Contains(notes[0], "estimate for 2026-06-08") {
		t.Errorf("notes = %v, want the one estimate", notes)
	}
}
