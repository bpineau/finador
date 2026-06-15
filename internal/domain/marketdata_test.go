package domain

import (
	"encoding/json"
	"testing"
)

func d(s string) Date {
	dd, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func TestPriceSeriesAt(t *testing.T) {
	s := &PriceSeries{}
	s.Merge([]PricePoint{
		{Date: d("2026-06-01"), Close: 100},
		{Date: d("2026-06-03"), Close: 103},
		{Date: d("2026-06-05"), Close: 105},
	})
	for _, tc := range []struct {
		at    string
		want  float64
		wDate string
		ok    bool
	}{
		{"2026-06-05", 105, "2026-06-05", true},
		{"2026-06-04", 103, "2026-06-03", true}, // forward-fill of the last close
		{"2026-06-01", 100, "2026-06-01", true},
		{"2026-05-31", 0, "", false}, // before the start of the series
		{"2026-07-01", 105, "2026-06-05", true},
	} {
		got, gDate, ok := s.At(d(tc.at))
		if ok != tc.ok || (ok && (got != tc.want || gDate != d(tc.wDate))) {
			t.Errorf("At(%s) = %v %v %v", tc.at, got, gDate, ok)
		}
	}
}

func TestPriceSeriesMergeUpsert(t *testing.T) {
	s := &PriceSeries{}
	s.Merge([]PricePoint{{Date: d("2026-06-01"), Close: 100}, {Date: d("2026-06-02"), Close: 101}})
	// overlap: the 02 is corrected, the 03 added; the order stays sorted
	s.Merge([]PricePoint{{Date: d("2026-06-02"), Close: 102}, {Date: d("2026-06-03"), Close: 103}})
	if len(s.Points) != 3 {
		t.Fatalf("points = %d, attendu 3", len(s.Points))
	}
	if v, _, _ := s.At(d("2026-06-02")); v != 102 {
		t.Errorf("upsert raté: %v", v)
	}
	last, ok := s.Last()
	if !ok || last.Close != 103 {
		t.Errorf("Last = %+v %v", last, ok)
	}
}

func TestMarketDataLazyAndJSON(t *testing.T) {
	b := NewBook()
	ps := b.Market.Price("cw8")
	ps.Merge([]PricePoint{{Date: d("2026-06-01"), Close: 550}})
	b.Market.FXSeries(EUR).Merge([]PricePoint{{Date: d("2026-06-01"), Close: 1.08}})
	b.Market.Dividends = map[AssetID][]DividendEvent{
		"cw8": {{ExDate: d("2026-03-10"), Amount: 1.5}},
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	back := NewBook()
	if err := json.Unmarshal(raw, back); err != nil {
		t.Fatal(err)
	}
	if v, _, ok := back.Market.Price("cw8").At(d("2026-06-02")); !ok || v != 550 {
		t.Fatalf("prix perdu au roundtrip: %v %v", v, ok)
	}
	if v, _, ok := back.Market.FXSeries(EUR).At(d("2026-06-01")); !ok || v != 1.08 {
		t.Fatalf("fx perdu: %v %v", v, ok)
	}
	if len(back.Market.Dividends["cw8"]) != 1 {
		t.Fatalf("dividendes perdus")
	}
}
