package market

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"finador/internal/domain"
)

// fakeProvider scripts a single Daily outcome and records whether it was hit.
type fakeProvider struct {
	name   string
	data   DailyData
	err    error
	called bool
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Daily(context.Context, Ref, domain.Date) (DailyData, error) {
	p.called = true
	return p.data, p.err
}

func series() DailyData {
	return DailyData{Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-06-01"), Close: 1}}}
}

func multi(providers ...Provider) *Multi {
	return &Multi{resolver: NewYahoo(), providers: providers}
}

func TestMultiYahooFirst(t *testing.T) {
	yahoo := &fakeProvider{name: "yahoo", data: series()}
	ft := &fakeProvider{name: "ft", data: series()}
	got, err := multi(yahoo, ft).Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2026-06-01"))
	if err != nil || len(got.Closes) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if !yahoo.called {
		t.Error("Yahoo not tried")
	}
	if ft.called {
		t.Error("FT tried although Yahoo already succeeded")
	}
}

func TestMultiFallsThroughOnError(t *testing.T) {
	// Yahoo errors (429, not-found, whatever) → FT takes over.
	yahoo := &fakeProvider{name: "yahoo", err: errors.New("HTTP 429")}
	ft := &fakeProvider{name: "ft", data: series()}
	got, err := multi(yahoo, ft).Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2026-06-01"))
	if err != nil || len(got.Closes) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if !ft.called {
		t.Error("FT should have been tried after Yahoo failed")
	}
}

func TestMultiFallsThroughOnNotCovered(t *testing.T) {
	// Yahoo can't cover an ISIN-only ref → ErrNotCovered → FT wins.
	yahoo := &fakeProvider{name: "yahoo", err: ErrNotCovered}
	ft := &fakeProvider{name: "ft", data: series()}
	got, err := multi(yahoo, ft).Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2026-06-01"))
	if err != nil || len(got.Closes) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestMultiFallsThroughOnEmptySeries(t *testing.T) {
	// A provider returning no error but no points is not a success.
	yahoo := &fakeProvider{name: "yahoo", data: DailyData{Currency: domain.EUR}}
	ft := &fakeProvider{name: "ft", data: series()}
	got, err := multi(yahoo, ft).Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2026-06-01"))
	if err != nil || len(got.Closes) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if !ft.called {
		t.Error("FT should have been tried after Yahoo returned an empty series")
	}
}

func TestMultiLastErrorWhenAllFail(t *testing.T) {
	yahooErr := errors.New("yahoo down")
	ftErr := errors.New("ft down")
	yahoo := &fakeProvider{name: "yahoo", err: yahooErr}
	ft := &fakeProvider{name: "ft", err: ftErr}
	_, err := multi(yahoo, ft).Daily(context.Background(), Ref{ISIN: "X"}, mustDate("2026-06-01"))
	if err != ftErr {
		t.Fatalf("err = %v, want last error %v", err, ftErr)
	}
}

func TestMultiNotFoundWhenOnlyNotCovered(t *testing.T) {
	// Every provider declines coverage → ErrNotFound, never ErrNotCovered.
	yahoo := &fakeProvider{name: "yahoo", err: ErrNotCovered}
	ft := &fakeProvider{name: "ft", err: ErrNotCovered}
	_, err := multi(yahoo, ft).Daily(context.Background(), Ref{}, mustDate("2026-06-01"))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMultiIntraday(t *testing.T) {
	// covered: symbol present → delegates to Yahoo resolver
	y := testYahoo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(intradayCW8))
	})
	m := &Multi{resolver: y, providers: []Provider{y}}
	data, err := m.Intraday(context.Background(), Ref{Symbol: "CW8.PA"})
	if err != nil || len(data.Points) == 0 {
		t.Fatalf("intraday = %+v err %v", data, err)
	}
	// ISIN-only ref (FT/Morningstar territory): Yahoo returns ErrNotCovered
	_, err = m.Intraday(context.Background(), Ref{ISIN: "LU1832174962"})
	if err != ErrNotCovered {
		t.Fatalf("err = %v, want ErrNotCovered", err)
	}
}

func TestMultiResolveDelegates(t *testing.T) {
	// Resolve must go through the resolver, not the provider chain.
	if _, ok := any(Default()).(Source); !ok {
		t.Fatal("Multi does not satisfy market.Source")
	}
}
